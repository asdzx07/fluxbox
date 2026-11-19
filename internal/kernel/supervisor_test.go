package kernel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisorCircuitBreakerOnFrequentCrashes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "supervisor-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfgPath := filepath.Join(tempDir, "config.json")
	_ = os.WriteFile(cfgPath, []byte("{}"), 0644)

	var fakeBin string
	if runtime.GOOS == "windows" {
		fakeBin = filepath.Join(tempDir, "fake_singbox.bat")
		_ = os.WriteFile(fakeBin, []byte("@echo off\r\nexit /b 1\r\n"), 0755)
	} else {
		fakeBin = filepath.Join(tempDir, "fake_singbox.sh")
		_ = os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 1\n"), 0755)
	}

	sup := NewSupervisor(fakeBin, cfgPath)

	var hookTriggered int32
	sup.SetCircuitBreakerHook(func() {
		atomic.AddInt32(&hookTriggered, 1)
	})

	// 模拟 3 次快速崩溃流程触发熔断
	now := time.Now()
	sup.crashTimes = []time.Time{
		now.Add(-10 * time.Second),
		now.Add(-5 * time.Second),
	}

	// 模拟一个退出进程执行 watchProcess
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit 1")
	} else {
		cmd = exec.Command("sh", "-c", "exit 1")
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动测试子进程失败: %v", err)
	}

	cmdDone := make(chan struct{})
	sup.mu.Lock()
	sup.cmd = cmd
	sup.cmdDone = cmdDone
	sup.startedAt = time.Now()
	sup.mu.Unlock()

	ctx := context.Background()
	sup.watchProcess(cmd, cmdDone, ctx)

	// 检查熔断是否触发
	if !sup.IsCircuitBroken() {
		t.Errorf("预期连续崩溃达到 3 次后进入熔断保护状态，但 IsCircuitBroken 返回 false")
	}

	if atomic.LoadInt32(&hookTriggered) != 1 {
		t.Errorf("预期熔断恢复钩子被调用 1 次，实际调用次数: %d", atomic.LoadInt32(&hookTriggered))
	}

	status := sup.Status()
	if !status.CircuitBroken {
		t.Errorf("sup.Status().CircuitBroken 应当为 true")
	}

	// 验证重置熔断
	sup.ResetCircuitBreaker()
	if sup.IsCircuitBroken() {
		t.Errorf("ResetCircuitBreaker 后 IsCircuitBroken 应当为 false")
	}
}

func TestSupervisorManualStopDoesNotTriggerCircuitBreaker(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "supervisor-test-stop-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfgPath := filepath.Join(tempDir, "config.json")
	_ = os.WriteFile(cfgPath, []byte("{}"), 0644)

	var fakeBin string
	if runtime.GOOS == "windows" {
		fakeBin = filepath.Join(tempDir, "fake_singbox.bat")
		_ = os.WriteFile(fakeBin, []byte("@echo off\r\nping -n 3 127.0.0.1 >nul\r\n"), 0755)
	} else {
		fakeBin = filepath.Join(tempDir, "fake_singbox.sh")
		_ = os.WriteFile(fakeBin, []byte("#!/bin/sh\nsleep 3\n"), 0755)
	}

	sup := NewSupervisor(fakeBin, cfgPath)
	var hookTriggered int32
	sup.SetCircuitBreakerHook(func() {
		atomic.AddInt32(&hookTriggered, 1)
	})

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "ping -n 3 127.0.0.1 >nul")
	} else {
		cmd = exec.Command("sh", "-c", "sleep 3")
	}
	_ = cmd.Start()

	cmdDone := make(chan struct{})
	sup.mu.Lock()
	sup.cmd = cmd
	sup.cmdDone = cmdDone
	sup.startedAt = time.Now()
	sup.mu.Unlock()

	go sup.watchProcess(cmd, cmdDone, context.Background())

	time.Sleep(100 * time.Millisecond)

	// 主动调用 Stop
	_ = sup.Stop()

	// 此时 manualStop 应为 true
	if atomic.LoadInt32(&hookTriggered) != 0 {
		t.Errorf("主动停止不应触发熔断恢复钩子！")
	}
	if sup.IsCircuitBroken() {
		t.Errorf("主动停止不应进入熔断状态！")
	}
}

func TestSupervisorWatchdogBackoffCancellation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "supervisor-test-cancel-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfgPath := filepath.Join(tempDir, "config.json")
	_ = os.WriteFile(cfgPath, []byte("{}"), 0644)

	sup := NewSupervisor("fake-sing-box", cfgPath)

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit 1")
	} else {
		cmd = exec.Command("sh", "-c", "exit 1")
	}
	_ = cmd.Start()

	cmdDone := make(chan struct{})
	sup.mu.Lock()
	sup.cmd = cmd
	sup.cmdDone = cmdDone
	sup.startedAt = time.Now()
	sup.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	// 启动后立即取消 context，watchProcess 在退避等待时应能迅速返回
	done := make(chan struct{})
	go func() {
		sup.watchProcess(cmd, cmdDone, ctx)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// 成功迅速退出
	case <-time.After(2 * time.Second):
		t.Errorf("watchProcess 未能在 context 取消时迅速退出，退避等待未响应取消信号")
	}
}

