package subscription

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 验证 Subscription-Userinfo 头解析的全面性与鲁棒性
func TestParseSubscriptionUserinfo(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected *Userinfo
	}{
		{
			name:  "标准分号分隔",
			input: "upload=1073741824; download=5368709120; total=107374182400; expire=1735689600",
			expected: &Userinfo{
				Upload:   1073741824,
				Download: 5368709120,
				Total:    107374182400,
				Expire:   1735689600,
			},
		},
		{
			name:  "包含多余空格与逗号混合",
			input: "  upload = 2048, download= 4096 ; total = 1048576, expire=0 ",
			expected: &Userinfo{
				Upload:   2048,
				Download: 4096,
				Total:    1048576,
				Expire:   0,
			},
		},
		{
			name:  "大小写混合",
			input: "Upload=100; Download=200; TOTAL=1000",
			expected: &Userinfo{
				Upload:   100,
				Download: 200,
				Total:    1000,
				Expire:   0,
			},
		},
		{
			name:     "空字符串或无效输入",
			input:    "   ",
			expected: nil,
		},
		{
			name:     "无有效字段",
			input:    "foo=bar; baz=qux",
			expected: nil,
		},
		{
			name:  "引号与浮点数值混合",
			input: `upload="1048576"; download='2097152'; total=107374182400.0; expire="1735689600"`,
			expected: &Userinfo{
				Upload:   1048576,
				Download: 2097152,
				Total:    107374182400,
				Expire:   1735689600,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res := ParseSubscriptionUserinfo(tc.input)
			if tc.expected == nil {
				if res != nil {
					t.Fatalf("预期返回 nil，实际返回 %+v", res)
				}
				return
			}
			if res == nil {
				t.Fatalf("预期返回 %+v，实际返回 nil", tc.expected)
			}
			if res.Upload != tc.expected.Upload ||
				res.Download != tc.expected.Download ||
				res.Total != tc.expected.Total ||
				res.Expire != tc.expected.Expire {
				t.Fatalf("解析结果与预期不符: 实际 %+v, 预期 %+v", res, tc.expected)
			}
		})
	}
}

// 验证 FetchAndParseWithInfo 在拉取订阅时正确捕获 Subscription-Userinfo
func TestFetchAndParseWithInfo(t *testing.T) {
	nodeURI := "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pass123")) + "@1.2.3.4:8388#TestNode"
	base64Content := base64.StdEncoding.EncodeToString([]byte(nodeURI))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=1024; download=2048; total=104857600; expire=1735689600")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(base64Content))
	}))
	defer ts.Close()

	nodes, info, err := FetchAndParseWithInfo(ts.URL)
	if err != nil {
		t.Fatalf("FetchAndParseWithInfo 失败: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("预期解析出 1 个节点，实际解析出 %d 个", len(nodes))
	}
	if info == nil {
		t.Fatalf("预期捕获到 Userinfo，实际为 nil")
	}
	if info.Upload != 1024 || info.Download != 2048 || info.Total != 104857600 || info.Expire != 1735689600 {
		t.Fatalf("捕获的 Userinfo 数据不符合预期: %+v", info)
	}
}
