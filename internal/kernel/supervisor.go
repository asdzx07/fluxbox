package kernel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EnsureMuslCompat 检查是否处于 musl libc 环境（如 OpenWrt/iStoreOS）并缺少标准 glibc 解释器软链接。
// 官方发布的 sing-box 静态构建虽然 CGO: disabled，但其 ELF interpreter 默认声明为 /lib64/ld-linux-x86-64.so.2。
// 当检测到系统存在 /lib/ld-musl-x86_64.so.1 但缺少 /lib64/ld-linux-x86-64.so.2 时，自动创建软链接，
// 从而使官方内核开箱即用，避免报 'not found' 导致无法启动。
func EnsureMuslCompat() {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return
	}
	muslLd := "/lib/ld-musl-x86_64.so.1"
	glibcLd := "/lib64/ld-linux-x86-64.so.2"
	if _, err := os.Stat(muslLd); err == nil {
		if _, err := os.Stat(glibcLd); os.IsNotExist(err) {
			_ = os.MkdirAll("/lib64", 0755)
			if err := os.Symlink(muslLd, glibcLd); err == nil {
				log.Printf("🔗 已自动建立 musl -> glibc 动态解释器软链接: %s -> %s", glibcLd, muslLd)
			}
		}
	}
}

type Status struct {
	Running   bool   `json:"running"`
	Pid       int    `json:"pid"`
	Uptime    int64  `json:"uptime"`
	Version   string `json:"version"`
	ConfigErr string `json:"configErr,omitempty"`
}

type Supervisor struct {
	mu         sync.Mutex
	binPath    string
	configPath string
	cmd        *exec.Cmd
	startedAt  time.Time
	logRing    []string
	maxLogs    int
}

func NewSupervisor(binPath, configPath string) *Supervisor {
	EnsureMuslCompat()
	if binPath == "" {
		binPath = findSingBoxBinary()
	}
	return &Supervisor{
		binPath:    binPath,
		configPath: configPath,
		logRing:    make([]string, 0, 500),
		maxLogs:    500,
	}
}

func findSingBoxBinary() string {
	candidates := []string{
		os.Getenv("SINGBOX_PATH"),
		"/usr/bin/sing-box",
		"/opt/fluxbox/bin/sing-box",
		"/usr/local/bin/sing-box",
		"sing-box",
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "sing-box"
}

// CheckConfig 运行 sing-box check -c <file> 验证语法合法性
func (s *Supervisor) CheckConfig(cfgPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.binPath, "check", "-c", cfgPath)
	cmd.Env = append(os.Environ(),
		"ENABLE_DEPRECATED_MISSING_DOMAIN_RESOLVER=true",
		"ENABLE_DEPRECATED_LEGACY_RULE_SET_DOWNLOAD_DETOUR=true",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("config check failed: %v, output: %s", err, string(out))
	}
	return nil
}

// Start 启动 sing-box 内核进程并实时捕获其输出日志
func (s *Supervisor) Start() error {
	EnsureMuslCompat()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		// 检查进程是否仍然存活
		if err := s.cmd.Process.Signal(os.Signal(nil)); err == nil {
			return nil // 已经在运行中
		}
	}

	// 启动前先确保没有外部僵尸进程占用 7891、7893、9095 等核心端口
	if extPid := findExternalSingBoxPid(); extPid > 0 {
		_ = exec.Command("kill", "-9", fmt.Sprintf("%d", extPid)).Run()
		time.Sleep(200 * time.Millisecond)
	}

	if _, err := os.Stat(s.configPath); err != nil {
		return fmt.Errorf("config file not found: %s", s.configPath)
	}

	cmd := exec.Command(s.binPath, "run", "-c", s.configPath)
	cmd.Env = append(os.Environ(),
		"ENABLE_DEPRECATED_MISSING_DOMAIN_RESOLVER=true",
		"ENABLE_DEPRECATED_LEGACY_RULE_SET_DOWNLOAD_DETOUR=true",
		// 【核心内存优化】强制将 sing-box 运行时堆内存软上限锁死在 55MB，提高 GC 回收频率
		"GOMEMLIMIT=55MiB",
		"GOGC=40",
	)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start sing-box: %w", err)
	}

	s.cmd = cmd
	s.startedAt = time.Now()

	// 异步读取日志存入环形缓冲区 (并发读取 stdout 与 stderr)
	go s.captureLogs(stdoutPipe)
	go s.captureLogs(stderrPipe)

	// 监听进程退出，回收子进程，避免产生僵尸进程
	go func() {
		_ = cmd.Wait()
		s.mu.Lock()
		if s.cmd == cmd {
			s.cmd = nil
			s.startedAt = time.Time{}
		}
		s.mu.Unlock()
	}()

	log.Printf("[KERNEL] sing-box 已启动 (PID: %d, 配置: %s)", cmd.Process.Pid, s.configPath)
	return nil
}

