package configgen

import (
	"encoding/json"
	"os"
	"testing"
)

func TestDirectBypassAlwaysKeepsGeoipCN(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "configgen-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	for _, directBypass := range []bool{false, true} {
		var in BuildInput
		in.DataDir = tempDir
		in.Settings.Network.DirectBypass = directBypass
		in.Nodes = []map[string]any{
			{
				"tag":    "测试节点",
				"type":   "shadowsocks",
				"server": "1.2.3.4",
			},
		}

		cfgBytes, _, err := GenerateConfig(in)
		if err != nil {
			t.Fatalf("directBypass=%v 时 GenerateConfig 失败: %v", directBypass, err)
		}

		var cfg map[string]any
		if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
			t.Fatalf("解析生成的 JSON 失败: %v", err)
		}

		route, ok := cfg["route"].(map[string]any)
		if !ok {
			t.Fatalf("route 字段缺失或类型错误")
		}

		// 1. 验证 rule_set 中必定包含 geoip-cn
		ruleSets, ok := route["rule_set"].([]any)
		if !ok {
			t.Fatalf("route.rule_set 字段缺失或类型错误")
		}
		foundGeoipRuleSet := false
		for _, rsItem := range ruleSets {
			rsMap, ok := rsItem.(map[string]any)
			if ok && rsMap["tag"] == "geoip-cn" {
				foundGeoipRuleSet = true
				break
			}
		}
		if !foundGeoipRuleSet {
			t.Errorf("directBypass=%v 时，route.rule_set 未包含 geoip-cn！", directBypass)
		}

		// 2. 验证 rules 中必定存在兜底直连且包含 geoip-cn
		rules, ok := route["rules"].([]any)
		if !ok {
			t.Fatalf("route.rules 字段缺失或类型错误")
		}
		foundDirectFallback := false
		for _, rItem := range rules {
			rMap, ok := rItem.(map[string]any)
			if !ok {
				continue
			}
			if rMap["outbound"] == "直连" {
				if rsList, ok := rMap["rule_set"].([]any); ok {
					hasGeosite := false
					hasGeoip := false
					for _, rs := range rsList {
						if rs == "geosite-cn" {
							hasGeosite = true
						}
						if rs == "geoip-cn" {
							hasGeoip = true
						}
					}
					if hasGeosite && hasGeoip {
						foundDirectFallback = true
						break
					}
				}
			}
		}
		if !foundDirectFallback {
			t.Errorf("directBypass=%v 时，未找到包含 geosite-cn 和 geoip-cn 的直连兜底规则！", directBypass)
		}
	}
}

func TestDirectBypassWithCustomPolicyAndNoNodes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "configgen-policy-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	var in BuildInput
	in.DataDir = tempDir
	in.Settings.Network.DirectBypass = true
	in.Policies = []PolicyConfig{
		{
			ID:       "pol-1",
			Name:     "国内直连策略",
			Target:   "direct",
			Rulesets: []string{"geoip-cn"},
			Enabled:  true,
		},
	}

	cfgBytes, _, err := GenerateConfig(in)
	if err != nil {
		t.Fatalf("GenerateConfig 失败: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatalf("解析生成的 JSON 失败: %v", err)
	}

	route, ok := cfg["route"].(map[string]any)
	if !ok {
		t.Fatalf("route 字段缺失")
	}

	// 验证即使策略引用了 geoip-cn，且 DirectBypass 为 true，也能够正确加载
	ruleSets, ok := route["rule_set"].([]any)
	if !ok {
		t.Fatalf("route.rule_set 缺失")
	}
	found := false
	for _, rsItem := range ruleSets {
		if rsMap, ok := rsItem.(map[string]any); ok && rsMap["tag"] == "geoip-cn" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("自定义策略引用时，route.rule_set 未包含 geoip-cn！")
	}
}

