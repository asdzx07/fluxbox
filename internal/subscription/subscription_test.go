package subscription

import (
	"encoding/base64"
	"testing"
)

// 测试 Clash YAML 中的 VMess 转换为 sing-box Outbound
func TestConvertClashVMessToSingBox(t *testing.T) {
	clashYaml := `
proxies:
  - name: "VMess-WS-TLS"
    type: vmess
    server: example.com
    port: 443
    uuid: 12345678-1234-1234-1234-123456789abc
    alterId: 0
    cipher: auto
    tls: true
    servername: sni.example.com
    skip-cert-verify: true
    network: ws
    ws-opts:
      path: "/vmess-ws"
      headers:
        Host: sni.example.com

  - name: "VMess-gRPC"
    type: vmess
    server: 1.2.3.4
    port: 8443
    uuid: 87654321-4321-4321-4321-cba987654321
    alterId: 16
    cipher: aes-128-gcm
    tls: false
    network: grpc
    grpc-opts:
      grpc-service-name: "test-grpc-service"

  - name: "VMess-WebSocket-TopLevel"
    type: vmess
    server: ws.example.org
    port: 8080
    uuid: aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee
    network: websocket
    ws-path: "/custom-ws"
    ws-headers:
      Host: ws.example.org

  - name: "VMess-HTTP-SlicePath"
    type: vmess
    server: h2.example.org
    port: 443
    uuid: bbbbbbbb-cccc-dddd-eeee-ffffffffffff
    network: http
    http-opts:
      path:
        - "/video"

  - name: "VMess-Missing-UUID"
    type: vmess
    server: bad.example.org
    port: 443
`

	nodes, err := parseClashYAML([]byte(clashYaml))
	if err != nil {
		t.Fatalf("parseClashYAML 失败: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("预期解析 4 个合规节点 (第 5 个缺失 UUID 应被过滤), 实际得到 %d", len(nodes))
	}

	// 验证第一个 WS 节点
	n1 := nodes[0]
	if n1["type"] != "vmess" {
		t.Errorf("n1.type 应为 vmess, 实际: %v", n1["type"])
	}
	if n1["tag"] != "VMess-WS-TLS" || n1["server"] != "example.com" || n1["server_port"] != 443 {
		t.Errorf("n1 基础属性不匹配: %+v", n1)
	}
	if n1["uuid"] != "12345678-1234-1234-1234-123456789abc" || n1["alter_id"] != 0 || n1["security"] != "auto" {
		t.Errorf("n1 vmess 核心参数不匹配: %+v", n1)
	}
	tlsObj, ok := n1["tls"].(map[string]any)
	if !ok || tlsObj["enabled"] != true || tlsObj["server_name"] != "sni.example.com" || tlsObj["insecure"] != true {
		t.Errorf("n1 tls 配置错误: %+v", n1["tls"])
	}
	transObj, ok := n1["transport"].(map[string]any)
	if !ok || transObj["type"] != "ws" || transObj["path"] != "/vmess-ws" {
		t.Errorf("n1 transport 配置错误: %+v", n1["transport"])
	}

	// 验证第二个 gRPC 节点
	n2 := nodes[1]
	if n2["type"] != "vmess" {
		t.Errorf("n2.type 应为 vmess, 实际: %v", n2["type"])
	}
	if n2["alter_id"] != 16 || n2["security"] != "aes-128-gcm" {
		t.Errorf("n2 vmess 核心参数不匹配: %+v", n2)
	}
	if _, hasTLS := n2["tls"]; hasTLS {
		t.Errorf("n2 未启用 TLS, 不应包含 tls 对象")
	}
	transObj2, ok := n2["transport"].(map[string]any)
	if !ok || transObj2["type"] != "grpc" || transObj2["service_name"] != "test-grpc-service" {
		t.Errorf("n2 transport 配置错误: %+v", n2["transport"])
	}

	// 验证第三个 websocket 别名与顶层 ws-path 节点
	n3 := nodes[2]
	if n3["tag"] != "VMess-WebSocket-TopLevel" {
		t.Errorf("n3.tag 期望 VMess-WebSocket-TopLevel, 实际 %v", n3["tag"])
	}
	transObj3, ok := n3["transport"].(map[string]any)
	if !ok || transObj3["type"] != "ws" || transObj3["path"] != "/custom-ws" {
		t.Errorf("n3 transport 期望 ws /custom-ws, 实际 %+v", n3["transport"])
	}

	// 验证第四个 http 数组路径节点
	n4 := nodes[3]
	transObj4, ok := n4["transport"].(map[string]any)
	if !ok || transObj4["type"] != "http" || transObj4["path"] != "/video" {
		t.Errorf("n4 transport 期望 http /video, 实际 %+v", n4["transport"])
	}
}

// 测试 Shadowsocks URI 解析 (SIP002 与 Legacy Base64)
func TestParseShadowsocksURI(t *testing.T) {
	tests := []struct {
		name       string
		uri        string
		wantType   string
		wantTag    string
		wantServer string
		wantPort   int
		wantMethod string
		wantPass   string
	}{
		{
			name:       "Legacy 全段 Base64",
			uri:        "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:my-secret-pw@198.51.100.10:8388")) + "#香港-SS-01",
			wantType:   "shadowsocks",
			wantTag:    "香港-SS-01",
			wantServer: "198.51.100.10",
			wantPort:   8388,
			wantMethod: "aes-256-gcm",
			wantPass:   "my-secret-pw",
		},
		{
			name:       "Legacy Base64 无 Tag (自动回退 Host)",
			uri:        "ss://" + base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pass123@ss.example.org:1080")),
			wantType:   "shadowsocks",
			wantTag:    "ss.example.org",
			wantServer: "ss.example.org",
			wantPort:   1080,
			wantMethod: "chacha20-ietf-poly1305",
			wantPass:   "pass123",
		},
		{
			name:       "Legacy 整体 Base64 包含 #Tag",
			uri:        "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pass456@1.1.1.1:8888#内置Tag")),
			wantType:   "shadowsocks",
			wantTag:    "内置Tag",
			wantServer: "1.1.1.1",
			wantPort:   8888,
			wantMethod: "aes-128-gcm",
			wantPass:   "pass456",
		},
		{
			name:       "SIP002 标准格式 (Base64 UserInfo)",
			uri:        "ss://" + base64.URLEncoding.EncodeToString([]byte("2022-blake3-aes-128-gcm:secret@key")) + "@ss2.example.com:9000#日本-SIP002",
			wantType:   "shadowsocks",
			wantTag:    "日本-SIP002",
			wantServer: "ss2.example.com",
			wantPort:   9000,
			wantMethod: "2022-blake3-aes-128-gcm",
			wantPass:   "secret@key",
		},
		{
			name:       "SIP002 格式带插件参数 query",
			uri:        "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pass789")) + "@192.0.2.1:443/?plugin=v2ray-plugin%3Bhost%3Dcdn.example.com#新加坡-Plugin",
			wantType:   "shadowsocks",
			wantTag:    "新加坡-Plugin",
			wantServer: "192.0.2.1",
			wantPort:   443,
			wantMethod: "aes-128-gcm",
			wantPass:   "pass789",
		},
		{
			name:       "SIP002 IPv6 地址格式",
			uri:        "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:ipv6pass")) + "@[2001:db8::1]:8388#IPv6-Node",
			wantType:   "shadowsocks",
			wantTag:    "IPv6-Node",
			wantServer: "2001:db8::1",
			wantPort:   8388,
			wantMethod: "aes-256-gcm",
			wantPass:   "ipv6pass",
		},
		{
			name:       "SIP002 Base64 包含 '+' 特殊字符（防 QueryUnescape 误转为空格）",
			uri:        "ss://YWVzLTEyOC1nY206cD4/Pg==@198.51.100.20:8388#Plus-Node",
			wantType:   "shadowsocks",
			wantTag:    "Plus-Node",
			wantServer: "198.51.100.20",
			wantPort:   8388,
			wantMethod: "aes-128-gcm",
			wantPass:   "p>?>",
		},
		{
			name:       "Legacy Base64 整体携带外部插件 query 与斜杠",
			uri:        "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pass123@1.2.3.4:8388")) + "/?plugin=obfs-local#Legacy-Plugin",
			wantType:   "shadowsocks",
			wantTag:    "Legacy-Plugin",
			wantServer: "1.2.3.4",
			wantPort:   8388,
			wantMethod: "aes-128-gcm",
			wantPass:   "pass123",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := parseSingleURI(tc.uri)
			if node == nil {
				t.Fatalf("解析失败，返回 nil")
			}
			if node["type"] != tc.wantType {
				t.Errorf("type 期望 %s, 实际 %v", tc.wantType, node["type"])
			}
			if node["tag"] != tc.wantTag {
				t.Errorf("tag 期望 %s, 实际 %v", tc.wantTag, node["tag"])
			}
			if node["server"] != tc.wantServer {
				t.Errorf("server 期望 %s, 实际 %v", tc.wantServer, node["server"])
			}
			if node["server_port"] != tc.wantPort {
				t.Errorf("server_port 期望 %d, 实际 %v", tc.wantPort, node["server_port"])
			}
			if node["method"] != tc.wantMethod {
				t.Errorf("method 期望 %s, 实际 %v", tc.wantMethod, node["method"])
			}
			if node["password"] != tc.wantPass {
				t.Errorf("password 期望 %s, 实际 %v", tc.wantPass, node["password"])
			}
		})
	}
}

