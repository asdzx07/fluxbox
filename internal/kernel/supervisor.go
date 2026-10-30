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

	"fluxbox/internal/dnsmasq"
	"fluxbox/internal/netstack"
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

const (
	// CircuitBreakerWindow 熔断统计滑动窗口：1 分钟
	CircuitBreakerWindow = 1 * time.Minute
	// CircuitBreakerThreshold 连续崩溃触发熔断阈值：3 次
	CircuitBreakerThreshold = 3
	// InitialBackoff 初始自愈重试延迟：1 秒
	InitialBackoff = 1 * time.Second
	// MaxBackoff 最大退避延迟：30 秒
	MaxBackoff = 30 * time.Second
	// StableRunDuration 稳定运行判定阈值：若单次稳定运行超过 1 分钟，复位退避计数
	StableRunDuration = 1 * time.Minute
)

type Status struct {
	Running       bool   `json:"running"`
	Pid           int    `json:"pid"`
	Uptime        int64  `json:"uptime"`
	Version       string `json:"version"`
	ConfigErr     string `json:"configErr,omitempty"`
	CircuitBroken bool   `json:"circuitBroken"`
}

type Supervisor struct {
	mu         sync.Mutex
	binPath    string
	configPath string
	cmd        *exec.Cmd
	cmdDone    chan struct{}      // 用于通知子进程 cmd.Wait() 已完成退出，避免并发调用 Wait
	startedAt  time.Time
	logRing    []string
	maxLogs    int

	manualStop       bool               // 标记是否为主动停止 (避免主动退出时触发 Watchdog)
	circuitBroken    bool               // 标记是否已触发防断网熔断
	crashTimes       []time.Time        // 记录滑动窗口内的崩溃时间戳
	retryCount       int                // 指数退避重试计数
	cancelWatch      context.CancelFunc // 取消当前退避等待上下文
	onCircuitBreaker func()             // 熔断恢复执行函数 (支持测试注入)
}

func NewSupervisor(binPath, configPath string) *Supervisor {
	EnsureMuslCompat()
	if binPath == "" {
		binPath = findSingBoxBinary()
	}
	s := &Supervisor{
		binPath:    binPath,
		configPath: configPath,
		logRing:    make([]string, 0, 500),
		maxLogs:    500,
		crashTimes: make([]time.Time, 0, 10),
	}
	// 默认防断网熔断动作：还原 dnsmasq 与网络栈，确保全家网络立即直连不断网
	s.onCircuitBreaker = func() {
		log.Printf("🛡️ [防断网熔断] 正在将局域网网络无缝还原为纯原生直连模式...")
		_ = dnsmasq.Restore()
		_ = netstack.Teardown()
		log.Printf("🛡️ [防断网熔断] 网络还原完成，全家断网风险已彻底排除！")
	}
	return s
}

// SetCircuitBreakerHook 允许注入自定义熔断恢复逻辑 (便于单元测试)
func (s *Supervisor) SetCircuitBreakerHook(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCircuitBreaker = fn
}

// ResetCircuitBreaker 手动重置熔断状态与崩溃历史
func (s *Supervisor) ResetCircuitBreaker() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.circuitBroken = false
	s.crashTimes = nil
	s.retryCount = 0
}

// IsCircuitBroken 查询当前是否处于熔断状态
func (s *Supervisor) IsCircuitBroken() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.circuitBroken
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

// Start 启动 sing-box 内核进程并实时捕获其输出日志 (外部/用户主动调用)
func (s *Supervisor) Start() error {
	return s.startInternal(false)
}

func (s *Supervisor) startInternal(isAutoRecovery bool) error {
	EnsureMuslCompat()
	s.mu.Lock()
	defer s.mu.Unlock()

	// 主动触发启动时重置主动停止标记与熔断状态，并清空历史崩溃记录与退避计数
	s.manualStop = false
	s.circuitBroken = false
	if !isAutoRecovery {
		s.crashTimes = nil
		s.retryCount = 0
	}

	// 取消之前可能正在进行的退避重试等待
	if s.cancelWatch != nil {
		s.cancelWatch()
		s.cancelWatch = nil
	}

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

	cmdDone := make(chan struct{})
	s.cmd = cmd
	s.cmdDone = cmdDone
	s.startedAt = time.Now()

	watchCtx, cancel := context.WithCancel(context.Background())
	s.cancelWatch = cancel

	// 异步读取日志存入环形缓冲区 (并发读取 stdout 与 stderr)
	go s.captureLogs(stdoutPipe)
	go s.captureLogs(stderrPipe)

	// 启动进程守护生命周期 (Watchdog 自愈与防断网熔断)
	go s.watchProcess(cmd, cmdDone, watchCtx)

	// 启动稳定运行监控器：稳定运行 1 分钟后自动复位退避计数与崩溃记录
	go s.monitorStability(cmd, s.startedAt, watchCtx)

	log.Printf("[KERNEL] sing-box 已启动 (PID: %d, 配置: %s)", cmd.Process.Pid, s.configPath)
	return nil
}

