//go:build linux

package bpf

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
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
	// 默认写入局域网保留网段与中国大陆 8,000+ CIDR 到直连 LPM 表，防止内网与国内流量误进代理
	eng.initPrivateCIDRs()
	_ = eng.InitChinaCIDRs("")
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
	var key fluxboxIpv4LpmKey
	key.Prefixlen = uint32(ones)
	copy(key.Addr[:], ipNet.IP.To4())
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

func (e *Engine) GetStats() (Stats, error) {
	var s Stats
	if e.objs.FluxboxStatsMap == nil {
		return s, nil
	}
	var directPerCPU, proxyPerCPU, shotPerCPU []uint64
	var k0, k1, k2 uint32 = 0, 1, 2
	if err := e.objs.FluxboxStatsMap.Lookup(&k0, &directPerCPU); err == nil {
		for _, v := range directPerCPU {
			s.DirectPackets += v
		}
	}
	if err := e.objs.FluxboxStatsMap.Lookup(&k1, &proxyPerCPU); err == nil {
		for _, v := range proxyPerCPU {
			s.ProxyPackets += v
		}
	}
	if err := e.objs.FluxboxStatsMap.Lookup(&k2, &shotPerCPU); err == nil {
		for _, v := range shotPerCPU {
			s.ShotPackets += v
		}
	}
	return s, nil
}

// AddLearnedDirectIP 将 sing-box 规则引擎判定的直连 IP 动态刻入 eBPF LRU 哈希表
func (e *Engine) AddLearnedDirectIP(ipStr string) error {
	parsed := net.ParseIP(strings.TrimSpace(ipStr)).To4()
	if parsed == nil {
		return fmt.Errorf("invalid ipv4: %s", ipStr)
	}
	ipKey := binary.LittleEndian.Uint32(parsed)
	val := uint8(1)
	if e.objs.LearnedIpMap == nil {
		return nil
	}
	return e.objs.LearnedIpMap.Put(&ipKey, &val)
}

// InitChinaCIDRs 将内置的 8,000+ 条中国大陆 IPv4 CIDR 列表高速注入内核 LPM Trie 树
func (e *Engine) InitChinaCIDRs(customPath string) int {
	var r io.Reader
	if customPath != "" {
		if f, err := os.Open(customPath); err == nil {
			defer f.Close()
			r = f
		}
	}
	if r == nil {
		if len(defaultChinaIPv4Bytes) == 0 {
			return 0
		}
		r = bytes.NewReader(defaultChinaIPv4Bytes)
	}

	scanner := bufio.NewScanner(r)
	count := 0
	val := uint8(1)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		slash := strings.IndexByte(line, '/')
		if slash == -1 {
			continue
		}
		ipStr := line[:slash]
		maskStr := line[slash+1:]
		ip := net.ParseIP(ipStr).To4()
		if ip == nil {
			continue
		}
		var mask uint32
		for i := 0; i < len(maskStr); i++ {
			if maskStr[i] >= '0' && maskStr[i] <= '9' {
				mask = mask*10 + uint32(maskStr[i]-'0')
			}
		}
		if mask > 32 {
			continue
		}

		key := fluxboxIpv4LpmKey{
			Prefixlen: mask,
			Addr:      [4]byte{ip[0], ip[1], ip[2], ip[3]},
		}
		if err := e.objs.DirectCidrMap.Update(&key, &val, ebpf.UpdateAny); err == nil {
			count++
		}
	}
	return count
}

func (e *Engine) Close() error {
	return e.objs.Close()
}
