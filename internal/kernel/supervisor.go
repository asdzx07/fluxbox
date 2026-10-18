package kernel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

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
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("config check failed: %v, output: %s", err, string(out))
	}
	return nil
}

// Start 启动 sing-box 内核进程并实时捕获其输出日志
func (s *Supervisor) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		// 检查进程是否仍然存活
		if err := s.cmd.Process.Signal(os.Signal(nil)); err == nil {
			return nil // 已经在运行中
		}
	}

	if _, err := os.Stat(s.configPath); err != nil {
		return fmt.Errorf("config file not found: %s", s.configPath)
	}

	cmd := exec.Command(s.binPath, "run", "-c", s.configPath)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // 合并 stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start sing-box: %w", err)
	}

	s.cmd = cmd
	s.startedAt = time.Now()

	// 异步读取日志存入环形缓冲区
	go s.captureLogs(stdoutPipe)

	log.Printf("[KERNEL] sing-box 已启动 (PID: %d, 配置: %s)", cmd.Process.Pid, s.configPath)
	return nil
}

// Stop 停止 sing-box 进程
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}

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
	log.Printf("[KERNEL] sing-box 已安全停止")
	return nil
}

// Restart 优雅重启内核
func (s *Supervisor) Restart() error {
	_ = s.Stop()
	time.Sleep(200 * time.Millisecond)
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

	ver := s.Version()
	return Status{
		Running: running,
		Pid:     pid,
		Uptime:  uptime,
		Version: ver,
	}
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
