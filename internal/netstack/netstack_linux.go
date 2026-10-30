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

	// 1. 旁路由单臂必备：NAT postrouting 伪装，保证客户端将网关指向旁路由时的回包正常
	if err := applyNatNft(); err != nil {
		return fmt.Errorf("apply nat nftables: %w", err)
	}

	// 2. 策略路由：带有 FwmarkBypass (0x2024) 的流量直接查询 main 路由表走直连，防止内核出站自回环与死锁
	if err := applyBypassRule(); err != nil {
		return fmt.Errorf("apply bypass rule: %w", err)
	}

	log.Printf("[OK] 旁路由单臂网络栈就绪 (IP 转发 + NAT 伪装回包 + 策略路由闭环)")
	return nil
}

func applyBypassRule() error {
	// 添加前先检查或清理旧规则防止重复
	cleanupBypassRule()

	rule := netlink.NewRule()
	rule.Family = syscall.AF_INET
	rule.Priority = PrioBypass
	rule.Mark = FwmarkBypass
	rule.Table = 254 // syscall.RT_TABLE_MAIN (main 路由表)
	if err := netlink.RuleAdd(rule); err != nil {
		// 降级使用 ip 命令行重试
		cmd := exec.Command("ip", "rule", "add", "pref", fmt.Sprintf("%d", PrioBypass), "fwmark", fmt.Sprintf("0x%x", FwmarkBypass), "table", "main")
		if out, errCmd := cmd.CombinedOutput(); errCmd != nil {
			return fmt.Errorf("netlink rule add: %v, cmd rule add: %v (%s)", err, errCmd, string(out))
		}
	}
	return nil
}

func cleanupBypassRule() {
	// 遍历 netlink 策略路由清理：匹配优先级或 0x2024 bypass 标记
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err == nil {
		for _, r := range rules {
			if r.Priority == PrioBypass || r.Mark == FwmarkBypass {
				rCopy := r
				_ = netlink.RuleDel(&rCopy)
			}
		}
	}
	// 执行 ip rule del 确保清理彻底 (针对可能存在的重复规则循环清理直至无匹配)
	for i := 0; i < 5; i++ {
		if err := exec.Command("ip", "rule", "del", "pref", fmt.Sprintf("%d", PrioBypass)).Run(); err != nil {
			break
		}
	}
	for i := 0; i < 5; i++ {
		if err := exec.Command("ip", "rule", "del", "fwmark", fmt.Sprintf("0x%x", FwmarkBypass), "table", "main").Run(); err != nil {
			break
		}
	}
}

func Teardown() error {
	_ = exec.Command("nft", "delete", "table", "inet", "fluxbox_tproxy").Run()
	_ = exec.Command("nft", "delete", "table", "inet", "fluxbox_nat").Run()

	// 清理 pref 8999 策略路由规则
	cleanupBypassRule()

	// 强力清理可能残留的旧版 table 2022、rule 8999、9000、9001、9002、32768 等死锁规则
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err == nil {
		for _, r := range rules {
			if r.Table == RouteTableID || r.Priority == 8999 || r.Priority == 9000 || r.Priority == 9001 || r.Priority == 9002 || r.Priority == 32768 {
				rCopy := r
				_ = netlink.RuleDel(&rCopy)
			}
		}
	}

	_ = exec.Command("ip", "rule", "del", "table", fmt.Sprintf("%d", RouteTableID)).Run()
	_ = exec.Command("ip", "route", "flush", "table", fmt.Sprintf("%d", RouteTableID)).Run()

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

func applyNatNft() error {
	// 旁路由单臂必备：NAT postrouting 伪装，保证客户端将网关指向旁路由时的回包正常
	nftScript := `
table inet fluxbox_nat {
	chain postrouting {
		type nat hook postrouting priority srcnat; policy accept;
		oifname != "lo" ip saddr 192.168.0.0/16 masquerade
		oifname != "lo" ip saddr 10.0.0.0/8 masquerade
	}
}
`
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