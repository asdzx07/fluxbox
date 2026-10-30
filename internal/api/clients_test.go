package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fluxbox/internal/store"
)

// 验证常见厂商 MAC OUI 识别能力
func TestLookupVendor(t *testing.T) {
	testCases := []struct {
		mac      string
		expected string
	}{
		{"38:f9:d3:11:22:33", "Apple"},
		{"E4-5F-01-AA-BB-CC", "Raspberry Pi"},
		{"78:02:F8:12:34:56", "Xiaomi"},
		{"00:1e:10:99:88:77", "Huawei"},
		{"00:07:ab:00:11:22", "Samsung"},
		{"00:02:B3:33:44:55", "Intel"},
		{"28:18:78:66:77:88", "Microsoft"},
		{"00:01:4A:aa:bb:cc", "Sony"},
		{"14:75:90:55:66:77", "TP-Link"},
		{"00:00:00:00:00:00", ""},
		{"invalid-mac", ""},
	}

	for _, tc := range testCases {
		res := LookupVendor(tc.mac)
		if res != tc.expected {
			t.Errorf("LookupVendor(%s) = %s, 预期 %s", tc.mac, res, tc.expected)
		}
	}
}

// 验证 OpenWrt dhcp.leases 格式解析
func TestParseDHCPLeases(t *testing.T) {
	rawLeases := `
1730000000 38:f9:d3:11:22:33 192.168.1.101 MacBook-Pro 01:38:f9:d3:11:22:33
1730000000 78:02:f8:12:34:56 192.168.1.102 Xiaomi-14 *
1730000000 00:11:22:33:44:55 192.168.1.103 * 01:00:11:22:33:44:55
# 注释行应该被忽略
`
	leases := ParseDHCPLeases(strings.NewReader(rawLeases))

	// 验证 MacBook-Pro
	mbp, ok := leases["192.168.1.101"]
	if !ok || mbp.Hostname != "MacBook-Pro" {
		t.Fatalf("未能解析 MacBook-Pro: %+v", mbp)
	}

	// 验证 Xiaomi-14
	mi, ok := leases[NormalizeMAC("78:02:f8:12:34:56")]
	if !ok || mi.Hostname != "Xiaomi-14" {
		t.Fatalf("未能解析 Xiaomi-14: %+v", mi)
	}

	// 验证 * 视为空 Hostname
	anon, ok := leases["192.168.1.103"]
	if !ok || anon.Hostname != "" {
		t.Fatalf("未能将 * 视为空 Hostname: %+v", anon)
	}
}

