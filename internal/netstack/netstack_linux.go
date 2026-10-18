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
	FwmarkTProxy = 0x2023 // eBPF TC 打标的代理流量
	FwmarkBypass = 0x2024 // sing-box 自身出站直连标记
	RouteTableID = 2022   // 专属策略路由表 ID
	TProxyPort   = 7893   // 对应 configgen.go 中的 sing-box tproxy 监听端口

	PrioBypass = 8999 // 优先放行 sing-box 自身流量
	PrioTProxy = 9001 // 拦截被 eBPF 标记的局域网流量
)

// Setup 在 FluxBox 启动时自动初始化 Linux 策略路由与 TProxy 交接表
func Setup() error {
	// 1. 开启 Linux 内核转发并放宽 rp_filter (防止 TProxy 伪造源地址包被内核丢弃)
	enableSysctl()

	// 2. 幂等清理可能残留的旧规则
	_ = Teardown()

	// 3. 在 table 2022 中添加 local 0.0.0.0/0 dev lo 路由
	// 作用：欺骗内核将 fwmark 0x2023 的转发包视为发往本机(local)，从而移交上层 Socket
	loLink, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("找不到回环网卡 lo: %w", err)
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
		return fmt.Errorf("写入 table %d 路由失败: %w", RouteTableID, err)
	}

	// 4. 添加 ip rule 8999: fwmark 0x2024 lookup main (防回环直连)
	mask := uint32(0xffffffff)
	ruleBypass := netlink.NewRule()
	ruleBypass.Priority = PrioBypass
	ruleBypass.Mark = FwmarkBypass
	ruleBypass.Mask = &mask
	ruleBypass.Table = syscall.RT_TABLE_MAIN
	if err := netlink.RuleAdd(ruleBypass); err != nil {
		return fmt.Errorf("添加 0x2024 bypass rule 失败: %w", err)
	}

	// 5. 添加 ip rule 9001: fwmark 0x2023 lookup 2022 (导向 TProxy 表)
	ruleTProxy := netlink.NewRule()
	ruleTProxy.Priority = PrioTProxy
	ruleTProxy.Mark = FwmarkTProxy
	ruleTProxy.Mask = &mask
	ruleTProxy.Table = RouteTableID
	if err := netlink.RuleAdd(ruleTProxy); err != nil {
		return fmt.Errorf("添加 0x2023 tproxy rule 失败: %w", err)
	}

	// 6. 挂载极简 nftables TProxy Socket 映射规则 (仅处理被 eBPF 打上 0x2023 的包)
	if err := applyTProxyNft(); err != nil {
		return fmt.Errorf("挂载 TProxy 交接表失败: %w", err)
	}

	log.Printf("[OK] 策略路由初始化完成 (fwmark 0x%x -> table %d -> TProxy :%d)", FwmarkTProxy, RouteTableID, TProxyPort)
	return nil
}

// Teardown 在 FluxBox 退出或停止内核时彻底还原网络栈（防止软路由断网）
func Teardown() error {
	// 1. 删除 nftables 专属表
	_ = exec.Command("nft", "delete", "table", "inet", "fluxbox_tproxy").Run()

	// 2. 移除我们添加的两条 ip rule
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err == nil {
		for _, r := range rules {
			if r.Priority == PrioBypass || r.Priority == PrioTProxy || r.Table == RouteTableID {
				rCopy := r
				_ = netlink.RuleDel(&rCopy)
			}
		}
	}

	// 3. 清空 table 2022 路由表
	loLink, err := netlink.LinkByName("lo")
	if err == nil {
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
	// 所有的分流判断（内网设备直连、国内 CIDR 旁路、QUIC 阻断）已在 eBPF TC 层完成！
	// 这里只需一行匹配 meta mark 0x2023 即可零开销移交给 sing-box TProxy 端口
	nftScript := fmt.Sprintf(`
table inet fluxbox_tproxy {
	chain prerouting {
		type filter hook prerouting priority mangle; policy accept;
		meta mark 0x%x meta l4proto { tcp, udp } tproxy ip to 127.0.0.1:%d accept
	}
}
`, FwmarkTProxy, TProxyPort)

	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(nftScript)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, string(out))
	}
	return nil
}

func enableSysctl() {
	sysctls := map[string]string{
		"/proc/sys/net/ipv4/ip_forward":          "1",
		"/proc/sys/net/ipv4/conf/all/rp_filter":  "0",
		"/proc/sys/net/ipv4/conf/br-lan/rp_filter": "0",
	}
	for path, val := range sysctls {
		_ = os.WriteFile(path, []byte(val), 0644)
	}
}