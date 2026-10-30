package dnsmasq

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDnsmasqDropInLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	confDir := filepath.Join(tempDir, "dnsmasq.d")
	confFile := filepath.Join(confDir, "fluxbox.conf")

	SetCustomPaths(confDir, confFile)
	defer ResetDefaultPaths()

	// 初始状态验证
	st := Status()
	if st["hijacked"] == true {
		t.Fatalf("初始状态 hijacked 应为 false, 得到: %v", st["hijacked"])
	}

	// 1. 测试异常端口
	if err := Hijack(0); err == nil {
		t.Errorf("Hijack(0) 应返回错误，但实际返回 nil")
	}
	if err := Hijack(-53); err == nil {
		t.Errorf("Hijack(-53) 应返回错误，但实际返回 nil")
	}
	if err := Hijack(70000); err == nil {
		t.Errorf("Hijack(70000) 应返回错误，但实际返回 nil")
	}

	// 2. 正常 Hijack
	port := 36053
	if err := Hijack(port); err != nil {
		t.Fatalf("Hijack(%d) 失败: %v", port, err)
	}

	st = Status()
	if st["hijacked"] != true {
		t.Errorf("Hijack 成功后 Status.hijacked 应为 true, 得到: %v", st["hijacked"])
	}
	if st["port"] != port {
		t.Errorf("Hijack 成功后 Status.port 应为 %d, 得到: %v", port, st["port"])
	}

	// 验证文件存在且内容正确
	data, err := os.ReadFile(confFile)
	if err != nil {
		t.Fatalf("读取生成的 drop-in 配置文件失败: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "no-resolv") {
		t.Errorf("drop-in 配置文件必须包含 no-resolv, 实际内容:\n%s", content)
	}
	expectedServer := "server=127.0.0.1#36053"
	if !strings.Contains(content, expectedServer) {
		t.Errorf("drop-in 配置文件必须包含 %s, 实际内容:\n%s", expectedServer, content)
	}

	// 3. 测试 Restore 还原
	if err := Restore(); err != nil {
		t.Fatalf("Restore 失败: %v", err)
	}

	// 验证配置文件已被安全删除
	if _, err := os.Stat(confFile); !os.IsNotExist(err) {
		t.Errorf("Restore 后 drop-in 配置文件 %s 应被删除", confFile)
	}

	st = Status()
	if st["hijacked"] != false {
		t.Errorf("Restore 成功后 Status.hijacked 应为 false, 得到: %v", st["hijacked"])
	}
	if st["port"] != 0 {
		t.Errorf("Restore 成功后 Status.port 应为 0, 得到: %v", st["port"])
	}

	// 4. 重复 Restore 不应报错（幂等性）
	if err := Restore(); err != nil {
		t.Errorf("重复 Restore 不应报错: %v", err)
	}
}
