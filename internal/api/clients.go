package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"fluxbox/internal/store"
)

// dhcpEntry 对应 OpenWrt / dnsmasq 的租约记录
type dhcpEntry struct {
	Expires  int64
	MAC      string
	IP       string
	Hostname string
	ClientID string
}

// 常用 MAC OUI 数据库 (前 6 位十六进制)
var macOUIVendors = map[string]string{
	// Apple
	"000393": "Apple", "000502": "Apple", "000A27": "Apple", "000A95": "Apple",
	"000D93": "Apple", "001124": "Apple", "001451": "Apple", "0016CB": "Apple",
	"0017F2": "Apple", "0019E3": "Apple", "001B63": "Apple", "001CB3": "Apple",
	"001D4F": "Apple", "001E52": "Apple", "001EC2": "Apple", "0021E9": "Apple",
	"002241": "Apple", "002312": "Apple", "002332": "Apple", "00236C": "Apple",
	"0023DF": "Apple", "002436": "Apple", "002500": "Apple", "00254B": "Apple",
	"0025BC": "Apple", "002608": "Apple", "00264A": "Apple", "0026B0": "Apple",
	"008865": "Apple", "040CCE": "Apple", "041552": "Apple", "042665": "Apple",
	"045453": "Apple", "086698": "Apple", "087045": "Apple", "0CBC9F": "Apple",
	"1093E9": "Apple", "1094BB": "Apple", "14109F": "Apple", "1499E2": "Apple",
	"18AF61": "Apple", "18E7F4": "Apple", "20EE28": "Apple", "286AB8": "Apple",
	"28CFE9": "Apple", "3408BC": "Apple", "34159E": "Apple", "34363B": "Apple",
	"38CADA": "Apple", "38F9D3": "Apple", "406C8F": "Apple", "40A6D9": "Apple",
	"444C0C": "Apple", "48D705": "Apple", "4C3275": "Apple", "4C57CA": "Apple",
	"50BC96": "Apple", "542696": "Apple", "5855CA": "Apple", "5CE91E": "Apple",
	"600308": "Apple", "60F445": "Apple", "64A5C3": "Apple", "68DBCA": "Apple",
	"6C4008": "Apple", "701124": "Apple", "705681": "Apple", "74E1B6": "Apple",
	"784F43": "Apple", "7C04D0": "Apple", "7C6D62": "Apple", "804971": "Apple",
	"80E650": "Apple", "843835": "Apple", "88665A": "Apple", "8C8590": "Apple",
	"90FD61": "Apple", "9801A7": "Apple", "9810E8": "Apple", "98E0D9": "Apple",
	"9C207B": "Apple", "9CE65E": "Apple", "A483E7": "Apple", "A8667F": "Apple",
	"AC1F74": "Apple", "ACBC32": "Apple", "ACFDEC": "Apple", "B4F0AB": "Apple",
	"B817C2": "Apple", "B8782E": "Apple", "BC52B7": "Apple", "BCFED9": "Apple",
	"C0847D": "Apple", "C869CD": "Apple", "CC29F4": "Apple", "D0034B": "Apple",
	"D4619D": "Apple", "D89E3F": "Apple", "DC2B61": "Apple", "E0B55D": "Apple",
	"E450EB": "Apple", "E4CE8F": "Apple", "E4E0A5": "Apple", "F01898": "Apple",
	"F4F15A": "Apple", "F81EDF": "Apple", "FCFC48": "Apple",

	// Xiaomi
	"009EC8": "Xiaomi", "04CF8C": "Xiaomi", "18F0E4": "Xiaomi", "286C07": "Xiaomi",
	"34CE00": "Xiaomi", "3480B3": "Xiaomi", "508F4C": "Xiaomi", "584498": "Xiaomi",
	"64CC2E": "Xiaomi", "640980": "Xiaomi", "68DFDD": "Xiaomi", "742344": "Xiaomi",
	"7802F8": "Xiaomi", "7811DC": "Xiaomi", "7C49EB": "Xiaomi", "84F3EB": "Xiaomi",
	"8CBEBE": "Xiaomi", "98FAE3": "Xiaomi", "A47733": "Xiaomi", "ACC1EE": "Xiaomi",
	"B072BF": "Xiaomi", "C403A8": "Xiaomi", "D4970B": "Xiaomi", "E4AAEC": "Xiaomi",
	"F8A450": "Xiaomi", "FCE557": "Xiaomi", "04A222": "Xiaomi", "14F65A": "Xiaomi",
	"1C5216": "Xiaomi", "2082C0": "Xiaomi", "28D127": "Xiaomi", "342912": "Xiaomi",
	"38539C": "Xiaomi", "3CCD5D": "Xiaomi", "4401BB": "Xiaomi", "5448E6": "Xiaomi",
	"584121": "Xiaomi", "60AB67": "Xiaomi", "6C02E0": "Xiaomi", "7451BA": "Xiaomi",
	"784476": "Xiaomi", "7C1CF1": "Xiaomi", "80AD16": "Xiaomi", "88C397": "Xiaomi",
	"8C53C3": "Xiaomi", "9C99A0": "Xiaomi", "A086C6": "Xiaomi", "A89CED": "Xiaomi",
	"B0A737": "Xiaomi", "BCEA2B": "Xiaomi", "C88D83": "Xiaomi", "D86375": "Xiaomi",
	"E446DA": "Xiaomi", "F0B429": "Xiaomi", "F8FFC2": "Xiaomi",

	// Huawei / Honor
	"001E10": "Huawei", "002568": "Huawei", "042528": "Huawei", "047970": "Huawei",
	"04C06F": "Huawei", "086361": "Huawei", "0C96BF": "Huawei", "104780": "Huawei",
	"10C61F": "Huawei", "18DED7": "Huawei", "200889": "Huawei", "20F3A3": "Huawei",
	"2469A5": "Huawei", "286ED4": "Huawei", "2CAB00": "Huawei", "342EB6": "Huawei",
	"384C90": "Huawei", "4846FB": "Huawei", "487D2E": "Huawei", "4C5499": "Huawei",
	"548998": "Huawei", "582AF7": "Huawei", "5C7D5E": "Huawei", "60DE44": "Huawei",
	"68A0F6": "Huawei", "7054F5": "Huawei", "786A89": "Huawei", "7C6097": "Huawei",
	"8038BC": "Huawei", "80B686": "Huawei", "845B12": "Huawei", "888603": "Huawei",
	"8C34FD": "Huawei", "9017AC": "Huawei", "90671C": "Huawei", "98924A": "Huawei",
	"A4CAA0": "Huawei", "AC853D": "Huawei", "B40B44": "Huawei", "B888E3": "Huawei",
	"BC7670": "Huawei", "C07009": "Huawei", "C49F4C": "Huawei", "C85195": "Huawei",
	"D46E5C": "Huawei", "D8490B": "Huawei", "DCD2FC": "Huawei", "E02861": "Huawei",
	"E435C8": "Huawei", "E8088B": "Huawei", "F09838": "Huawei", "F4559C": "Huawei",
	"F80113": "Huawei", "FC48EF": "Huawei",

	// Samsung
	"0007AB": "Samsung", "001247": "Samsung", "001599": "Samsung", "0018AF": "Samsung",
	"001D25": "Samsung", "0023D7": "Samsung", "0418D6": "Samsung", "08373D": "Samsung",
	"08D42B": "Samsung", "0C1420": "Samsung", "103047": "Samsung", "144D67": "Samsung",
	"183BD2": "Samsung", "1C5A3E": "Samsung", "244B81": "Samsung", "28CC01": "Samsung",
	"30CDA7": "Samsung", "3423BA": "Samsung", "380146": "Samsung", "3C8BFE": "Samsung",
	"404E36": "Samsung", "44F459": "Samsung", "4844F7": "Samsung", "508569": "Samsung",
	"549B12": "Samsung", "5CF6DC": "Samsung", "60A10A": "Samsung", "64B310": "Samsung",
	"68EBAE": "Samsung", "7445CE": "Samsung", "7840E4": "Samsung", "7CF85B": "Samsung",
	"8425DB": "Samsung", "88329B": "Samsung", "8C7712": "Samsung", "94652D": "Samsung",
	"9852B1": "Samsung", "A0821F": "Samsung", "A4EBD3": "Samsung", "AC5F3E": "Samsung",
	"B0C4E7": "Samsung", "B479A7": "Samsung", "B85E7B": "Samsung", "BC72B9": "Samsung",
	"C4731E": "Samsung", "C81479": "Samsung", "CC07AB": "Samsung", "D059E4": "Samsung",
	"D857EF": "Samsung", "E47CF9": "Samsung", "E8508B": "Samsung", "F47B5E": "Samsung",
	"FCA13E": "Samsung",

	// Intel
	"0002B3": "Intel", "000347": "Intel", "000423": "Intel", "0007E9": "Intel",
	"000E0C": "Intel", "001302": "Intel", "0013E8": "Intel", "001500": "Intel",
	"001676": "Intel", "0018DE": "Intel", "0019D1": "Intel", "001B77": "Intel",
	"001CC0": "Intel", "001E67": "Intel", "00215C": "Intel", "0022FA": "Intel",
	"002314": "Intel", "0024D7": "Intel", "0026C6": "Intel", "00270E": "Intel",
	"247703": "Intel", "3413E8": "Intel", "3CFDFE": "Intel", "4851B7": "Intel",
	"4C796E": "Intel", "5891CF": "Intel", "5C879C": "Intel", "644BF0": "Intel",
	"6805CA": "Intel", "6C8814": "Intel", "707781": "Intel", "782B46": "Intel",
	"7CB0C2": "Intel", "8086F2": "Intel", "84FDD1": "Intel", "887556": "Intel",
	"8C8D28": "Intel", "94E6F7": "Intel", "984FEE": "Intel", "A44CC8": "Intel",
	"AC7289": "Intel", "B49691": "Intel", "B88A60": "Intel", "C8F750": "Intel",
	"D0C637": "Intel", "D8F883": "Intel", "E4A471": "Intel", "E82A44": "Intel",
	"F8633F": "Intel",

	// Microsoft
	"0003FF": "Microsoft", "000D3A": "Microsoft", "00125A": "Microsoft", "00155D": "Microsoft",
	"0017FA": "Microsoft", "001DD8": "Microsoft", "002248": "Microsoft", "0025AE": "Microsoft",
	"0050F2": "Microsoft", "281878": "Microsoft", "3059B7": "Microsoft", "346F24": "Microsoft",
	"38BAF8": "Microsoft", "4886E8": "Microsoft", "501AC5": "Microsoft", "5882A8": "Microsoft",
	"6045BD": "Microsoft", "70480F": "Microsoft", "7C1E52": "Microsoft", "985FD3": "Microsoft",
	"B831B5": "Microsoft", "C0335E": "Microsoft", "C49DED": "Microsoft", "DC9840": "Microsoft",
	"EC59E7": "Microsoft",

	// Sony
	"00014A": "Sony", "00041F": "Sony", "000AD9": "Sony", "000E07": "Sony",
	"001315": "Sony", "0015C1": "Sony", "0019C5": "Sony", "001A80": "Sony",
	"001DBA": "Sony", "001FA7": "Sony", "00234D": "Sony", "0024BE": "Sony",
	"080046": "Sony", "0CFE45": "Sony", "1048B1": "Sony", "1430C6": "Sony",
	"18002D": "Sony", "280DFC": "Sony", "307512": "Sony", "380A94": "Sony",
	"4040A7": "Sony", "44D884": "Sony", "544249": "Sony", "709E29": "Sony",
	"748114": "Sony", "78843C": "Sony", "84D81B": "Sony", "9003B7": "Sony",
	"A0E453": "Sony", "AC9B0A": "Sony", "B00594": "Sony", "B8F934": "Sony",
	"C4366C": "Sony", "D8D43C": "Sony", "E063E5": "Sony", "F8461C": "Sony",

	// TP-Link
	"001D0F": "TP-Link", "0023CD": "TP-Link", "002586": "TP-Link", "002719": "TP-Link",
	"045EA4": "TP-Link", "085700": "TP-Link", "0C722C": "TP-Link", "10FEED": "TP-Link",
	"147590": "TP-Link", "14CF92": "TP-Link", "18A6F7": "TP-Link", "18D6C7": "TP-Link",
	"1C60DE": "TP-Link", "20DCE6": "TP-Link", "30B5C2": "TP-Link", "349672": "TP-Link",
	"388345": "TP-Link", "40313C": "TP-Link", "48EA63": "TP-Link", "50BD5F": "TP-Link",
	"54C80F": "TP-Link", "6032B1": "TP-Link", "645601": "TP-Link", "6466B3": "TP-Link",
	"647002": "TP-Link", "6CE873": "TP-Link", "708A09": "TP-Link", "7405A5": "TP-Link",
	"78D38D": "TP-Link", "7C8BCA": "TP-Link", "8416F9": "TP-Link", "882593": "TP-Link",
	"8C210A": "TP-Link", "90F652": "TP-Link", "984827": "TP-Link", "98DED0": "TP-Link",
	"A0F3C1": "TP-Link", "A42BB0": "TP-Link", "AC84C6": "TP-Link", "B0487A": "TP-Link",
	"B4B024": "TP-Link", "BC4699": "TP-Link", "C025E9": "TP-Link", "C0A5DD": "TP-Link",
	"C46E1F": "TP-Link", "CC32E5": "TP-Link", "D46E0E": "TP-Link", "D80D17": "TP-Link",
	"DC16B2": "TP-Link", "E4C32A": "TP-Link", "E848B8": "TP-Link", "EC086B": "TP-Link",
	"F4EC38": "TP-Link", "F81A67": "TP-Link",

	// ASUS
	"04D9F5": "ASUS", "08606E": "ASUS", "086266": "ASUS", "107B44": "ASUS",
	"10BF48": "ASUS", "14DDA9": "ASUS", "1C872C": "ASUS", "2C4D54": "ASUS",
	"2CFDA1": "ASUS", "3085A9": "ASUS", "382C4A": "ASUS", "40167E": "ASUS",
	"50465D": "ASUS", "54A050": "ASUS", "6045CB": "ASUS", "708BCD": "ASUS",

	// Raspberry Pi
	"B827EB": "Raspberry Pi", "DCA632": "Raspberry Pi", "E45F01": "Raspberry Pi", "28CDC1": "Raspberry Pi",

	// Google
	"001A11": "Google", "18B430": "Google", "20DFB9": "Google", "30074D": "Google",
	"3C5A37": "Google", "546009": "Google", "F40304": "Google", "F4F5E8": "Google",
}

