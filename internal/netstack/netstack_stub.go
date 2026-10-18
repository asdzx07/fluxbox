//go:build !linux

package netstack

import (
	"log"
)

func Setup() error {
	log.Printf("[NETSTACK-STUB] 非 Linux 环境，跳过策略路由与 TProxy 规则挂载")
	return nil
}

func Teardown() error {
	log.Printf("[NETSTACK-STUB] 非 Linux 环境，跳过策略路由与 TProxy 规则清理")
	return nil
}
