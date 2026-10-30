package store

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"fluxbox/internal/configgen"
	"fluxbox/internal/subscription"
)

// 验证 GetSettings() 返回的是完全独立的深拷贝副本，外部修改绝不污染内部数据
func TestGetSettingsDeepCopy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "store-deepcopy-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}

	// 注入包含各类引用类型的测试数据
	now := time.Now().Unix()
	_ = st.MutateSettings(func(s *FullSettings) {
		s.DNS.AdblockAllow = []string{"allow.com"}
		s.DNS.AdblockCustom = []string{"block.com"}
		s.Nodes = []map[string]any{
			{
				"tag":    "原始节点1",
				"server": "1.1.1.1",
				"tls": map[string]any{
					"server_name": "original.sni",
				},
				"transport": map[string]any{
					"headers": map[string]string{
						"Host": "original.host",
					},
				},
			},
		}
		s.Subscriptions = []subscription.Subscription{
			{
				ID:       "sub-1",
				NodeTags: []string{"Tag1", "Tag2"},
			},
		}
		s.Groups = []configgen.GroupConfig{
			{
				ID:       "g-1",
				Keywords: []string{"HK", "TW"},
			},
		}
		s.Policies = []configgen.PolicyConfig{
			{
				ID:           "pol-1",
				DomainSuffix: []string{"suffix.com"},
			},
		}
		s.Meta.LastDeployAt = &now
	})

	// 获取副本并深度篡改
	copy1 := st.GetSettings()
	copy1.DNS.AdblockAllow = append(copy1.DNS.AdblockAllow, "tampered.com")
	copy1.DNS.AdblockAllow[0] = "mutated.com"
	copy1.DNS.AdblockCustom[0] = "mutated-block.com"
	copy1.Nodes[0]["server"] = "9.9.9.9"
	if tlsObj, ok := copy1.Nodes[0]["tls"].(map[string]any); ok {
		tlsObj["server_name"] = "tampered.sni"
	}
	if transObj, ok := copy1.Nodes[0]["transport"].(map[string]any); ok {
		if headers, ok := transObj["headers"].(map[string]string); ok {
			headers["Host"] = "tampered.host"
		}
	}
	copy1.Subscriptions[0].NodeTags[0] = "TamperedTag"
	copy1.Groups[0].Keywords[0] = "US"
	copy1.Policies[0].DomainSuffix[0] = "tampered.org"
	*copy1.Meta.LastDeployAt = 999999999

	// 再次获取，验证内部原始数据完好无损
	clean := st.GetSettings()

	if len(clean.DNS.AdblockAllow) != 1 || clean.DNS.AdblockAllow[0] != "allow.com" {
		t.Errorf("DNS.AdblockAllow 被外部修改污染: %v", clean.DNS.AdblockAllow)
	}
	if clean.DNS.AdblockCustom[0] != "block.com" {
		t.Errorf("DNS.AdblockCustom 被外部修改污染: %v", clean.DNS.AdblockCustom)
	}
	if clean.Nodes[0]["server"] != "1.1.1.1" {
		t.Errorf("Nodes[0].server 被外部修改污染: %v", clean.Nodes[0]["server"])
	}
	tlsObj := clean.Nodes[0]["tls"].(map[string]any)
	if tlsObj["server_name"] != "original.sni" {
		t.Errorf("Nodes[0].tls.server_name 被外部修改污染: %v", tlsObj["server_name"])
	}
	transObj := clean.Nodes[0]["transport"].(map[string]any)
	headers := transObj["headers"].(map[string]string)
	if headers["Host"] != "original.host" {
		t.Errorf("Nodes[0].transport.headers['Host'] 被外部修改污染: %v", headers["Host"])
	}
	if clean.Subscriptions[0].NodeTags[0] != "Tag1" {
		t.Errorf("Subscriptions[0].NodeTags 被外部修改污染: %v", clean.Subscriptions[0].NodeTags)
	}
	if clean.Groups[0].Keywords[0] != "HK" {
		t.Errorf("Groups[0].Keywords 被外部修改污染: %v", clean.Groups[0].Keywords)
	}
	if clean.Policies[0].DomainSuffix[0] != "suffix.com" {
		t.Errorf("Policies[0].DomainSuffix 被外部修改污染: %v", clean.Policies[0].DomainSuffix)
	}
	if *clean.Meta.LastDeployAt != now {
		t.Errorf("Meta.LastDeployAt 被外部修改污染: %v", *clean.Meta.LastDeployAt)
	}
}

// 高并发压力测试：验证 GetSettings() 与 MutateSettings() 并发读写绝无 Data Race
func TestGetSettingsConcurrencyDataRace(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "store-race-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	st, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}

	var wg sync.WaitGroup
	workers := 10
	iterations := 100

	// 读 goroutines
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				settings := st.GetSettings()
				// 模拟外部业务逻辑对返回数据的遍历与读取
				for _, n := range settings.Nodes {
					_ = n["tag"]
				}
				for _, p := range settings.Policies {
					for _, d := range p.DomainSuffix {
						_ = d
					}
				}
				for _, a := range settings.DNS.AdblockAllow {
					_ = a
				}
			}
		}(i)
	}

	// 写 goroutines
	for i := 0; i < workers/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = st.MutateSettings(func(s *FullSettings) {
					s.DNS.AdblockAllow = []string{fmt.Sprintf("domain-%d-%d.com", id, j)}
					s.Nodes = []map[string]any{
						{
							"tag":    fmt.Sprintf("node-%d-%d", id, j),
							"server": "1.2.3.4",
						},
					}
				})
			}
		}(i)
	}

	wg.Wait()
}