// NormalizeMAC 将各类格式的 MAC 地址清洗为标准大写无分隔符字符串 (6 字节 hex)
func NormalizeMAC(mac string) string {
	clean := strings.ToUpper(mac)
	clean = strings.ReplaceAll(clean, ":", "")
	clean = strings.ReplaceAll(clean, "-", "")
	clean = strings.ReplaceAll(clean, ".", "")
	return strings.TrimSpace(clean)
}

// LookupVendor 根据 MAC 地址前缀匹配识别设备制造商品牌
func LookupVendor(mac string) string {
	clean := NormalizeMAC(mac)
	if len(clean) >= 6 {
		prefix := clean[:6]
		if v, ok := macOUIVendors[prefix]; ok {
			return v
		}
	}
	return ""
}

// ParseDHCPLeases 从 reader 解析 OpenWrt dnsmasq 的 dhcp.leases 记录
func ParseDHCPLeases(r io.Reader) map[string]dhcpEntry {
	result := make(map[string]dhcpEntry)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			// expires mac ip hostname [client_id]
			mac := fields[1]
			ip := fields[2]
			hostname := fields[3]
			if hostname == "*" {
				hostname = ""
			}
			clientID := ""
			if len(fields) >= 5 {
				clientID = fields[4]
			}

			entry := dhcpEntry{
				MAC:      mac,
				IP:       ip,
				Hostname: hostname,
				ClientID: clientID,
			}
			result[NormalizeMAC(mac)] = entry
			result[ip] = entry
		}
	}
	return result
}

