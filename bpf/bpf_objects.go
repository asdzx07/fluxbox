package bpf

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"

	"github.com/cilium/ebpf"
)

// 直接将 Clang 编译出的 eBPF ELF 目标文件嵌入 Go 二进制
//go:embed fluxbox_tc.o
var fluxboxElfBytes []byte

// 直接将权威精选的中国大陆 8,000+ 条 IPv4 CIDR 列表嵌入 Go 二进制
//go:embed china_ipv4.txt
var defaultChinaIPv4Bytes []byte

type fluxboxPrograms struct {
	FluxboxTcIngress *ebpf.Program `ebpf:"fluxbox_tc_ingress"`
}

func (p *fluxboxPrograms) Close() error {
	return closeClosers(p.FluxboxTcIngress)
}

type fluxboxMaps struct {
	LanClientsMap   *ebpf.Map `ebpf:"lan_clients_map"`
	DirectCidrMap   *ebpf.Map `ebpf:"direct_cidr_map"`
	LearnedIpMap    *ebpf.Map `ebpf:"learned_ip_map"`
	GlobalCfgMap    *ebpf.Map `ebpf:"global_cfg_map"`
	FluxboxStatsMap *ebpf.Map `ebpf:"fluxbox_stats_map"`
}

func (m *fluxboxMaps) Close() error {
	return closeClosers(
		m.LanClientsMap,
		m.DirectCidrMap,
		m.LearnedIpMap,
		m.GlobalCfgMap,
		m.FluxboxStatsMap,
	)
}

type fluxboxObjects struct {
	fluxboxPrograms
	fluxboxMaps
}

func (o *fluxboxObjects) Close() error {
	return closeClosers(&o.fluxboxPrograms, &o.fluxboxMaps)
}

type fluxboxIpv4LpmKey struct {
	Prefixlen uint32
	Addr      [4]byte
}

func closeClosers(closers ...io.Closer) error {
	for _, closer := range closers {
		if closer != nil {
			_ = closer.Close()
		}
	}
	return nil
}

func loadFluxboxObjects(obj interface{}, opts *ebpf.CollectionOptions) error {
	if len(fluxboxElfBytes) == 0 {
		return fmt.Errorf("fluxbox eBPF ELF bytecode not embedded")
	}
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(fluxboxElfBytes))
	if err != nil {
		return fmt.Errorf("load eBPF spec: %w", err)
	}
	return spec.LoadAndAssign(obj, opts)
}
