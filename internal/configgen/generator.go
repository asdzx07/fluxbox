package configgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"fluxbox/internal/flip"
)

// FwmarkBypass = 0x2024 (8228): sing-box 自身发出的流量标记，被 eBPF 直接放行防死锁
const FwmarkBypass = 0x2024

// Settings 对应前端 /api/settings 的配置模型
type Settings struct {
	Network struct {
		IPv6           bool `json:"ipv6"`
		RejectQuic     bool `json:"rejectQuic"`
		DirectBypass   bool `json:"directBypass"`
		DirectForNodes bool `json:"directForNodes"`
	} `json:"network"`
	DNS struct {
		Mode          string   `json:"mode"`
		Direct        string   `json:"direct"`
		DirectAddress string   `json:"directAddress"`
		Proxy         string   `json:"proxy"`
		Strategy      string   `json:"strategy"`
		ProxyProtocol string   `json:"proxyProtocol"`
		Split         bool     `json:"split"`
		FakeIP        bool     `json:"fakeIp"`
		Adblock       bool     `json:"adblock"`
		AdblockAllow  []string `json:"adblockAllow"`
		AdblockCustom []string `json:"adblockCustom"`
	} `json:"dns"`
	Kernel struct {
		LogLevel string `json:"logLevel"`
	} `json:"kernel"`
}