// Stop 停止 sing-box 进程
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(os.Interrupt)

		done := make(chan error, 1)
		go func() {
			done <- s.cmd.Wait()
		}()

		select {
		case <-time.After(3 * time.Second):
			_ = s.cmd.Process.Kill()
			<-done
		case <-done:
		}

		s.cmd = nil
		s.startedAt = time.Time{}
	}

	// 彻底清理系统中外部或旧版服务可能残留的 sing-box 进程
	_ = exec.Command("killall", "-9", "sing-box").Run()
	_ = exec.Command("/etc/init.d/mybox-kernel", "stop").Run()

	log.Printf("[KERNEL] sing-box 已安全停止")
	return nil
}

// Restart 优雅重启内核
func (s *Supervisor) Restart() error {
	_ = s.Stop()
	time.Sleep(300 * time.Millisecond)
	return s.Start()
}

func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()

	running := false
	pid := 0
	var uptime int64 = 0

	if s.cmd != nil && s.cmd.Process != nil {
		if err := s.cmd.Process.Signal(os.Signal(nil)); err == nil {
			running = true
			pid = s.cmd.Process.Pid
			uptime = int64(time.Since(s.startedAt).Seconds())
		}
	}

	// 智能探测兜底：即使内核由系统 procd 或外部启动，只要 9095 可通信或进程存活即算运行中
	if !running {
		if extPid := findExternalSingBoxPid(); extPid > 0 {
			running = true
			pid = extPid
		} else if isPortListening("127.0.0.1:9095") {
			running = true
			pid = findExternalSingBoxPid()
		}
	}

	// 若进程运行中但 uptime 尚未设定，从系统 /proc/<pid> 获取精确存活秒数
	if running && pid > 0 && uptime <= 0 {
		uptime = getProcessUptime(pid)
	}
	if running && uptime <= 0 {
		uptime = 1
	}

	ver := s.Version()
	return Status{
		Running: running,
		Pid:     pid,
		Uptime:  uptime,
		Version: ver,
	}
}

func getProcessUptime(pid int) int64 {
	procPath := fmt.Sprintf("/proc/%d", pid)
	if fi, err := os.Stat(procPath); err == nil {
		sec := int64(time.Since(fi.ModTime()).Seconds())
		if sec > 0 {
			return sec
		}
	}
	return 0
}

func findExternalSingBoxPid() int {
	out, err := exec.Command("pidof", "sing-box").Output()
	if err == nil {
		fields := strings.Fields(string(out))
		if len(fields) > 0 {
			if p, err := strconv.Atoi(fields[0]); err == nil && p > 0 {
				return p
			}
		}
	}
	return 0
}

func isPortListening(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return true
	}
	return false
}

func (s *Supervisor) Version() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.binPath, "version")
	out, err := cmd.Output()
	if err != nil {
		return "sing-box (unknown)"
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return "sing-box"
}

func (s *Supervisor) GetLogs(lines int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if lines <= 0 || lines > len(s.logRing) {
		lines = len(s.logRing)
	}
	start := len(s.logRing) - lines
	return strings.Join(s.logRing[start:], "\n")
}

func (s *Supervisor) captureLogs(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		log.Printf("[SING-BOX] %s", line)
		s.mu.Lock()
		if len(s.logRing) >= s.maxLogs {
			s.logRing = s.logRing[1:]
		}
		s.logRing = append(s.logRing, line)
		s.mu.Unlock()
	}
}

func (s *Supervisor) GetConfigPath() string {
	return s.configPath
}

func (s *Supervisor) SetConfigPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configPath = path
}

func (s *Supervisor) GetBinPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.binPath
}

