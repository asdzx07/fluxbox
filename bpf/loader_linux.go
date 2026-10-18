//go:build linux

package bpf

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type Engine struct {
	mu       sync.Mutex
	objs     fluxboxObjects
	iface    string
	attached bool
}

func NewEngine(ifaceName string) (*Engine, error) {
	// 解除旧内核 eBPF 内存锁定限制
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("remove memlock: %w", err)
	}

	var objs fluxboxObjects
	if err := loadFluxboxObjects(&objs, nil); err != nil {
		return nil, fmt.Errorf("load bpf objects: %w", err)
	}

	eng := &Engine{
		objs:  objs,
		iface: ifaceName,
	}
	// 默认写入局域网保留网段到直连 LPM 表，防止内网互访进代理
	eng.initPrivateCIDRs()
	return eng, nil
}

// AttachTC 将 eBPF 程序挂载到软路由网口 (如 br-lan 或 eth0) 的 TC Ingress
func (e *Engine) AttachTC() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	link, err := netlink.LinkByName(e.iface)
	if err != nil {
		return fmt.Errorf("find interface %s: %w", e.iface, err)
	}

	// 1. 确保 clsact qdisc 存在
	attrs := netlink.QdiscAttrs{
		LinkIndex: link.Attrs().Index,
		Handle:    netlink.MakeHandle(0xffff, 0),
		Parent:    netlink.HANDLE_CLSACT,
	}
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: attrs,
		QdiscType:  "clsact",
	}
	_ = netlink.QdiscReplace(qdisc)

	if e.objs.FluxboxTcIngress == nil {
		return fmt.Errorf("bpf tc program is nil")
	}

	// 2. 挂载 eBPF TC Filter (Direct Action 模式)
	filter := &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: link.Attrs().Index,
			Parent:    netlink.HANDLE_MIN_INGRESS,
			Handle:    1,
			Protocol:  unix.ETH_P_ALL,
			Priority:  1,
		},
		Fd:           e.objs.FluxboxTcIngress.FD(),
		Name:         "fluxbox_tc_ingress",
		DirectAction: true,
	}
	if err := netlink.FilterReplace(filter); err != nil {
		return fmt.Errorf("attach tc bpf filter: %w", err)
	}

	e.attached = true
	return nil
}

// UpdateClientMode 毫秒级动态更新「内网分流」设备策略（无需重启任何服务）
func (e *Engine) UpdateClientMode(ipStr string, mode string) error {
	parsed := net.ParseIP(ipStr).To4()
	if parsed == nil {
		return fmt.Errorf("invalid ipv4: %s", ipStr)
	}
	// 网络字节序存储 (与内核 __be32 一致)
	ipKey := binary.LittleEndian.Uint32(parsed)

	var modeVal uint8 = ClientModeRule
	switch mode {
	case "direct":
		modeVal = ClientModeDirect
	case "proxy":
		modeVal = ClientModeProxy
	default:
		modeVal = ClientModeRule
	}

	if e.objs.LanClientsMap == nil {
		return nil
	}

	if modeVal == ClientModeRule {
		// 默认规则模式无需占表，直接移除即可
		_ = e.objs.LanClientsMap.Delete(&ipKey)
		return nil
	}
	return e.objs.LanClientsMap.Put(&ipKey, &modeVal)
}

// AddDirectCIDR 向内核 LPM Trie 树注入直连 CIDR（如国内 IP 段或节点服务器自身 IP）
func (e *Engine) AddDirectCIDR(cidrStr string) error {
	_, ipNet, err := net.ParseCIDR(cidrStr)
	if err != nil {
		// 支持直接传入单个 IP
		ip := net.ParseIP(cidrStr).To4()
		if ip == nil {
			return err
		}
		_, ipNet, _ = net.ParseCIDR(cidrStr + "/32")
	}

	if e.objs.DirectCidrMap == nil {
		return nil
	}

	ones, _ := ipNet.Mask.Size()
	key := fluxboxIpv4LpmKey{
		Prefixlen: uint32(ones),
		Addr:      binary.LittleEndian.Uint32(ipNet.IP.To4()),
	}
	val := uint8(1)
	return e.objs.DirectCidrMap.Update(&key, &val, ebpf.UpdateAny)
}

// SetGlobalFlags 动态控制「直连不进内核」与「屏蔽 QUIC」开关
func (e *Engine) SetGlobalFlags(directBypass, rejectQuic bool) error {
	if e.objs.GlobalCfgMap == nil {
		return nil
	}
	var k0, k1 uint32 = 0, 1
	var vBypass, vQuic uint32
	if directBypass {
		vBypass = 1
	}
	if rejectQuic {
		vQuic = 1
	}
	if err := e.objs.GlobalCfgMap.Put(&k0, &vBypass); err != nil {
		return err
	}
	return e.objs.GlobalCfgMap.Put(&k1, &vQuic)
}

func (e *Engine) initPrivateCIDRs() {
	privates := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"224.0.0.0/4",
		"255.255.255.255/32",
	}
	for _, cidr := range privates {
		_ = e.AddDirectCIDR(cidr)
	}
}

func (e *Engine) Close() error {
	return e.objs.Close()
}
