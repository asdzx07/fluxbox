package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"fluxbox/internal/store"
)

func TestHandleSettingsSaveMissingFields(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "settings-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := store.NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}

	server := NewServer(st, nil, nil)

	payload := map[string]any{
		"dns": map[string]any{
			"mode":          "dnsmasq",
			"directAddress": "223.5.5.5",
			"proxy":         "8.8.8.8",
			"adblock":       true,
			"adblockAllow":  []any{"allow.example.com", "whitelist.org"},
			"adblockCustom": []any{"ad.tracker.com", "bad.ads.net"},
			"hijackPort":    5335,
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	server.handleSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings 期望返回 200, 实际返回 %d: %s", rec.Code, rec.Body.String())
	}

	// 1. 验证 store 中保存的数据
	cur := st.GetSettings()
	expectedAllow := []string{"allow.example.com", "whitelist.org"}
	if !reflect.DeepEqual(cur.DNS.AdblockAllow, expectedAllow) {
		t.Errorf("store 中的 DNS.AdblockAllow 期望 %v, 实际 %v", expectedAllow, cur.DNS.AdblockAllow)
	}
	expectedCustom := []string{"ad.tracker.com", "bad.ads.net"}
	if !reflect.DeepEqual(cur.DNS.AdblockCustom, expectedCustom) {
		t.Errorf("store 中的 DNS.AdblockCustom 期望 %v, 实际 %v", expectedCustom, cur.DNS.AdblockCustom)
	}
	if cur.DNS.HijackPort != 5335 {
		t.Errorf("store 中的 DNS.HijackPort 期望 5335, 实际 %d", cur.DNS.HijackPort)
	}

	// 2. 验证 GET /api/settings 返回的响应
	getReq := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	getRec := httptest.NewRecorder()
	server.handleSettings(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings 期望返回 200, 实际返回 %d", getRec.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析 GET 响应失败: %v", err)
	}
	dnsResp, ok := resp["dns"].(map[string]any)
	if !ok {
		t.Fatalf("GET 响应中缺少 dns 对象")
	}
	if int(dnsResp["hijackPort"].(float64)) != 5335 {
		t.Errorf("GET 响应中 hijackPort 期望 5335, 实际 %v", dnsResp["hijackPort"])
	}

	// 3. 验证传入 null 清空列表以及非法端口拦截
	clearPayload := map[string]any{
		"dns": map[string]any{
			"adblockAllow": nil,
			"hijackPort":   70000, // 超过 65535，应当被忽略，保留原端口 5335
		},
	}
	clearBody, _ := json.Marshal(clearPayload)
	clearReq := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(clearBody))
	clearRec := httptest.NewRecorder()
	server.handleSettings(clearRec, clearReq)

	if clearRec.Code != http.StatusOK {
		t.Fatalf("清空 PUT /api/settings 期望 200, 实际 %d", clearRec.Code)
	}
	afterClear := st.GetSettings()
	if len(afterClear.DNS.AdblockAllow) != 0 {
		t.Errorf("adblockAllow 传入 null 期望被清空，实际为 %v", afterClear.DNS.AdblockAllow)
	}
	if afterClear.DNS.HijackPort != 5335 {
		t.Errorf("非法端口 70000 应被忽略保留 5335，实际为 %d", afterClear.DNS.HijackPort)
	}
}