// ParseDHCPLeasesFile 读取并解析 DHCP 租约文件
func ParseDHCPLeasesFile(path string) map[string]dhcpEntry {
	if path == "" {
		path = os.Getenv("FLUXBOX_DHCP_LEASES")
	}
	if path == "" {
		path = "/tmp/dhcp.leases"
	}
	f, err := os.Open(path)
	if err != nil {
		// 备选路径
		f, err = os.Open("/var/lib/misc/dnsmasq.leases")
		if err != nil {
			return make(map[string]dhcpEntry)
		}
	}
	defer f.Close()
	return ParseDHCPLeases(f)
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		clients := s.store.GetClients()
		if len(clients) == 0 {
			// 若为空尝试扫描一次
			clients = s.scanAndMergeClients()
			_ = s.store.SaveClients(clients)
		}
		s.writeJSON(w, map[string]any{"ok": true, "clients": clients})

	case http.MethodPost:
		var req struct {
			Clients []store.ClientInfo `json:"clients"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "无效的 clients 数组")
			return
		}

		// 规范化与处理用户别名 (Alias)
		for i := range req.Clients {
			c := &req.Clients[i]
			if c.ID == "" {
				c.ID = fmt.Sprintf("c-%s", NormalizeMAC(c.MAC))
				if c.ID == "c-" {
					c.ID = fmt.Sprintf("client-%s", strings.ReplaceAll(c.IP, ".", "-"))
				}
			}
			// 如果传入了 Alias，Name 优先等于 Alias
			if c.Alias != "" {
				c.Name = c.Alias
			} else if c.Name != "" && c.Name != c.IP {
				// 若用户修改了自定义名称，同步记录为 Alias
				c.Alias = c.Name
			}
			if c.Mode == "" {
				c.Mode = "rule"
			}
		}

		_ = s.store.SaveClients(req.Clients)

		// 实时同步至 Linux 内核 eBPF lan_clients_map
		if s.bpfEngine != nil {
			for _, c := range req.Clients {
				_ = s.bpfEngine.UpdateClientMode(c.IP, c.Mode)
			}
		}

		s.writeJSON(w, map[string]any{"ok": true, "clients": req.Clients})
	}
}

func (s *Server) handleClientsScan(w http.ResponseWriter, r *http.Request) {
	clients := s.scanAndMergeClients()
	_ = s.store.SaveClients(clients)
	s.writeJSON(w, map[string]any{"ok": true, "clients": clients, "count": len(clients)})
}

func (s *Server) handleClientDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		s.writeError(w, http.StatusMethodNotAllowed, "仅支持 DELETE 请求")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/clients/")
	saved := s.store.GetClients()
	var kept []store.ClientInfo
	var deletedIP string
	for _, c := range saved {
		if c.ID != id && c.IP != id {
			kept = append(kept, c)
		} else {
			deletedIP = c.IP
		}
	}
	_ = s.store.SaveClients(kept)

	if deletedIP != "" && s.bpfEngine != nil {
		_ = s.bpfEngine.UpdateClientMode(deletedIP, "rule")
	}

	s.writeJSON(w, map[string]any{"ok": true, "clients": kept})
}

// scanAndMergeClients 结合 /proc/net/arp 与 /tmp/dhcp.leases 扫描局域网设备，并与持久化已有配置（如 Alias、Mode）做平滑合并
func (s *Server) scanAndMergeClients() []store.ClientInfo {
	scanned := scanLanDevices("", "")
	saved := s.store.GetClients()

	type clientConfig struct {
		Mode  string
		Alias string
		Name  string
	}
	cfgMap := make(map[string]clientConfig)
	matchedSaved := make(map[string]bool)

	for _, c := range saved {
		cfg := clientConfig{
			Mode:  c.Mode,
			Alias: c.Alias,
			Name:  c.Name,
		}
		if c.IP != "" {
			cfgMap[c.IP] = cfg
		}
		if c.MAC != "" {
			cfgMap[NormalizeMAC(c.MAC)] = cfg
		}
	}

	for i := range scanned {
		c := &scanned[i]
		cleanMAC := NormalizeMAC(c.MAC)

		var cfg clientConfig
		var found bool
		if cfg, found = cfgMap[c.IP]; !found && cleanMAC != "" {
			cfg, found = cfgMap[cleanMAC]
		}

		if found {
			if c.IP != "" {
				matchedSaved[c.IP] = true
			}
			if cleanMAC != "" {
				matchedSaved[cleanMAC] = true
			}
			if cfg.Mode != "" {
				c.Mode = cfg.Mode
			}
			if cfg.Alias != "" {
				c.Alias = cfg.Alias
				c.Name = cfg.Alias
			} else if cfg.Name != "" && cfg.Name != c.IP {
				// 保留用户旧版本保存的非 IP 名称
				c.Name = cfg.Name
			}
		}
	}

	// 补充已保存但本次扫描未在线/未被 ARP 或 DHCP 捕获的离线或手动静态设备，防止配置丢失
	for _, c := range saved {
		cleanMAC := NormalizeMAC(c.MAC)
		if (c.IP != "" && matchedSaved[c.IP]) || (cleanMAC != "" && matchedSaved[cleanMAC]) {
			continue
		}
		offlineClient := c
		offlineClient.Online = false
		if offlineClient.Note == "" {
			offlineClient.Note = "已配置离线设备"
		}
		scanned = append(scanned, offlineClient)
	}

	return scanned
}

// scanLanDevices 读取 ARP 表与 DHCP 租约记录，并进行设备主机名与厂商品牌识别
func scanLanDevices(arpPath, dhcpPath string) []store.ClientInfo {
	if arpPath == "" {
		arpPath = os.Getenv("FLUXBOX_PROC_ARP")
	}
	if arpPath == "" {
		arpPath = "/proc/net/arp"
	}

	leases := ParseDHCPLeasesFile(dhcpPath)
	var list []store.ClientInfo
	seenIPs := make(map[string]bool)

	// 1. 读取 ARP 缓存表
	if f, err := os.Open(arpPath); err == nil {
		scanner := bufio.NewScanner(f)
		isFirst := true
		for scanner.Scan() {
			if isFirst {
				isFirst = false // 忽略标题行
				continue
			}
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 4 {
				ip := fields[0]
				flags := fields[2]
				mac := fields[3]
				// flags: 0x2 代表可达已完成解析的 ARP 记录
				if flags == "0x2" && mac != "00:00:00:00:00:00" {
					cleanMAC := NormalizeMAC(mac)
					vendor := LookupVendor(cleanMAC)

					hostname := ""
					if l, ok := leases[cleanMAC]; ok && l.Hostname != "" {
						hostname = l.Hostname
					} else if l, ok := leases[ip]; ok && l.Hostname != "" {
						hostname = l.Hostname
					}

					// 智能设备命名策略: Hostname > Vendor 设备 > IP
					displayName := hostname
					if displayName == "" {
						if vendor != "" {
							displayName = fmt.Sprintf("%s 设备", vendor)
						} else {
							displayName = ip
						}
					}

					note := "局域网在线设备"
					if vendor != "" {
						note = fmt.Sprintf("%s 局域网设备", vendor)
					}

					clientID := "c-" + strings.ToLower(cleanMAC)
					if cleanMAC == "" {
						clientID = fmt.Sprintf("client-%s", strings.ReplaceAll(ip, ".", "-"))
					}

					list = append(list, store.ClientInfo{
						ID:       clientID,
						Name:     displayName,
						IP:       ip,
						MAC:      mac,
						Mode:     "rule",
						Online:   true,
						Hostname: hostname,
						Vendor:   vendor,
						Note:     note,
					})
					seenIPs[ip] = true
				}
			}
		}
		_ = f.Close()
	}

	// 2. 补充 DHCP 租约中存在但暂未出现在 ARP 活跃表的设备
	for _, entry := range leases {
		if entry.IP == "" || seenIPs[entry.IP] {
			continue
		}
		cleanMAC := NormalizeMAC(entry.MAC)
		vendor := LookupVendor(cleanMAC)
		displayName := entry.Hostname
		if displayName == "" {
			if vendor != "" {
				displayName = fmt.Sprintf("%s 设备", vendor)
			} else {
				displayName = entry.IP
			}
		}

		note := "DHCP 分配设备"
		if vendor != "" {
			note = fmt.Sprintf("%s 局域网设备", vendor)
		}

		clientID := "c-" + strings.ToLower(cleanMAC)
		if cleanMAC == "" {
			clientID = fmt.Sprintf("client-%s", strings.ReplaceAll(entry.IP, ".", "-"))
		}

		list = append(list, store.ClientInfo{
			ID:       clientID,
			Name:     displayName,
			IP:       entry.IP,
			MAC:      entry.MAC,
			Mode:     "rule",
			Online:   false,
			Hostname: entry.Hostname,
			Vendor:   vendor,
			Note:     note,
		})
		seenIPs[entry.IP] = true
	}

	return list
}
