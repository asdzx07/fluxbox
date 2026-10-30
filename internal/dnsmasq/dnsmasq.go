package dnsmasq

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

var (
	mu          sync.Mutex
	hijacked    bool
	currentPort int

	// 配置文件位于内存虚拟文件系统 /tmp，掉电或重启即自动消失，彻底杜绝 Flash 写入磨损
	confDir  = "/tmp/dnsmasq.d"
	confFile = "/tmp/dnsmasq.d/fluxbox.conf"
)

// SetCustomPaths 供单元测试动态设置配置文件路径
func SetCustomPaths(dir, file string) {
	mu.Lock()
	defer mu.Unlock()
	confDir = dir
	confFile = file
}

// ResetDefaultPaths 还原默认路径
func ResetDefaultPaths() {
	mu.Lock()
	defer mu.Unlock()
	confDir = "/tmp/dnsmasq.d"
	confFile = "/tmp/dnsmasq.d/fluxbox.conf"
}

// Status 返回当前 dnsmasq 接管状态
func Status() map[string]any {
	mu.Lock()
	defer mu.Unlock()

	hasUci := false
	if _, err := exec.LookPath("uci"); err == nil {
		hasUci = true
	}
	hasUbus := false
	if _, err := exec.LookPath("ubus"); err == nil {
		hasUbus = true
	}

	return map[string]any{
		"hijacked":   hijacked,
		"port":       currentPort,
		"configFile": confFile,
		"hasUci":     hasUci,
		"hasUbus":    hasUbus,
	}
}

// Hijack 向内存虚拟文件系统写入 /tmp/dnsmasq.d/fluxbox.conf 并重载 dnsmasq
func Hijack(port int) error {
	mu.Lock()
	defer mu.Unlock()

	if port <= 0 || port > 65535 {
		return fmt.Errorf("无效的劫持端口: %d", port)
	}

	// 1. 确保目标目录存在
	if err := os.MkdirAll(confDir, 0755); err != nil {
		return fmt.Errorf("创建 dnsmasq 配置目录失败: %w", err)
	}

	// 2. 生成内存 drop-in 配置文件内容
	content := fmt.Sprintf("# 由 FluxBox 自动生成，将 DNS 劫持至本地 sing-box\n# 位于内存虚拟文件系统，断电自动消失，保护 Flash 寿命\nno-resolv\nserver=127.0.0.1#%d\n", port)
	if err := os.WriteFile(confFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入 dnsmasq drop-in 配置文件失败: %w", err)
	}

	// 兼容 OpenWrt 命名实例目录（例如 /tmp/dnsmasq.cfg01411c.d）
	if matches, err := filepath.Glob("/tmp/dnsmasq.*.d"); err == nil {
		for _, m := range matches {
			instConf := filepath.Join(m, "fluxbox.conf")
			_ = os.WriteFile(instConf, []byte(content), 0644)
		}
	}

	// 3. 触发服务重载
	reloadDnsmasqService()

	hijacked = true
	currentPort = port
	log.Printf("[DNSMASQ] 已成功将 DNS 请求劫持至 127.0.0.1#%d (通过内存配置 %s)", port, confFile)
	return nil
}

// Restore 删除内存中的 drop-in 配置文件并重载 dnsmasq 还原原生解析
func Restore() error {
	mu.Lock()
	defer mu.Unlock()

	// 检查 drop-in 配置文件是否存在
	_, err := os.Stat(confFile)
	fileExists := (err == nil)

	// 若未处于劫持状态且文件不存在，则无需重复重载服务，直接返回
	if !hijacked && !fileExists {
		return nil
	}

	// 1. 删除 drop-in 配置文件
	if fileExists {
		if err := os.Remove(confFile); err != nil && !os.IsNotExist(err) {
			log.Printf("[DNSMASQ] 删除 drop-in 配置文件警告: %v", err)
		}
	}

	// 清理 OpenWrt 命名实例目录下的配置文件
	if matches, err := filepath.Glob("/tmp/dnsmasq.*.d/fluxbox.conf"); err == nil {
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}

	// 2. 触发服务重载
	reloadDnsmasqService()

	hijacked = false
	currentPort = 0
	log.Printf("[DNSMASQ] 已还原 dnsmasq 原生上游配置 (已移除 %s)", confFile)
	return nil
}

// reloadDnsmasqService 重启/重载 dnsmasq 服务以重新加载配置
func reloadDnsmasqService() {
	// dnsmasq 规范明确指出 SIGHUP (reload) 不会重新读取配置文件指令，必须使用 restart
	initScript := "/etc/init.d/dnsmasq"
	if _, err := os.Stat(initScript); err == nil {
		if out, err := exec.Command(initScript, "restart").CombinedOutput(); err == nil {
			log.Printf("[DNSMASQ] 已通过 init.d restart 成功应用 dnsmasq 配置")
			return
		} else {
			log.Printf("[DNSMASQ] init.d restart 警告: %v (%s)", err, string(out))
		}
	}

	if _, err := exec.LookPath("ubus"); err == nil {
		cmd := exec.Command("ubus", "call", "service", "restart", `{"name":"dnsmasq"}`)
		if _, err := cmd.CombinedOutput(); err == nil {
			log.Printf("[DNSMASQ] 已通过 ubus 成功重启 dnsmasq 服务")
			return
		}
	}

	log.Printf("[DNSMASQ] 未检测到 ubus 或 %s，已仅生成/移除内存配置文件 (开发/测试环境)", initScript)
}
