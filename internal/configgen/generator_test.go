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