func TestSupervisorCircuitBreakerOnStartFailures(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "supervisor-test-fail-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 提供一个不存在的配置文件，导致每次启动均必定失败
	cfgPath := filepath.Join(tempDir, "non_existent_config.json")
	sup := NewSupervisor("non-existent-sing-box-bin", cfgPath)

	var hookTriggered int32
	sup.SetCircuitBreakerHook(func() {
		atomic.AddInt32(&hookTriggered, 1)
	})

	// 预设两次失败时间戳，使下一次自愈拉起失败时达到阈值 3
	now := time.Now()
	sup.crashTimes = []time.Time{
		now.Add(-10 * time.Second),
		now.Add(-5 * time.Second),
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit 1")
	} else {
		cmd = exec.Command("sh", "-c", "exit 1")
	}
	_ = cmd.Start()

	cmdDone := make(chan struct{})
	sup.mu.Lock()
	sup.cmd = cmd
	sup.cmdDone = cmdDone
	sup.startedAt = time.Now()
	sup.mu.Unlock()

	sup.watchProcess(cmd, cmdDone, context.Background())

	if !sup.IsCircuitBroken() {
		t.Errorf("内核在自愈拉起失败累积达到阈值时，预期触发熔断保护，但 IsCircuitBroken 返回 false")
	}
	if atomic.LoadInt32(&hookTriggered) != 1 {
		t.Errorf("预期熔断恢复钩子被触发 1 次，实际调用次数: %d", atomic.LoadInt32(&hookTriggered))
	}
}

func TestProberDualFeedbackFitnessCalculation(t *testing.T) {
	cfg := DefaultProberConfig()
	prober := NewProber(cfg)

	// 1. 模拟健康 204 响应
	prober.SetCustomProbeFn(func(ctx context.Context) ProbeResult {
		return ProbeResult{
			EvadeScore: 1.0,
			ValidScore: 1.0,
			Overhead:   0.02,
			Fitness:    0.98,
			StatusCode: 204,
		}
	})
	res := prober.ProbeOnce(context.Background())
	if res.Fitness < 0.95 {
		t.Errorf("正常 204 探针预期高适应度 (>0.95)，实际: %f", res.Fitness)
	}

	// 2. 模拟伪规避 (服务端返回 HTTP 400 畸形报错)
	prober.SetCustomProbeFn(func(ctx context.Context) ProbeResult {
		return ProbeResult{
			EvadeScore: 1.0,
			ValidScore: 0.0,
			Fitness:    0.0,
			StatusCode: 400,
		}
	})
	res400 := prober.ProbeOnce(context.Background())
	if res400.Fitness != 0.0 {
		t.Errorf("伪规避 (400) 预期适应度为 0.0，实际: %f", res400.Fitness)
	}
}

func TestTwoStageCircuitBreakerTransitions(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "two-stage-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfgPath := filepath.Join(tempDir, "config.json")
	_ = os.WriteFile(cfgPath, []byte("{}"), 0644)

	sup := NewSupervisor("echo", cfgPath)

	var degradedTriggered int32
	var fallbackTriggered int32

	sup.SetDegradedHook(func() {
		atomic.AddInt32(&degradedTriggered, 1)
	})
	sup.SetCircuitBreakerHook(func() {
		atomic.AddInt32(&fallbackTriggered, 1)
	})

	// 配置一个探针，FailThreshold = 3, ResumeThreshold = 2
	pCfg := DefaultProberConfig()
	pCfg.FailThreshold = 3
	pCfg.ResumeThreshold = 2
	pCfg.Interval = 10 * time.Millisecond
	testProber := NewProber(pCfg)

	failCount := 0
	testProber.SetCustomProbeFn(func(ctx context.Context) ProbeResult {
		failCount++
		if failCount <= 3 {
			// 前 3 次模拟被静默阻断 (Fitness = 0)
			return ProbeResult{EvadeScore: 0, ValidScore: 0, Fitness: 0}
		}
		// 后续模拟恢复健康 (Fitness = 1.0)
		return ProbeResult{EvadeScore: 1.0, ValidScore: 1.0, Fitness: 1.0, StatusCode: 204}
	})

	sup.SetProber(testProber)

	// 模拟连续 3 次探针失败
	for i := 0; i < 3; i++ {
		res := testProber.ProbeOnce(context.Background())
		sup.mu.Lock()
		sup.fitnessScore = res.Fitness
		_, fails, _ := testProber.GetLastResult()
		if sup.circuitLevel == CircuitLevelNormal && fails >= testProber.cfg.FailThreshold {
			sup.circuitLevel = CircuitLevelDegraded
			sup.circuitBroken = true
			if sup.onDegraded != nil {
				sup.onDegraded()
			}
		}
		sup.mu.Unlock()
	}

	// 验证进入一级降级熔断
	if sup.GetCircuitLevel() != CircuitLevelDegraded {
		t.Errorf("预期进入 CircuitLevelDegraded (一级熔断)，实际级别: %d", sup.GetCircuitLevel())
	}
	if atomic.LoadInt32(&degradedTriggered) != 1 {
		t.Errorf("预期一级降级钩子触发 1 次，实际: %d", atomic.LoadInt32(&degradedTriggered))
	}

	// 模拟连续 2 次探针成功恢复
	for i := 0; i < 2; i++ {
		res := testProber.ProbeOnce(context.Background())
		sup.mu.Lock()
		sup.fitnessScore = res.Fitness
		_, _, succ := testProber.GetLastResult()
		if sup.circuitLevel == CircuitLevelDegraded && succ >= testProber.cfg.ResumeThreshold {
			sup.circuitLevel = CircuitLevelNormal
			sup.circuitBroken = false
		}
		sup.mu.Unlock()
	}

	// 验证成功自愈回 Normal
	if sup.GetCircuitLevel() != CircuitLevelNormal {
		t.Errorf("预期自愈回 CircuitLevelNormal，实际级别: %d", sup.GetCircuitLevel())
	}
	if sup.IsCircuitBroken() {
		t.Errorf("自愈后 IsCircuitBroken 应当为 false")
	}
}
