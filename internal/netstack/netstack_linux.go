//go:build linux

package netstack

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/vishvananda/netlink"
)

const (
	FwmarkTProxy = 0x2023
	FwmarkBypass = 0x2024
	RouteTableID = 2022
	TProxyPort   = 7893

	PrioBypass = 8999
	PrioTProxy = 9001
)

// AutoDetectLanIface 自动探测软路由主网卡 (优先 br-lan，其次默认路由网卡如 eth0)
func AutoDetectLanIface() string {
	if env := os.Getenv("FLUXBOX_IFACE"); env != "" {
		return env
	}
	if _, err := net.InterfaceByName("br-lan"); err == nil {
		return "br-lan"
	}
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err == nil {
		for _, r := range routes {
			if r.Dst == nil || r.Dst.IP.Equal(net.IPv4zero) {
				if link, err := netlink.LinkByIndex(r.LinkIndex); err == nil {
					return link.Attrs().Name
				}
			}
		}
	}
	return "eth0"
}

func Setup() error {
	enableSysctl()
	_ = Teardown()

	// 1. 加载内核 TProxy 必需模块 (部分 OpenWrt 默认未挂载 nft_tproxy)
	_ = exec.Command("modprobe", "nft_tproxy").Run()
	_ = exec.Command("modprobe", "xt_TPROXY").Run()

	// 2. 配置 table 2022 local 路由
	loLink, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("find lo: %w", err)
	}
	_, defaultDst, _ := net.ParseCIDR("0.0.0.0/0")
	localRoute := &netlink.Route{
		LinkIndex: loLink.Attrs().Index,
		Scope:     netlink.SCOPE_HOST,
		Dst:       defaultDst,
		Table:     RouteTableID,
		Type:      syscall.RTN_LOCAL,
	}
	if err := netlink.RouteReplace(localRoute); err != nil {
		return fmt.Errorf("route replace table %d: %w", RouteTableID, err)
	}

	// 3. 配置 ip rule 8999 (0x2024 直连绕过) 与 9001 (0x2023 进 TProxy)
	mask := uint32(0xffffffff)
	ruleBypass := netlink.NewRule()
	ruleBypass.Priority = PrioBypass
	ruleBypass.Mark = FwmarkBypass
	ruleBypass.Mask = &mask
	ruleBypass.Table = syscall.RT_TABLE_MAIN
	_ = netlink.RuleAdd(ruleBypass)

	ruleTProxy := netlink.NewRule()
	ruleTProxy.Priority = PrioTProxy
	ruleTProxy.Mark = FwmarkTProxy
	ruleTProxy.Mask = &mask
	ruleTProxy.Table = RouteTableID
	_ = netlink.RuleAdd(ruleTProxy)

	// 4. 挂载完整版 nftables TProxy + DNS 劫持 + 旁路由 SNAT 规则
	if err := applyTProxyNft(); err != nil {
		return fmt.Errorf("apply nftables: %w", err)
	}

	log.Printf("[OK] 透明代理网络栈就绪 (TProxy :%d + DNS 劫持 + 旁路由伪装)", TProxyPort)
	return nil
}

func Teardown() error {
	_ = exec.Command("nft", "delete", "table", "inet", "fluxbox_tproxy").Run()

	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err == nil {
		for _, r := range rules {
			if r.Priority == PrioBypass || r.Priority == PrioTProxy || r.Table == RouteTableID {
				rCopy := r
				_ = netlink.RuleDel(&rCopy)
			}
		}
	}

	if loLink, err := netlink.LinkByName("lo"); err == nil {
		_, defaultDst, _ := net.ParseCIDR("0.0.0.0/0")
		_ = netlink.RouteDel(&netlink.Route{
			LinkIndex: loLink.Attrs().Index,
			Scope:     netlink.SCOPE_HOST,
			Dst:       defaultDst,
			Table:     RouteTableID,
			Type:      syscall.RTN_LOCAL,
		})
	}
	return nil
}

func applyTProxyNft() error {
	// 关键修复：
	// 1) 放行 0x2024 (sing-box 自身流量与面板拉订阅流量)
	// 2) 劫持所有 53 端口 DNS 请求进 TProxy (解决客户端指向旁路由后无法解析域名问题)
	// 3) 放行发往本机私有 IP 的非 DNS 流量 (保护 SSH 22 / 面板 3036 / LuCI 80 不断连)
	// 4) 将其余所有局域网转发流量与 eBPF 打标 0x2023 的流量送入 sing-box TProxy 7893 端口
	nftScript := fmt.Sprintf(`
table inet fluxbox_tproxy {
	set private_v4 {
		type ipv4_addr
		flags interval
		elements = { 10.0.0.0/8, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.168.0.0/16, 224.0.0.0/4, 240.0.0.0/4 }
	}

	chain prerouting {
		type filter hook prerouting priority mangle; policy accept;

		# 1. 放行 sing-box 和面板自身发出的直连流量
		meta mark 0x%x return

		# 2. 强制接管局域网所有 DNS 请求 (UDP/TCP 53) 送入 sing-box hijack-dns
		meta l4proto { tcp, udp } th dport 53 tproxy ip to 127.0.0.1:%d meta mark set 0x%x accept

		# 3. 绕过访问内网/软路由管理后台的流量
		ip daddr @private_v4 return

		# 4. 接管被 eBPF 标记或局域网路由过来的外网流量
		meta l4proto { tcp, udp } tproxy ip to 127.0.0.1:%d meta mark set 0x%x accept
	}

	chain postrouting {
		type nat hook postrouting priority srcnat; policy accept;
		# 5. 旁路由单臂模式必备：为直连放行流量自动做源地址伪装，防止异步路由丢包
		oifname != "lo" ip saddr 192.168.0.0/16 masquerade
		oifname != "lo" ip saddr 10.0.0.0/8 masquerade
	}
}
`, FwmarkBypass, TProxyPort, FwmarkTProxy, TProxyPort, FwmarkTProxy)

	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(nftScript)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, string(out))
	}
	return nil
}

func enableSysctl() {
	sysctls := map[string]string{
		"/proc/sys/net/ipv4/ip_forward":             "1",
		"/proc/sys/net/ipv4/conf/all/rp_filter":     "0",
		"/proc/sys/net/ipv4/conf/default/rp_filter": "0",
		"/proc/sys/net/ipv4/conf/br-lan/rp_filter":  "0",
		"/proc/sys/net/ipv4/conf/eth0/rp_filter":    "0",
	}
	for path, val := range sysctls {
		_ = os.WriteFile(path, []byte(val), 0644)
	}
}