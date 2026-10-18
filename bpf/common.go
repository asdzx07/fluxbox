package bpf

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror" fluxbox fluxbox_tc.c

const (
	ClientModeRule   uint8 = 0
	ClientModeDirect uint8 = 1
	ClientModeProxy  uint8 = 2
)
