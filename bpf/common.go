package bpf

const (
	ClientModeRule   uint8 = 0
	ClientModeDirect uint8 = 1
	ClientModeProxy  uint8 = 2
)

// Stats 记录内核 eBPF 旁路引擎的处理包统计
type Stats struct {
	DirectPackets uint64 `json:"direct_packets"`
	ProxyPackets  uint64 `json:"proxy_packets"`
	ShotPackets   uint64 `json:"shot_packets"`
}
