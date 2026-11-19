package configgen

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"

	"fluxbox/internal/flip"
)

// FwmarkBypass = 0x2024 (8228): sing-box 自身发出的流量标记，被 eBPF 直接放行防死锁
const FwmarkBypass = 0x2024

// DirectEvasionSuffixes 对应 CircumVolve 论文成果落地的免代理直连规避域名
// 涵盖仅受 SNI 干扰阻断但无需隐藏真实来源 IP 的海外开发镜像、开源库与公共 CDN 服务
// 注意：GitHub 域名已彻底释放回用户自定义分流策略（p-github / geosite-github），由用户自由调度走代理或直连
// 配合 sing-box 原生 tls_record_fragment 直连穿透，大幅卸载主力代理节点流量
var DirectEvasionSuffixes = []string{
	"docker.com",
	"docker.io",
	"npmjs.org",
	"npmjs.com",
	"pypi.org",
	"crates.io",
	"golang.org",
	"pkg.go.dev",
	"jsdelivr.net",
	"cdnjs.cloudflare.com",
	"unpkg.com",
	"wikipedia.org",
	"wikimedia.org",
	"archive.org",
	"arxiv.org",
	"sciencedirect.com",
}

// GithubCleanHosts GitHub 官方亚太与全球极速 CDN 节点优选静态 IP 映射表
// 包含 Microsoft Azure 官方核心 Anycast IP 与 Fastly 亚太低延迟边缘节点
// 配合 TLS 记录层分片穿透，彻底消除国内运营商国际出口丢包与 Local DNS 污染
var GithubCleanHosts = map[string][]string{
	"github.com":                    {"20.205.243.166"},
	"api.github.com":                {"20.205.243.168"},
	"codeload.github.com":           {"20.205.243.165"},
	"alive.github.com":              {"140.82.113.26"},
	"assets-cdn.github.com":         {"185.199.108.153", "185.199.109.153", "185.199.110.153", "185.199.111.153"},
	"githubassets.com":              {"185.199.108.154", "185.199.109.154", "185.199.110.154", "185.199.111.154"},
	"githubusercontent.com":         {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"raw.githubusercontent.com":     {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"gist.githubusercontent.com":    {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"camo.githubusercontent.com":    {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"objects.githubusercontent.com": {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"media.githubusercontent.com":   {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"avatars.githubusercontent.com": {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"avatars0.githubusercontent.com": {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"avatars1.githubusercontent.com": {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"avatars2.githubusercontent.com": {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"avatars3.githubusercontent.com": {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"avatars4.githubusercontent.com": {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
	"avatars5.githubusercontent.com": {"185.199.108.133", "185.199.109.133", "185.199.110.133", "185.199.111.133"},
}

// Settings 对应前端 /api/settings 的配置模型
type Settings struct {
	Network struct {
		IPv6           bool `json:"ipv6"`
		RejectQuic     bool `json:"rejectQuic"`
		DirectBypass   bool `json:"directBypass"`
		DirectForNodes bool `json:"directForNodes"`
		Sniff          bool `json:"sniff"`
		Tun            struct {
			AutoRedirect bool `json:"autoRedirect"`
			Aggressive   bool `json:"aggressive"`
			StrictRoute  bool `json:"strictRoute"`
		} `json:"tun"`
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
		HijackPort    int      `json:"hijackPort"`
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
	ID            string `json:"id"`
	Tag           string `json:"tag"`
	URL           string `json:"url"`
	Format        string `json:"format"`
	Enabled       bool   `json:"enabled"`
	UpdatedAt     *int64 `json:"updatedAt,omitempty"`
	FormattedDate string `json:"formattedDate,omitempty"`
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

	// 1. 收集所有节点 Tag 与节点服务器域名/IP（供 eBPF 旁路、DNS 解析与直连放行使用）
	var allNodeTags []string
	var nodeServerDomains []string
	var nodeServerIPs []string
	domainSeen := make(map[string]bool)
	ipSeen := make(map[string]bool)

	for _, n := range in.Nodes {
		nodeType, _ := n["type"].(string)
		// NaiveProxy 依赖 cronet 动态库 (libcronet.so)，嵌入式/软路由系统通常缺失该库，跳过防崩溃
		if strings.EqualFold(nodeType, "naive") {
			continue
		}

		if tag, ok := n["tag"].(string); ok && tag != "" {
			allNodeTags = append(allNodeTags, tag)
		}
		if srv, ok := n["server"].(string); ok && srv != "" {
			srv = strings.TrimSpace(srv)
			if parsedIP := net.ParseIP(srv); parsedIP != nil {
				cidr := srv + "/32"
				if parsedIP.To4() == nil {
					cidr = srv + "/128"
				}
				if !ipSeen[cidr] {
					ipSeen[cidr] = true
					nodeServerIPs = append(nodeServerIPs, cidr)
				}
			} else {
				if !domainSeen[srv] {
					domainSeen[srv] = true
					nodeServerDomains = append(nodeServerDomains, srv)
				}
			}
		}
	}

	// 2. 构建 Inbounds (经旧项目实测验证的 sing-box 官方 TUN 内核级透明重定向架构)
	dnsPort := in.Settings.DNS.HijackPort
	if dnsPort == 0 {
		dnsPort = 36053
	}

	mtu := 1500
	if in.Settings.Network.Tun.Aggressive {
		mtu = 9000
	}
	strictRoute := in.Settings.Network.Tun.StrictRoute
	autoRedirect := in.Settings.Network.Tun.AutoRedirect

	addressList := []string{"172.19.0.1/30"}
	dnsAddressList := []string{"172.19.0.2"}
	routeExcludeList := []string{
		"10.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16", "172.16.0.0/12",
		"192.168.0.0/16", "224.0.0.0/4", "255.255.255.255/32",
	}

	if in.Settings.Network.IPv6 {
		addressList = append(addressList, "fdfe:dcba:9876::1/126")
		dnsAddressList = append(dnsAddressList, "fdfe:dcba:9876::2")
		routeExcludeList = append(routeExcludeList, "fc00::/7", "fe80::/10")
	}

	tunInbound := map[string]any{
		"type":                  "tun",
		"tag":                   "tun-in",
		"interface_name":        "fluxbox-tun",
		"address":               addressList,
		"mtu":                   mtu,
		"auto_route":            true,
		"strict_route":          strictRoute,
		"udp_timeout":           "60s",
		"route_exclude_address": routeExcludeList,
		"dns_mode":              "hijack",
		"dns_address":           dnsAddressList,
	}
	if autoRedirect {
		tunInbound["auto_redirect"] = true
		tunInbound["auto_redirect_output_mark"] = FwmarkBypass
	}

	inbounds := []map[string]any{
		tunInbound,

		{
			"type":        "mixed",
			"tag":         "mixed-in",
			"listen":      "::",
			"listen_port": 7891,
		},
		{
			"type":        "direct",
			"tag":         "dns-in",
			"listen":      "::",
			"listen_port": dnsPort,
		},
	}

	// 3. 构建 Outbounds (基础出站 + 动态/静态策略组 + 订阅节点)
	outbounds := buildOutbounds(in.Nodes, in.Groups, allNodeTags, in.Settings.Network.IPv6)

	// 4. 构建 DNS 模块 (含 FakeIP、防污染分流、HTTPS 记录屏蔽、广告拦截)
	dnsConfig := buildDNS(in.Settings, nodeServerDomains, len(in.Nodes) > 0)

	// 5. 构建 Route 路由规则 (分流策略、Flip 热重载规则集映射)
	routeConfig := buildRoute(in, flipDir, nodeServerDomains, nodeServerIPs)

	// 6. 组装完整 sing-box 配置
	logLevel := in.Settings.Kernel.LogLevel
	if logLevel == "" {
		logLevel = "info"
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

func buildOutbounds(nodes []map[string]any, groups []GroupConfig, allTags []string, enableIPv6 bool) []map[string]any {
	var out []map[string]any

	// 关键修复：当还没有拉取任何订阅节点时，urltest 不能包含空或直连测速，必须回退为 selector 指向直连
	if len(allTags) == 0 {
		out = append(out,
			map[string]any{
				"type":      "selector",
				"tag":       "所有-自动",
				"outbounds": []string{"直连"},
				"default":   "直连",
			},
			map[string]any{
				"type":      "selector",
				"tag":       "所有-手动",
				"outbounds": []string{"直连"},
				"default":   "直连",
			},
		)
	} else {
		out = append(out,
			map[string]any{
				"type":      "urltest",
				"tag":       "所有-自动",
				"outbounds": allTags,
				"url":       "http://cp.cloudflare.com/generate_204",
				"interval":  "3m",
				"tolerance": 80,
			},
			map[string]any{
				"type":      "selector",
				"tag":       "所有-手动",
				"outbounds": append([]string{"所有-自动"}, allTags...),
				"default":   "所有-自动",
			},
		)
	}

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
				"url":       "http://cp.cloudflare.com/generate_204",
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

	// 注入全部真实节点（清洗内部使用的 __ 字段与 UI 辅助字段）
	for _, n := range nodes {
		nodeType, _ := n["type"].(string)
		// NaiveProxy 依赖 cronet 动态库，软路由环境跳过
		if strings.EqualFold(nodeType, "naive") {
			continue
		}

		cleanedNode := make(map[string]any)
		for k, v := range n {
			// sing-box 严格校验未知字段：出站对象标识字段为 "tag"，严禁带 "name" 及前端私有字段
			if strings.HasPrefix(k, "__") || k == "name" || k == "id" || k == "latency" || k == "history" {
				continue
			}
			cleanedNode[k] = v
		}
		// 容错：若未设置 tag 但有 name，则使用 name 充当 tag
		if _, ok := cleanedNode["tag"]; !ok {
			if name, ok := n["name"].(string); ok && name != "" {
				cleanedNode["tag"] = name
			}
		}

		// CircumVolve (CCS 2026) 成果落地：对所有 TLS 代理出站注入 TLS 记录层分片 (Record Fragmentation)
		// 打散 ClientHello 中的 SNI 明文与握手特征，防止 GFW 骨干网单包识别、打标与针对性阻断
		if tlsMap, ok := cleanedNode["tls"].(map[string]any); ok && tlsMap != nil {
			if enabled, hasEnabled := tlsMap["enabled"].(bool); !hasEnabled || enabled {
				tlsMap["record_fragment"] = true
				cleanedNode["tls"] = tlsMap
			}
		}

		out = append(out, cleanedNode)
	}

	// 内置基础出站
	directOut := map[string]any{
		"type":               "direct",
		"tag":                "直连",
		"inet4_bind_address": "0.0.0.0",
	}
	if enableIPv6 {
		directOut["inet6_bind_address"] = "::"
	}
	out = append(out, directOut, map[string]any{"type": "block", "tag": "拒绝"})
	return out
}

func buildDNS(s Settings, nodeServers []string, hasNodes bool) map[string]any {
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
			"detour":      "直连",
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
		fakeServer := map[string]any{
			"tag":         "dns-fakeip",
			"type":        "fakeip",
			"inet4_range": "198.19.0.0/16",
		}
		if s.Network.IPv6 {
			fakeServer["inet6_range"] = "fc00::/18"
		}
		servers = append(servers, fakeServer)
	}

	// 注入 GitHub 官方亚太与全球极速 CDN 节点优选静态 DNS 传输（方案 B 核心闭环）
	servers = append(servers, map[string]any{
		"tag":        "dns-hosts",
		"type":       "hosts",
		"predefined": GithubCleanHosts,
	})

	var rules []map[string]any

	// 1. 吸收勘察报告设计：屏蔽 HTTPS/SVCB 查询记录，防止浏览器绕过透明代理分流
	rules = append(rules, map[string]any{
		"query_type": []string{"HTTPS", "SVCB"},
		"action":     "predefined",
		"rcode":      "NOERROR",
	})

	// 1.1 GitHub 官方优质 CDN 优选解析 (免代理高速引擎：0ms 瞬间直达 Clean IP，杜绝国内运营商出口丢包)
	var githubDomains []string
	for d := range GithubCleanHosts {
		githubDomains = append(githubDomains, d)
	}
	rules = append(rules, map[string]any{
		"domain": githubDomains,
		"server": "dns-hosts",
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
			"rule_set": []string{"geosite-category-ads"},
			"action":   "reject",
		})
	}

	// 4. 国内常用域名无条件直连 DNS 解析（免死金牌，保证离线与规则集未下载时国内全部秒开）
	cnSuffixes := []string{
		".cn", ".com.cn", ".net.cn", ".edu.cn", ".gov.cn", ".top",
		".baidu.com", ".qq.com", ".weixin.qq.com", ".taobao.com", ".jd.com",
		".bilibili.com", ".aliyun.com", ".alipay.com", ".163.com", ".126.net",
		".sina.com.cn", ".weibo.com", ".zhihu.com", ".douyin.com", ".bytedance.com",
		".feishu.cn", ".meituan.com", ".dianping.com", ".sohu.com", ".youku.com",
		".iqiyi.com", ".apple.com", ".icloud.com", ".microsoft.com",
	}
	rules = append(rules, map[string]any{
		"domain_suffix": cnSuffixes,
		"server":        "dns-direct",
	})

	// 5. 国内外智能分流与 FakeIP
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

	finalDNS := "dns-proxy"
	if !hasNodes {
		finalDNS = "dns-direct"
	}

	// 6. 对免代理直连规避域名强制走无污染安全 DNS (finalDNS) 解析真实 IP，防止 UDP 53 旁路投毒
	rules = append(rules, map[string]any{
		"domain_suffix": DirectEvasionSuffixes,
		"server":        finalDNS,
	})

	return map[string]any{
		"servers":  servers,
		"rules":    rules,
		"final":    finalDNS,
		"strategy": strategy,
	}
}

func buildRoute(in BuildInput, flipDir string, nodeServerDomains, nodeServerIPs []string) map[string]any {
	rules := []map[string]any{
		{"action": "sniff"},
		{"inbound": []string{"dns-in"}, "action": "hijack-dns"},
		{"protocol": "dns", "action": "hijack-dns"},
		{"port": []int{53}, "action": "hijack-dns"},
		{"ip_is_private": true, "outbound": "直连"},
		{
			"domain_suffix": []string{".cn", ".baidu.com", ".qq.com", ".weixin.qq.com", ".taobao.com", ".jd.com", ".bilibili.com", ".aliyun.com", ".apple.com", ".icloud.com"},
			"outbound":      "直连",
		},
		// CircumVolve (CCS 2026) 落地：免代理直连规避出站 (通过 TLS 记录层分片穿透 SNI 阻断)
		{
			"domain_suffix":       DirectEvasionSuffixes,
			"tls_record_fragment": true,
			"outbound":            "直连",
		},
	}

	// 节点服务器强制直连 (域名走 domain，纯 IP 走 ip_cidr)
	if in.Settings.Network.DirectForNodes {
		if len(nodeServerDomains) > 0 {
			rules = append(rules, map[string]any{
				"domain":   nodeServerDomains,
				"outbound": "直连",
			})
		}
		if len(nodeServerIPs) > 0 {
			rules = append(rules, map[string]any{
				"ip_cidr":  nodeServerIPs,
				"outbound": "直连",
			})
		}
	}

	// 始终保留 geoip-cn 作为规则集兜底路由，防止国内纯 IP 流量在 Fallback 时跌落至代理出站
	referencedRulesets := map[string]bool{
		"geosite-cn":           true,
		"geoip-cn":             true,
		"geosite-category-ads": in.Settings.DNS.Adblock,
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
			var activeRulesets []string
			for _, rs := range p.Rulesets {
				referencedRulesets[rs] = true
				activeRulesets = append(activeRulesets, rs)
			}
			if len(activeRulesets) > 0 {
				matchSubRules = append(matchSubRules, map[string]any{"rule_set": activeRulesets})
			}
		}
		if len(p.DomainSuffix) > 0 {
			matchSubRules = append(matchSubRules, map[string]any{"domain_suffix": p.DomainSuffix})
		}
		if len(matchSubRules) == 0 {
			continue
		}

		// 组合条件：(命中策略规则集 OR 命中域名后缀) AND 命中 Flip 热切换开关
		policyRule := map[string]any{
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
		}

		// 【方案 B 完整闭环】：当策略出口为「直连」时，若该策略属于 GitHub 或包含 GitHub 域名/规则集，自动启用 TLS 记录层分片穿透
		// 配合 dns-hosts 的官方亚太 Clean IP 映射，实现 0 代理流量消耗、秒级高速直连穿透
		if targetOutbound == "直连" {
			isGithubPolicy := false
			if strings.Contains(strings.ToLower(p.Name), "github") || strings.Contains(strings.ToLower(p.ID), "github") {
				isGithubPolicy = true
			}
			if !isGithubPolicy {
				for _, rs := range p.Rulesets {
					if rs == "geosite-github" || rs == "geosite-gitlab" {
						isGithubPolicy = true
						break
					}
				}
			}
			if !isGithubPolicy {
				for _, dom := range p.DomainSuffix {
					if strings.Contains(strings.ToLower(dom), "github") || strings.Contains(strings.ToLower(dom), "gitlab") {
						isGithubPolicy = true
						break
					}
				}
			}
			if isGithubPolicy {
				policyRule["tls_record_fragment"] = true
			}
		}

		rules = append(rules, policyRule)
	}

	// 国内流量兜底直连：始终包含 geosite-cn 与 geoip-cn，防止国内纯 IP 流量倒灌跌落至代理出站
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

	detourOutbound := "所有-自动"
	if len(in.Nodes) == 0 {
		detourOutbound = "直连"
	}

	for rsTag, needed := range referencedRulesets {
		if !needed {
			continue
		}
		if custom, ok := customSubMap[rsTag]; ok {
			ruleSets = append(ruleSets, map[string]any{
				"type":            "remote",
				"tag":             custom.Tag,
				"format":          custom.Format,
				"url":             custom.URL,
				"download_detour": detourOutbound,
				"update_interval": "1d",
			})
		} else {
			// 默认映射至 SagerNet 官方 sing-geosite / sing-geoip 二进制源
			repo := "sing-geosite"
			if strings.HasPrefix(rsTag, "geoip-") {
				repo = "sing-geoip"
			}
			ruleSets = append(ruleSets, map[string]any{
				"type":            "remote",
				"tag":             rsTag,
				"format":          "binary",
				"url":             "https://testingcf.jsdelivr.net/gh/SagerNet/" + repo + "@rule-set/" + rsTag + ".srs",
				"download_detour": detourOutbound,
				"update_interval": "1d",
			})
		}
	}

	finalRoute := "所有-自动"
	if len(in.Nodes) == 0 {
		finalRoute = "直连"
	}

	return map[string]any{
		"rules":                   rules,
		"rule_set":                ruleSets,
		"final":                   finalRoute,
		"auto_detect_interface":   true,
		"default_domain_resolver": map[string]any{
			"server": "dns-direct",
		},
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