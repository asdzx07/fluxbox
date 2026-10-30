package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"fluxbox/internal/store"
	"fluxbox/internal/subscription"
)

// 验证 /api/subscriptions 接口暴露 Userinfo 配额看板信息及刷新更新能力
func TestSubscriptionsAPI_Userinfo(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sub-api-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := store.NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}

	nodeURI := "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pass123")) + "@1.2.3.4:8388#NodeA"
	b64Data := base64.StdEncoding.EncodeToString([]byte(nodeURI))

	// 模拟远程机场订阅服务器
	subServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=1073741824; download=5368709120; total=107374182400; expire=1735689600")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(b64Data))
	}))
	defer subServer.Close()

	server := NewServer(st, nil, nil)

	// 1. POST 添加订阅
	addBody := map[string]string{
		"name": "测试机场",
		"url":  subServer.URL,
	}
	bodyBytes, _ := json.Marshal(addBody)
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.handleSubscriptions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("添加订阅失败: status=%d, body=%s", w.Code, w.Body.String())
	}

	var addResp struct {
		ID       string                 `json:"id"`
		Ok       bool                   `json:"ok"`
		Userinfo *subscription.Userinfo `json:"userinfo"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("反序列化添加结果失败: %v", err)
	}
	if addResp.Userinfo == nil || addResp.Userinfo.Total != 107374182400 {
		t.Fatalf("POST 返回的 Userinfo 配额不符: %+v", addResp.Userinfo)
	}

	// 2. GET 订阅列表应包含 userinfo
	getReq := httptest.NewRequest(http.MethodGet, "/api/subscriptions", nil)
	getRec := httptest.NewRecorder()
	server.handleSubscriptions(getRec, getReq)

	var getResp struct {
		Subscriptions []struct {
			ID       string                 `json:"id"`
			Userinfo *subscription.Userinfo `json:"userinfo"`
		} `json:"subscriptions"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("反序列化 GET 结果失败: %v", err)
	}
	if len(getResp.Subscriptions) != 1 {
		t.Fatalf("预期 1 个订阅，实际 %d", len(getResp.Subscriptions))
	}
	if getResp.Subscriptions[0].Userinfo == nil || getResp.Subscriptions[0].Userinfo.Download != 5368709120 {
		t.Fatalf("GET 返回的订阅缺少有效 Userinfo: %+v", getResp.Subscriptions[0].Userinfo)
	}

	// 3. 验证定时同步 / 批量同步方法 SyncSubscriptions
	updated, syncErr := server.SyncSubscriptions()
	if syncErr != nil {
		t.Fatalf("SyncSubscriptions 执行报错: %v", syncErr)
	}
	if updated != 1 {
		t.Fatalf("预期同步 1 个订阅，实际同步 %d 个", updated)
	}

	// 4. 验证 POST /api/subscriptions/sync
	syncReq := httptest.NewRequest(http.MethodPost, "/api/subscriptions/sync", nil)
	syncRec := httptest.NewRecorder()
	server.handleSubscriptionsSync(syncRec, syncReq)

	if syncRec.Code != http.StatusOK {
		t.Fatalf("/api/subscriptions/sync 状态码错误: %d", syncRec.Code)
	}
	var syncEndpointResp struct {
		Ok      bool `json:"ok"`
		Updated int  `json:"updated"`
	}
	if err := json.Unmarshal(syncRec.Body.Bytes(), &syncEndpointResp); err != nil || !syncEndpointResp.Ok || syncEndpointResp.Updated != 1 {
		t.Fatalf("批量同步端点响应不符: %+v", syncEndpointResp)
	}
}

// 验证 StartSubscriptionAutoSync 与 StopSubscriptionAutoSync 协程生命周期
func TestSubscriptionAutoSyncLifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sub-lifecycle-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := store.NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}

	server := NewServer(st, nil, nil)
	defer server.Close()
	server.StartSubscriptionAutoSync(50 * time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	server.StopSubscriptionAutoSync()
}

// 验证 handleSubscriptionAction 严格检验请求方法，防止非法请求误触发
func TestSubscriptionAction_MethodCheck(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sub-action-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := store.NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}

	server := NewServer(st, nil, nil)
	defer server.Close()

	// 1. 测试 GET /api/subscriptions/sub-123 应返回 405
	getReq := httptest.NewRequest(http.MethodGet, "/api/subscriptions/sub-123", nil)
	getRec := httptest.NewRecorder()
	server.handleSubscriptionAction(getRec, getReq)

	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/subscriptions/:id 预期返回 405，实际返回 %d", getRec.Code)
	}

	// 2. 测试 GET /api/subscriptions/sub-123/refresh 应返回 405 (仅支持 POST)
	getRefreshReq := httptest.NewRequest(http.MethodGet, "/api/subscriptions/sub-123/refresh", nil)
	getRefreshRec := httptest.NewRecorder()
	server.handleSubscriptionAction(getRefreshRec, getRefreshReq)

	if getRefreshRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/subscriptions/:id/refresh 预期返回 405，实际返回 %d", getRefreshRec.Code)
	}
}
