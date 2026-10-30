package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"fluxbox/internal/store"
)

func TestExtractClientIP(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"192.168.1.50:12345", "192.168.1.50"},
		{"10.0.0.1:80", "10.0.0.1"},
		{"[2001:db8::1]:8080", "2001:db8::1"},
		{"[2001:db8::1]", "2001:db8::1"},
		{"192.168.1.50", "192.168.1.50"},
		{"  192.168.1.50:54321  ", "192.168.1.50"},
		{"", "unknown"},
		{"   ", "unknown"},
	}

	for _, tc := range tests {
		got := extractClientIP(tc.input)
		if got != tc.expected {
			t.Errorf("extractClientIP(%q) = %q; 期望 %q", tc.input, got, tc.expected)
		}
	}
}

func TestAuthLoginRateLimitingWithPortRandomization(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "auth-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := store.NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	if err := st.SetPassword("correct-password"); err != nil {
		t.Fatalf("设置密码失败: %v", err)
	}

	server := NewServer(st, nil, nil)

	// 连续 5 次使用不同端口发送错误密码
	for port := 10001; port <= 10005; port++ {
		body, _ := json.Marshal(map[string]string{"password": "wrong-password"})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		if port == 10001 {
			req.RemoteAddr = "192.168.1.88:49152"
		} else if port == 10002 {
			req.RemoteAddr = "192.168.1.88:49153"
		} else if port == 10003 {
			req.RemoteAddr = "192.168.1.88:49154"
		} else if port == 10004 {
			req.RemoteAddr = "192.168.1.88:49155"
		} else if port == 10005 {
			req.RemoteAddr = "192.168.1.88:49156"
		}

		w := httptest.NewRecorder()
		server.handleAuthLogin(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次尝试期望 401 Unauthorized，实际返回 %d", port-10000, w.Code)
		}
	}

	// 第 6 次尝试（端口再度变换），应当被限流拦截 (429 Too Many Requests)
	{
		body, _ := json.Marshal(map[string]string{"password": "wrong-password"})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		req.RemoteAddr = "192.168.1.88:49157"
		w := httptest.NewRecorder()
		server.handleAuthLogin(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("第 6 次尝试端口随机变化后，期望被限流拦截 (429 Too Many Requests)，实际返回 %d (body: %s)", w.Code, w.Body.String())
		}
	}

	// 验证不同 IP 的客户端不受影响
	{
		body, _ := json.Marshal(map[string]string{"password": "correct-password"})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		req.RemoteAddr = "192.168.1.99:50000"
		w := httptest.NewRecorder()
		server.handleAuthLogin(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("其他 IP 期望 200 OK，实际返回 %d", w.Code)
		}
	}
}

func TestAuthLoginConcurrentRateLimiting(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "auth-concurrent-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := store.NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	if err := st.SetPassword("correct-password"); err != nil {
		t.Fatalf("设置密码失败: %v", err)
	}

	server := NewServer(st, nil, nil)

	// 并发发送 20 次错误登录请求，验证线程安全性
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]string{"password": "wrong-password"})
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
			req.RemoteAddr = "10.0.0.5:12345"
			w := httptest.NewRecorder()
			server.handleAuthLogin(w, req)
			// 响应状态要么是 401，要么是 429
			if w.Code != http.StatusUnauthorized && w.Code != http.StatusTooManyRequests {
				t.Errorf("并发测试请求返回非预期状态码: %d", w.Code)
			}
		}(i)
	}
	wg.Wait()

	// 并发完成后，第 21 次请求必定被限流拦截
	body, _ := json.Marshal(map[string]string{"password": "wrong-password"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.RemoteAddr = "10.0.0.5:54321"
	w := httptest.NewRecorder()
	server.handleAuthLogin(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("并发累计超限后，期望 429 Too Many Requests，实际返回 %d", w.Code)
	}
}

func TestAuthLoginRateLimitingDecay(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "auth-decay-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := store.NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	if err := st.SetPassword("correct-password"); err != nil {
		t.Fatalf("设置密码失败: %v", err)
	}

	server := NewServer(st, nil, nil)

	// 注入一个 16 分钟前的失败尝试
	oldTime := time.Now().Add(-16 * time.Minute)
	server.loginFails.Store("172.16.0.1", &failAttempt{
		startedAt: oldTime,
		count:     5,
	})

	// 此时因超过 15 分钟，第一波请求应重置计数并只返回 401 而非 429
	body, _ := json.Marshal(map[string]string{"password": "wrong-password"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.RemoteAddr = "172.16.0.1:23456"
	w := httptest.NewRecorder()
	server.handleAuthLogin(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("15 分钟衰减后，第 1 次错误应返回 401 Unauthorized，实际返回 %d", w.Code)
	}
}
