//go:build !linux

package bpf

import (
	"log"
)

type Engine struct {
	iface string
}

func NewEngine(ifaceName string) (*Engine, error) {
	log.Printf("[BPF-STUB] 非 Linux 环境，初始化模拟 eBPF 引擎 (网卡: %s)", ifaceName)
	return &Engine{iface: ifaceName}, nil
}

func (e *Engine) AttachTC() error {
	log.Printf("[BPF-STUB] 模拟 AttachTC 到 %s 成功", e.iface)
	return nil
}

func (e *Engine) UpdateClientMode(ipStr string, mode string) error {
	log.Printf("[BPF-STUB] 模拟更新设备策略: IP=%s, Mode=%s", ipStr, mode)
	return nil
}

func (e *Engine) AddDirectCIDR(cidrStr string) error {
	log.Printf("[BPF-STUB] 模拟添加直连 CIDR: %s", cidrStr)
	return nil
}

func (e *Engine) SetGlobalFlags(directBypass, rejectQuic bool) error {
	log.Printf("[BPF-STUB] 模拟设置全局标志: DirectBypass=%v, RejectQuic=%v", directBypass, rejectQuic)
	return nil
}

func (e *Engine) Close() error {
	log.Printf("[BPF-STUB] 模拟关闭 eBPF 引擎")
	return nil
}