// 测试非法 SS URI 容错
func TestParseShadowsocksURIFailures(t *testing.T) {
	badURIs := []string{
		"ss://",
		"ss://not-base64",
		"ss://invalid@invalid:abc",
		"ss://@1.2.3.4:8388",
		"ss://YWVzLTEyOC1nY206cGFzc3dvcmQ=@",
		"ss://YWVzLTEyOC1nY206cGFzc3dvcmQ=@1.2.3.4:0",
		"ss://YWVzLTEyOC1nY206cGFzc3dvcmQ=@1.2.3.4:70000",
		"ss://YWVzLTEyOC1nY206@1.2.3.4:8388",
		"ss://:password@1.2.3.4:8388",
		"ss://YWVzLTEyOC1nY206cGFzc3dvcmQ=@1.2.3.4:-1",
	}

	for _, u := range badURIs {
		res := parseSingleURI(u)
		if res != nil {
			t.Errorf("解析非法 URI %q 期望返回 nil, 但得到了: %+v", u, res)
		}
	}
}

// 测试批量 URI 行解析与标签去重
func TestParseURILines(t *testing.T) {
	ss1 := "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pass1@1.1.1.1:8388")) + "#节点A"
	ss2 := "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pass2@2.2.2.2:8388")) + "#节点A" // 重名

	raw := ss1 + "\n" + ss2
	nodes := deduplicateTags(parseURILines(raw))
	if len(nodes) != 2 {
		t.Fatalf("期望 2 个节点, 实际: %d", len(nodes))
	}
	if nodes[0]["tag"] != "节点A" {
		t.Errorf("第一个节点 tag 期望 节点A, 实际: %v", nodes[0]["tag"])
	}
	if nodes[1]["tag"] != "节点A #2" {
		t.Errorf("第二个重名节点 tag 期望 节点A #2, 实际: %v", nodes[1]["tag"])
	}
}
