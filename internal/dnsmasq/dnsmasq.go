package dnsmasq

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
)

var (
	mu          sync.Mutex
	hijacked    bool
	currentPort int
)

// Status 返回当前 dnsmasq 接管状态
func Status() map[string]any {
	mu.Lock()
	defer mu.Unlock()

	hasUci := false
	if _, err := exec.LookPath("uci"); err == nil {
		hasUci = true
	}

	return map[string]any{
		"hijacked": hijacked,
		"port":     currentPort,
		"hasUci":   hasUci,
	}
}

// Hijack 使用 UCI 命令将 dnsmasq 的上游劫持至 sing-box 监听的本地端口
func Hijack(port int) error {
	mu.Lock()
	defer mu.Unlock()

	if _, err := exec.LookPath("uci"); err != nil {
		log.Printf("[DNSMASQ] 系统缺少 uci 工具 (非 OpenWrt/iStoreOS)，跳过 dnsmasq 自动接管")
		hijacked = true
		currentPort = port
		return nil
	}

	commands := [][]string{
		{"uci", "set", "dhcp.@dnsmasq[0].noresolv=1"},
		{"uci", "-q", "delete", "dhcp.@dnsmasq[0].server"},
		{"uci", "add_list", fmt.Sprintf("dhcp.@dnsmasq[0].server=127.0.0.1#%d", port)},
		{"uci", "commit", "dhcp"},
	}

	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("execute %s failed: %v (%s)", strings.Join(args, " "), err, string(out))
		}
	}

	// 重启 dnsmasq 生效
	if out, err := exec.Command("/etc/init.d/dnsmasq", "restart").CombinedOutput(); err != nil {
		_ = exec.Command("/etc/init.d/dnsmasq", "reload").Run()
		log.Printf("[DNSMASQ] 重启 dnsmasq 警告: %v (%s)", err, string(out))
	}

	hijacked = true
	currentPort = port
	log.Printf("[DNSMASQ] 已成功将 DNS 请求劫持至 127.0.0.1#%d", port)
	return nil
}

// Restore 还原 dnsmasq 默认配置，防止 FluxBox 停止后局域网断网
func Restore() error {
	mu.Lock()
	defer mu.Unlock()

	if !hijacked {
		return nil
	}

	if _, err := exec.LookPath("uci"); err != nil {
		hijacked = false
		return nil
	}

	commands := [][]string{
		{"uci", "set", "dhcp.@dnsmasq[0].noresolv=0"},
		{"uci", "-q", "delete", "dhcp.@dnsmasq[0].server"},
		{"uci", "commit", "dhcp"},
	}

	for _, args := range commands {
		_ = exec.Command(args[0], args[1:]...).Run()
	}

	_ = exec.Command("/etc/init.d/dnsmasq", "restart").Run()
	hijacked = false
	currentPort = 0
	log.Printf("[DNSMASQ] 已还原 dnsmasq 原生上游配置")
	return nil
}
