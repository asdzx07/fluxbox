package bpf

import (
	"bytes"
	"fmt"
	"io"

	"github.com/cilium/ebpf"
)

type fluxboxPrograms struct {
	FluxboxTcIngress *ebpf.Program `ebpf:"fluxbox_tc_ingress"`
}

func (p *fluxboxPrograms) Close() error {
	return closeClosers(
		p.FluxboxTcIngress,
	)
}

type fluxboxMaps struct {
	LanClientsMap *ebpf.Map `ebpf:"lan_clients_map"`
	DirectCidrMap *ebpf.Map `ebpf:"direct_cidr_map"`
	GlobalCfgMap  *ebpf.Map `ebpf:"global_cfg_map"`
}

func (m *fluxboxMaps) Close() error {
	return closeClosers(
		m.LanClientsMap,
		m.DirectCidrMap,
		m.GlobalCfgMap,
	)
}

type fluxboxObjects struct {
	fluxboxPrograms
	fluxboxMaps
}

func (o *fluxboxObjects) Close() error {
	return closeClosers(
		&o.fluxboxPrograms,
		&o.fluxboxMaps,
	)
}

type fluxboxIpv4LpmKey struct {
	Prefixlen uint32
	Addr      uint32
}

func closeClosers(closers ...io.Closer) error {
	for _, closer := range closers {
		if closer != nil {
			_ = closer.Close()
		}
	}
	return nil
}

// loadFluxboxObjects 优先从内嵌编译字节码加载；若未内嵌则安全报错供上层捕获
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

// fluxboxElfBytes 存储预编译或构建期注入的 eBPF 字节码
var fluxboxElfBytes []byte