func TestStoreSelectedEnabledInConfig(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "configgen-store-selected-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	var in BuildInput
	in.DataDir = tempDir

	cfgBytes, _, err := GenerateConfig(in)
	if err != nil {
		t.Fatalf("GenerateConfig 失败: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatalf("解析生成的 JSON 失败: %v", err)
	}

	exp, ok := cfg["experimental"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 experimental 配置块")
	}
	cacheFile, ok := exp["cache_file"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 experimental.cache_file 配置块")
	}
	if cacheFile["enabled"] != true {
		t.Errorf("experimental.cache_file.enabled 必须为 true, 实际为: %v", cacheFile["enabled"])
	}
	if _, ok := cacheFile["path"].(string); !ok {
		t.Errorf("experimental.cache_file.path 必须存在有效路径")
	}
	// sing-box 1.15 只要启用 cache_file 就会自动持久化选点，严禁出现 store_selected 未知字段
	if _, ok := cacheFile["store_selected"]; ok {
		t.Errorf("experimental.cache_file 不能包含非标准的 store_selected 字段 (会被 sing-box 1.15 报错)")
	}
}

func TestTLSRecordFragmentAndDirectEvasion(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "configgen-evasion-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	var in BuildInput
	in.DataDir = tempDir
	in.Nodes = []map[string]any{
		{
			"tag":         "sg-reality-test",
			"type":        "vless",
			"server":      "54.251.136.137",
			"server_port": 443,
			"uuid":        "2f6933a0-a1c6-4048-9c3e-d9f7ddcb1b2b",
			"tls": map[string]any{
				"enabled":     true,
				"server_name": "www.nlb.gov.sg",
				"reality": map[string]any{
					"enabled":    true,
					"public_key": "dummy-key",
					"short_id":   "dummy-id",
				},
			},
		},
		{
			"tag":         "plain-ss-test",
			"type":        "shadowsocks",
			"server":      "1.2.3.4",
			"server_port": 8388,
			"method":      "2022-blake3-aes-128-gcm",
			"password":    "secret",
		},
	}

	cfgBytes, _, err := GenerateConfig(in)
	if err != nil {
		t.Fatalf("GenerateConfig 失败: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatalf("解析生成的 JSON 失败: %v", err)
	}

	// 1. 验证出站节点：Reality 节点的 tls.record_fragment 自动注入为 true
	outbounds, ok := cfg["outbounds"].([]any)
	if !ok {
		t.Fatalf("缺少 outbounds 字段")
	}
	foundRealityFragment := false
	for _, ob := range outbounds {
		obMap, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if obMap["tag"] == "sg-reality-test" {
			tlsMap, ok := obMap["tls"].(map[string]any)
			if !ok {
				t.Fatalf("sg-reality-test 缺少 tls 字段")
			}
			if frag, ok := tlsMap["record_fragment"].(bool); ok && frag {
				foundRealityFragment = true
			} else {
				t.Errorf("sg-reality-test 的 tls.record_fragment 未被正确注入为 true: %v", tlsMap["record_fragment"])
			}
		}
	}
	if !foundRealityFragment {
		t.Errorf("未能验证到 sg-reality-test 的 tls.record_fragment")
	}

	// 2. 验证路由规则：包含 tls_record_fragment: true 的免代理直连规则
	route, ok := cfg["route"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 route 字段")
	}
	rules, ok := route["rules"].([]any)
	if !ok {
		t.Fatalf("缺少 route.rules 字段")
	}
	foundDirectEvasionRoute := false
	for _, r := range rules {
		rMap, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if frag, ok := rMap["tls_record_fragment"].(bool); ok && frag {
			if rMap["outbound"] == "直连" {
				if suffixes, ok := rMap["domain_suffix"].([]any); ok && len(suffixes) > 0 {
					for _, s := range suffixes {
						if s == "github.com" {
							foundDirectEvasionRoute = true
							break
						}
					}
				}
			}
		}
	}
	if !foundDirectEvasionRoute {
		t.Errorf("未能找到包含 github.com 且带有 tls_record_fragment: true 的直连出站规则！")
	}

	// 3. 验证 DNS 规则：包含针对直连规避域名的安全 DNS 解析
	dns, ok := cfg["dns"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 dns 字段")
	}
	dnsRules, ok := dns["rules"].([]any)
	if !ok {
		t.Fatalf("缺少 dns.rules 字段")
	}
	foundDirectEvasionDNS := false
	for _, r := range dnsRules {
		rMap, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if suffixes, ok := rMap["domain_suffix"].([]any); ok {
			for _, s := range suffixes {
				if s == "github.com" {
					if rMap["server"] == "dns-proxy" {
						foundDirectEvasionDNS = true
						break
					}
				}
			}
		}
	}
	if !foundDirectEvasionDNS {
		t.Errorf("未能找到针对 github.com 走安全 DNS (dns-proxy) 的解析规则！")
	}
}