// watchProcess 监控子进程生命周期，处理意外崩溃自愈与防断网熔断保护
func (s *Supervisor) watchProcess(cmd *exec.Cmd, cmdDone chan struct{}, ctx context.Context) {
	waitErr := cmd.Wait()
	close(cmdDone) // 明确通知 Stop() 等等待者：子进程已彻底退出并完成资源回收

	s.mu.Lock()
	// 如果已被新启动的实例替换，直接退出
	if s.cmd != cmd {
		s.mu.Unlock()
		return
	}
	s.cmd = nil
	s.cmdDone = nil
	s.startedAt = time.Time{}

	// 若为用户主动停止或重启，不触发 Watchdog 自愈
	if s.manualStop {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	// 进入 Watchdog 自愈与熔断主循环
	s.handleCrashOrFailure(waitErr, ctx)
}

// handleCrashOrFailure 处理崩溃或拉起失败：指数退避重试并在连续达到阈值时触发防断网熔断
func (s *Supervisor) handleCrashOrFailure(lastErr error, ctx context.Context) {
	for {
		s.mu.Lock()
		if s.manualStop || s.circuitBroken {
			s.mu.Unlock()
			return
		}

		now := time.Now()
		// 维护滑动窗口内的崩溃/失败历史
		var recent []time.Time
		for _, t := range s.crashTimes {
			if now.Sub(t) <= CircuitBreakerWindow {
				recent = append(recent, t)
			}
		}
		recent = append(recent, now)
		s.crashTimes = recent
		s.retryCount++

		// 检查是否在滑动窗口内连续崩溃/失败达到熔断阈值
		if len(s.crashTimes) >= CircuitBreakerThreshold {
			s.circuitBroken = true
			s.manualStop = true // 锁定停止自愈
			hook := s.onCircuitBreaker
			s.mu.Unlock()

			log.Printf("🚨 [熔断保护] sing-box 内核在 %v 内连续崩溃/拉起失败 %d 次 (>= %d)，已触发防断网熔断保护！立即停止自动重启并恢复网络...",
				CircuitBreakerWindow, len(recent), CircuitBreakerThreshold)

			// 触发防断网熔断：还原网络栈与 dnsmasq，确保原生直连不断网
			if hook != nil {
				hook()
			}
			return
		}

		// 计算指数退避延迟: 1s, 2s, 4s, 8s... 上限 30s
		backoff := InitialBackoff
		for i := 1; i < s.retryCount; i++ {
			backoff *= 2
			if backoff > MaxBackoff {
				backoff = MaxBackoff
				break
			}
		}

		log.Printf("⚠️ [WATCHDOG] sing-box 内核异常 (%v)，已启动自愈重试守护。将在 %v 后尝试自愈拉起 (第 %d 次自愈重试，窗口崩溃 %d/%d)...",
			lastErr, backoff, s.retryCount, len(recent), CircuitBreakerThreshold)
		s.mu.Unlock()

		// 等待退避时间或主动取消
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			log.Printf("[WATCHDOG] 收到主动停止或取消信号，终止自愈守护")
			return
		}

		s.mu.Lock()
		if s.manualStop || s.circuitBroken {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()

		log.Printf("🔄 [WATCHDOG] 正在执行 sing-box 自愈拉起...")
		if err := s.startInternal(true); err != nil {
			log.Printf("❌ [WATCHDOG] sing-box 自愈拉起失败: %v", err)
			lastErr = err
			// 拉起失败同样计入崩溃历史，在下一轮循环中继续退避重试或触发防断网熔断
			continue
		}

		log.Printf("✅ [WATCHDOG] sing-box 自愈拉起成功！正在恢复网络栈...")
		_ = netstack.Setup()
		return
	}
}

// monitorStability 稳定运行监控器
func (s *Supervisor) monitorStability(cmd *exec.Cmd, startedAt time.Time, ctx context.Context) {
	select {
	case <-time.After(StableRunDuration):
		s.mu.Lock()
		if s.cmd == cmd && s.startedAt.Equal(startedAt) {
			if len(s.crashTimes) > 0 || s.retryCount > 0 {
				log.Printf("✨ [WATCHDOG] sing-box 已稳定运行超过 %v，重置崩溃计数器与退避延迟", StableRunDuration)
				s.crashTimes = nil
				s.retryCount = 0
			}
		}
		s.mu.Unlock()
	case <-ctx.Done():
		return
	}
}

// Stop 停止 sing-box 进程
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	// 标记为主动作出停止，防止触发 Watchdog 重启
	s.manualStop = true

	// 取消正在进行的退避重试
	if s.cancelWatch != nil {
		s.cancelWatch()
		s.cancelWatch = nil
	}

	cmd := s.cmd
	cmdDone := s.cmdDone
	s.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)

		if cmdDone != nil {
			select {
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-cmdDone
			case <-cmdDone:
			}
		}
	}

	s.mu.Lock()
	if s.cmd == cmd {
		s.cmd = nil
		s.cmdDone = nil
		s.startedAt = time.Time{}
	}
	s.mu.Unlock()

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
		Running:       running,
		Pid:           pid,
		Uptime:        uptime,
		Version:       ver,
		CircuitBroken: s.circuitBroken,
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