// GroupConfig 对应前端 /api/groups 的分组配置
type GroupConfig struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`     // selector | urltest
	Mode     string   `json:"mode"`     // dynamic | static
	Keywords []string `json:"keywords"` // 动态匹配关键词
	Members  []string `json:"members"`  // 静态成员列表
	Enabled  bool     `json:"enabled"`
}

// PolicyConfig 对应前端 /api/policies 的分流策略
type PolicyConfig struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Target       string   `json:"target"`
	Rulesets     []string `json:"rulesets"`
	DomainSuffix []string `json:"domainSuffix"`
	Enabled      bool     `json:"enabled"`
}

// RulesetSub 对应前端 /api/ruleset-subs 的自定义规则集订阅
type RulesetSub struct {
	ID      string `json:"id"`
	Tag     string `json:"tag"`
	URL     string `json:"url"`
	Format  string `json:"format"`
	Enabled bool   `json:"enabled"`
}

type BuildInput struct {
	DataDir     string
	Settings    Settings
	Nodes       []map[string]any
	Groups      []GroupConfig
	Policies    []PolicyConfig
	RulesetSubs []RulesetSub
}

// GenerateConfig 生成完整的 sing-box config.json 并同步写入 Flip 开关文件
func GenerateConfig(in BuildInput) ([]byte, []string, error) {
	flipDir := filepath.Join(in.DataDir, "flip")
	_ = os.MkdirAll(flipDir, 0755)

	// 1. 收集所有节点 Tag 与节点服务器域名/IP（供 eBPF 旁路与 DNS 直连使用）
	var allNodeTags []string
	var nodeServerDomains []string
	for _, n := range in.Nodes {
		if tag, ok := n["tag"].(string); ok && tag != "" {
			allNodeTags = append(allNodeTags, tag)
		}
		if srv, ok := n["server"].(string); ok && srv != "" {
			nodeServerDomains = append(nodeServerDomains, srv)
		}
	}

	// 2. 构建 Inbounds (使用 eBPF 配套的 TProxy 入站替代传统高开销 TUN 入站)
	inbounds := []map[string]any{
		{
			"type":        "tproxy",
			"tag":         "ebpf-tproxy-in",
			"listen":      "::",
			"listen_port": 7893,
		},
		{
			"type":        "mixed",
			"tag":         "mixed-in",
			"listen":      "0.0.0.0",
			"listen_port": 7891,
		},
		{
			"type":        "direct",
			"tag":         "dns-in",
			"listen":      "::",
			"listen_port": 7853,
		},
	}

	// 3. 构建 Outbounds (基础出站 + 动态/静态策略组 + 订阅节点)
	outbounds := buildOutbounds(in.Nodes, in.Groups, allNodeTags)

	// 4. 构建 DNS 模块 (含 FakeIP、防污染分流、HTTPS 记录屏蔽、广告拦截)
	dnsConfig := buildDNS(in.Settings, nodeServerDomains)

	// 5. 构建 Route 路由模块与 Rule-Set (结合 Flip 50字节热切换机制)
	routeConfig := buildRoute(in, flipDir, nodeServerDomains)

	// 6. 组装完整 sing-box 配置
	logLevel := in.Settings.Kernel.LogLevel
	if logLevel == "" {
		logLevel = "warn"
	}
	fullConfig := map[string]any{
		"log": map[string]any{
			"level":     logLevel,
			"timestamp": true,
		},
		"dns":       dnsConfig,
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     routeConfig,
		"experimental": map[string]any{
			"clash_api": map[string]any{
				"external_controller": "127.0.0.1:9095",
			},
			"cache_file": map[string]any{
				"enabled":      true,
				"path":         filepath.Join(in.DataDir, "cache.db"),
				"store_fakeip": in.Settings.DNS.FakeIP,
			},
		},
	}

	cleanedConfig := cleanConfig(fullConfig)
	cfgBytes, err := json.MarshalIndent(cleanedConfig, "", "  ")
	return cfgBytes, nodeServerDomains, err
}

func cleanConfig(v any) any {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case map[string]any:
		res := make(map[string]any)
		for k, item := range val {
			if strings.HasPrefix(k, "__") {
				continue
			}
			cleanedItem := cleanConfig(item)
			if cleanedItem != nil {
				res[k] = cleanedItem
			}
		}
		return res
	case []any:
		var res []any
		for _, item := range val {
			cleanedItem := cleanConfig(item)
			if cleanedItem != nil {
				res = append(res, cleanedItem)
			}
		}
		return res
	case []map[string]any:
		var res []map[string]any
		for _, item := range val {
			if cleanedItem, ok := cleanConfig(item).(map[string]any); ok {
				res = append(res, cleanedItem)
			}
		}
		return res
	default:
		return v
	}
}

func buildOutbounds(nodes []map[string]any, groups []GroupConfig, allTags []string) []map[string]any {
	var out []map[string]any

	// 兜底防空：若当前没有可用节点，放入直连占位防止 sing-box 启动报错
	fallbackMembers := allTags
	if len(fallbackMembers) == 0 {
		fallbackMembers = []string{"直连"}
	}

	// 内置核心分组：所有-自动 (urltest) 与 所有-手动 (selector)
	out = append(out,
		map[string]any{
			"type":      "urltest",
			"tag":       "所有-自动",
			"outbounds": fallbackMembers,
			"url":       "http://www.gstatic.com/generate_204",
			"interval":  "5m",
			"tolerance": 100,
		},
		map[string]any{
			"type":      "selector",
			"tag":       "所有-手动",
			"outbounds": append([]string{"所有-自动"}, fallbackMembers...),
			"default":   "所有-自动",
		},
	)

	// 用户自定义分组（支持按关键词自动收编节点）
	for _, g := range groups {
		if !g.Enabled || g.ID == "all-auto" || g.ID == "all-manual" || g.Name == "所有-自动" || g.Name == "所有-手动" {
			continue
		}
		var matched []string
		if g.Mode == "dynamic" && len(g.Keywords) > 0 {
			for _, tag := range allTags {
				upperTag := strings.ToUpper(tag)
				for _, kw := range g.Keywords {
					if kw != "" && strings.Contains(upperTag, strings.ToUpper(kw)) {
						matched = append(matched, tag)
						break
					}
				}
			}
		} else {
			matched = g.Members
		}
		if len(matched) == 0 {
			matched = []string{"所有-自动", "直连"}
		}

		gType := strings.ToLower(g.Type)
		if gType == "urltest" {
			out = append(out, map[string]any{
				"type":      "urltest",
				"tag":       g.Name,
				"outbounds": matched,
				"url":       "http://www.gstatic.com/generate_204",
				"interval":  "5m",
			})
		} else {
			out = append(out, map[string]any{
				"type":      "selector",
				"tag":       g.Name,
				"outbounds": matched,
			})
		}
	}

	// 注入全部真实节点（清洗内部使用的 __ 字段）
	for _, n := range nodes {
		cleanedNode := make(map[string]any)
		for k, v := range n {
			if !strings.HasPrefix(k, "__") {
				cleanedNode[k] = v
			}
		}
		out = append(out, cleanedNode)
	}

	// 内置基础出站
	out = append(out,
		map[string]any{"type": "direct", "tag": "直连"},
		map[string]any{"type": "block", "tag": "拒绝"},
	)
	return out
}

func buildDNS(s Settings, nodeServers []string) map[string]any {
	directDNS := s.DNS.DirectAddress
	if directDNS == "" {
		directDNS = "223.5.5.5"
	}
	proxyDNS := s.DNS.Proxy
	if proxyDNS == "" {
		proxyDNS = "1.1.1.1"
	}
	proxyProto := s.DNS.ProxyProtocol
	if proxyProto == "" {
		proxyProto = "tcp"
	}
	strategy := s.DNS.Strategy
	if strategy == "" {
		strategy = "prefer_ipv4"
	}
	if !s.Network.IPv6 {
		strategy = "ipv4_only"
	}

	servers := []map[string]any{
		{
			"tag":         "dns-direct",
			"type":        "udp",
			"server":      directDNS,
			"server_port": 53,
		},
		{
			"tag":         "dns-proxy",
			"type":        proxyProto,
			"server":      proxyDNS,
			"server_port": 53,
			"detour":      "所有-自动",
		},
	}

	if s.DNS.FakeIP {
		servers = append(servers, map[string]any{
			"tag":         "dns-fakeip",
			"type":        "fakeip",
			"inet4_range": "198.19.0.0/16",
		})
	}

	var rules []map[string]any

	// 1. 吸收勘察报告设计：屏蔽 HTTPS/SVCB 查询记录，防止浏览器绕过透明代理分流
	rules = append(rules, map[string]any{
		"query_type": []string{"HTTPS", "SVCB"},
		"action":     "predefined",
		"rcode":      "NOERROR",
	})

	// 2. 节点服务器自身域名强制走国内直连 DNS，防止死锁
	if len(nodeServers) > 0 {
		rules = append(rules, map[string]any{
			"domain": nodeServers,
			"server": "dns-direct",
		})
	}

	// 3. DNS 广告过滤与黑白名单 (对应前端「DNS 广告拦截」页)
	if s.DNS.Adblock {
		if len(s.DNS.AdblockAllow) > 0 {
			rules = append(rules, map[string]any{
				"domain_suffix": s.DNS.AdblockAllow,
				"server":        "dns-direct",
			})
		}
		if len(s.DNS.AdblockCustom) > 0 {
			rules = append(rules, map[string]any{
				"domain_suffix": s.DNS.AdblockCustom,
				"action":        "reject",
			})
		}
		rules = append(rules, map[string]any{
			"rule_set": []string{"geosite-category-ads-all"},
			"action":   "reject",
		})
	}

	// 4. 国内外智能分流与 FakeIP
	if s.DNS.Split {
		rules = append(rules, map[string]any{
			"rule_set": []string{"geosite-cn"},
			"server":   "dns-direct",
		})
	}
	if s.DNS.FakeIP {
		rules = append(rules, map[string]any{
			"query_type": []string{"A", "AAAA"},
			"server":     "dns-fakeip",
		})
	}

	return map[string]any{
		"servers":  servers,
		"rules":    rules,
		"final":    "dns-proxy",
		"strategy": strategy,
	}
}

func buildRoute(in BuildInput, flipDir string, nodeServers []string) map[string]any {
	rules := []map[string]any{
		{"action": "sniff"},
		{"protocol": "dns", "action": "hijack-dns"},
		{"ip_is_private": true, "outbound": "直连"},
	}

	// 节点服务器强制直连
	if in.Settings.Network.DirectForNodes && len(nodeServers) > 0 {
		rules = append(rules, map[string]any{
			"domain":   nodeServers,
			"outbound": "直连",
		})
	}

	referencedRulesets := map[string]bool{
		"geosite-cn":               true,
		"geoip-cn":                 true,
		"geosite-category-ads-all": in.Settings.DNS.Adblock,
	}
	var flipRuleSets []map[string]any

	// 遍历用户配置的「目标分流」策略：每个策略生成一个 Flip 外部规则集实现 0 断流秒切
	for _, p := range in.Policies {
		flipTag := "obflip-" + p.ID
		flipPath := filepath.Join(flipDir, flipTag+".json")
		_ = flip.WritePolicyFlip(flipDir, p.ID, p.Enabled)

		flipRuleSets = append(flipRuleSets, map[string]any{
			"type":   "local",
			"tag":    flipTag,
			"format": "source",
			"path":   flipPath,
		})

		targetOutbound := resolveTargetOutbound(p.Target, in.Groups)

		var matchSubRules []map[string]any
		if len(p.Rulesets) > 0 {
			for _, rs := range p.Rulesets {
				referencedRulesets[rs] = true
			}
			matchSubRules = append(matchSubRules, map[string]any{"rule_set": p.Rulesets})
		}
		if len(p.DomainSuffix) > 0 {
			matchSubRules = append(matchSubRules, map[string]any{"domain_suffix": p.DomainSuffix})
		}
		if len(matchSubRules) == 0 {
			continue
		}

		// 组合条件：(命中策略规则集 OR 命中域名后缀) AND 命中 Flip 热切换开关
		rules = append(rules, map[string]any{
			"type": "logical",
			"mode": "and",
			"rules": []map[string]any{
				{
					"type":  "logical",
					"mode":  "or",
					"rules": matchSubRules,
				},
				{"rule_set": []string{flipTag}},
			},
			"outbound": targetOutbound,
		})
	}

	// 国内流量兜底直连
	rules = append(rules, map[string]any{
		"rule_set": []string{"geosite-cn", "geoip-cn"},
		"outbound": "直连",
	})

	// 组装 rule_set 定义列表 (本地 Flip 开关 + 远程 .srs 规则集)
	ruleSets := append([]map[string]any{}, flipRuleSets...)
	customSubMap := make(map[string]RulesetSub)
	for _, sub := range in.RulesetSubs {
		if sub.Enabled {
			customSubMap[sub.Tag] = sub
		}
	}

	for rsTag, needed := range referencedRulesets {
		if !needed {
			continue
		}
		if custom, ok := customSubMap[rsTag]; ok {
			ruleSets = append(ruleSets, map[string]any{
				"type":   "remote",
				"tag":    custom.Tag,
				"format": custom.Format,
				"url":    custom.URL,
				"http_client": map[string]any{
					"detour": "所有-自动",
				},
			})
		} else {
			// 默认映射至 SagerNet 官方 sing-geosite / sing-geoip 二进制源
			repo := "sing-geosite"
			if strings.HasPrefix(rsTag, "geoip-") {
				repo = "sing-geoip"
			}
			ruleSets = append(ruleSets, map[string]any{
				"type":   "remote",
				"tag":    rsTag,
				"format": "binary",
				"url":    "https://raw.githubusercontent.com/SagerNet/" + repo + "/rule-set/" + rsTag + ".srs",
				"http_client": map[string]any{
					"detour": "所有-自动",
				},
			})
		}
	}

	return map[string]any{
		"rules":                   rules,
		"rule_set":                ruleSets,
		"final":                   "所有-自动",
		"auto_detect_interface":   true,
		"default_domain_resolver": "dns-direct",
		"default_http_client": map[string]any{
			"detour": "所有-自动",
		},
		"default_mark":            FwmarkBypass, // 0x2024：配合 eBPF 实现零损耗防回环
	}
}

func resolveTargetOutbound(target string, groups []GroupConfig) string {
	switch target {
	case "builtin-direct", "direct":
		return "直连"
	case "builtin-block", "block":
		return "拒绝"
	case "all-auto", "":
		return "所有-自动"
	case "all-manual":
		return "所有-手动"
	}
	for _, g := range groups {
		if g.ID == target || g.Name == target {
			return g.Name
		}
	}
	return target
}