// 验证 ARP 与 DHCP 拓扑融合及智能设备命名与别名保留
func TestScanAndMergeClients(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "clients-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	arpContent := `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.101    0x1         0x2         38:f9:d3:11:22:33     *        br-lan
192.168.1.102    0x1         0x2         78:02:f8:12:34:56     *        br-lan
192.168.1.104    0x1         0x2         00:07:ab:00:11:22     *        br-lan
`
	dhcpContent := `
1730000000 38:f9:d3:11:22:33 192.168.1.101 MacBook-Pro 01:38:f9:d3:11:22:33
1730000000 78:02:f8:12:34:56 192.168.1.102 Xiaomi-14 *
1730000000 00:07:ab:00:11:22 192.168.1.104 * 01:00:07:ab:00:11:22
`
	arpFile := filepath.Join(tempDir, "arp")
	dhcpFile := filepath.Join(tempDir, "dhcp.leases")
	_ = os.WriteFile(arpFile, []byte(arpContent), 0644)
	_ = os.WriteFile(dhcpFile, []byte(dhcpContent), 0644)

	t.Setenv("FLUXBOX_PROC_ARP", arpFile)
	t.Setenv("FLUXBOX_DHCP_LEASES", dhcpFile)

	st, err := store.NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}

	// 预先设定用户自定义别名与模式
	_ = st.SaveClients([]store.ClientInfo{
		{
			ID:    "c-38f9d3112233",
			Name:  "我的工作机",
			Alias: "我的工作机",
			IP:    "192.168.1.101",
			Mode:  "proxy",
		},
	})

	server := NewServer(st, nil, nil)

	// 调用 scanAndMergeClients
	merged := server.scanAndMergeClients()
	if len(merged) < 3 {
		t.Fatalf("扫描到的设备数不足 3 个: %d", len(merged))
	}

	var foundWorkstation, foundXiaomi, foundSamsung bool
	for _, c := range merged {
		if c.IP == "192.168.1.101" {
			foundWorkstation = true
			if c.Name != "我的工作机" || c.Alias != "我的工作机" {
				t.Errorf("用户别名未保留: %+v", c)
			}
			if c.Mode != "proxy" {
				t.Errorf("用户模式未保留: %+v", c)
			}
			if c.Vendor != "Apple" {
				t.Errorf("Apple 厂商识别错误: %+v", c)
			}
		}
		if c.IP == "192.168.1.102" {
			foundXiaomi = true
			if c.Name != "Xiaomi-14" {
				t.Errorf("Hostname 命名优化失败: %+v", c)
			}
			if c.Vendor != "Xiaomi" {
				t.Errorf("Xiaomi 厂商识别错误: %+v", c)
			}
		}
		if c.IP == "192.168.1.104" {
			foundSamsung = true
			// 无 Hostname，应采用 Vendor 设备 命名
			if c.Name != "Samsung 设备" {
				t.Errorf("无 Hostname 时 Vendor 设备 命名失败: %+v", c)
			}
			if c.Vendor != "Samsung" {
				t.Errorf("Samsung 厂商识别错误: %+v", c)
			}
		}
	}

	if !foundWorkstation || !foundXiaomi || !foundSamsung {
		t.Fatalf("未全部匹配到测试设备: %+v", merged)
	}

	// 验证 HTTP 接口保存别名
	updateReqBody := `{"clients":[{"id":"c-7802f8123456","ip":"192.168.1.102","name":"客厅电视","alias":"客厅电视","mode":"direct"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/clients", bytes.NewBufferString(updateReqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.handleClients(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/clients 返回 %d", w.Code)
	}

	savedClients := st.GetClients()
	if len(savedClients) != 1 || savedClients[0].Alias != "客厅电视" || savedClients[0].Mode != "direct" {
		t.Fatalf("持久化保存的别名不符合预期: %+v", savedClients)
	}
}

// 验证 handleClientDelete 严格检查 HTTP DELETE 请求方法，防止 GET 等误触删除
func TestClientDelete_MethodCheck(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "client-del-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := store.NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}

	_ = st.SaveClients([]store.ClientInfo{
		{ID: "c-112233445566", IP: "192.168.1.50", Name: "NAS", Mode: "direct"},
	})

	server := NewServer(st, nil, nil)
	defer server.Close()

	// 1. 发送 GET 请求应被拒绝 (405)
	getReq := httptest.NewRequest(http.MethodGet, "/api/clients/c-112233445566", nil)
	getRec := httptest.NewRecorder()
	server.handleClientDelete(getRec, getReq)

	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/clients/:id 应返回 405，实际返回 %d", getRec.Code)
	}
	if len(st.GetClients()) != 1 {
		t.Fatalf("GET 请求误删除了设备！")
	}

	// 2. 发送 DELETE 请求应成功执行删除
	delReq := httptest.NewRequest(http.MethodDelete, "/api/clients/c-112233445566", nil)
	delRec := httptest.NewRecorder()
	server.handleClientDelete(delRec, delReq)

	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/clients/:id 应返回 200，实际返回 %d", delRec.Code)
	}
	if len(st.GetClients()) != 0 {
		t.Fatalf("DELETE 请求未成功删除设备: %+v", st.GetClients())
	}
}

// 验证 scanAndMergeClients 在重新扫描时完整保留未在线的离线或手动静态设备
func TestScanAndMergeClients_PreservesOfflineClients(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "clients-offline-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// ARP 与 DHCP 均不包含 192.168.1.200 (NAS)
	arpContent := "IP address       HW type     Flags       HW address            Mask     Device\n192.168.1.101    0x1         0x2         38:f9:d3:11:22:33     *        br-lan\n"
	dhcpContent := "1730000000 38:f9:d3:11:22:33 192.168.1.101 MacBook-Pro 01:38:f9:d3:11:22:33\n"

	arpFile := filepath.Join(tempDir, "arp")
	dhcpFile := filepath.Join(tempDir, "dhcp.leases")
	_ = os.WriteFile(arpFile, []byte(arpContent), 0644)
	_ = os.WriteFile(dhcpFile, []byte(dhcpContent), 0644)

	t.Setenv("FLUXBOX_PROC_ARP", arpFile)
	t.Setenv("FLUXBOX_DHCP_LEASES", dhcpFile)

	st, err := store.NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}

	// 预先存入手动添加的静态设备
	_ = st.SaveClients([]store.ClientInfo{
		{ID: "client-192-168-1-200", IP: "192.168.1.200", Name: "家用NAS", Alias: "家用NAS", Mode: "direct", Online: true},
	})

	server := NewServer(st, nil, nil)
	defer server.Close()

	merged := server.scanAndMergeClients()
	var foundNAS bool
	for _, c := range merged {
		if c.IP == "192.168.1.200" {
			foundNAS = true
			if c.Name != "家用NAS" || c.Alias != "家用NAS" {
				t.Errorf("离线设备的别名未保留: %+v", c)
			}
			if c.Online {
				t.Errorf("本次未扫描到的设备应标记为离线: %+v", c)
			}
		}
	}

	if !foundNAS {
		t.Fatalf("scanAndMergeClients 误删除了已保存但离线的设备！")
	}
}
