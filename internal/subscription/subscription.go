package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Subscription 对应前端 /api/subscriptions 的数据结构
type Subscription struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	URL       string           `json:"url"`
	Enabled   bool             `json:"enabled"`
	NodeCount int              `json:"nodeCount"`
	NodeTags  []string         `json:"nodeTags"`
	Nodes     []map[string]any `json:"nodes"`
	UpdatedAt string           `json:"updatedAt"`
}

// FetchAndParse 拉取远程订阅并自动识别 sing-box JSON / Clash YAML / Base64 URI
func FetchAndParse(subURL string) ([]map[string]any, error) {
	client := &http.Client{Timeout: 25 * time.Second}
	req, err := http.NewRequest("GET", subURL, nil)
	if err != nil {
		return nil, err
	}
	// 使用通用代理客户端 UA，确保机场返回包含 Hysteria2 / VLESS REALITY 的完整节点
	req.Header.Set("User-Agent", "sing-box/1.15.0 Clash.Meta/v1.18.0 Mihomo/1.18")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("拉取订阅失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("订阅返回 HTTP %d", resp.StatusCode)
	}

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(string(rawBytes))

	// 1. 尝试作为原生 sing-box JSON 解析
	if strings.HasPrefix(content, "{") {
		if nodes, err := parseSingBoxJSON(rawBytes); err == nil && len(nodes) > 0 {
			return deduplicateTags(nodes), nil
		}
	}

	// 2. 尝试作为 Clash / Mihomo YAML 解析 (包含 proxies: 字段)
	if strings.Contains(content, "proxies:") {
		if nodes, err := parseClashYAML(rawBytes); err == nil && len(nodes) > 0 {
			return deduplicateTags(nodes), nil
		}
	}

	// 3. 尝试作为 Base64 或明文 URI 分享链接解析 (vless://, hy2://, trojan://, ss://, vmess://)
	decoded := tryDecodeBase64(content)
	nodes := parseURILines(decoded)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("未能从订阅内容中解析出有效节点")
	}
	return deduplicateTags(nodes), nil
}

// 1. 解析原生 sing-box JSON
func parseSingBoxJSON(data []byte) ([]map[string]any, error) {
	var raw struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var valid []map[string]any
	ignoreTypes := map[string]bool{
		"direct": true, "block": true, "dns": true, "selector": true, "urltest": true,
	}
	for _, ob := range raw.Outbounds {
		t, _ := ob["type"].(string)
		if t == "" || ignoreTypes[strings.ToLower(t)] {
			continue
		}
		if name, ok := ob["tag"].(string); ok && name != "" {
			ob["name"] = name // 兼容前端 app.js 读取 n.name
		}
		valid = append(valid, ob)
	}
	return valid, nil
}

// 2. 解析 Clash Meta / Mihomo YAML 并转换为 sing-box Outbound 格式
func parseClashYAML(data []byte) ([]map[string]any, error) {
	var clash struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(data, &clash); err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, p := range clash.Proxies {
		if ob := convertClashProxyToSingBox(p); ob != nil {
			out = append(out, ob)
		}
	}
	return out, nil
}

func convertClashProxyToSingBox(p map[string]any) map[string]any {
	name := asString(p["name"])
	pType := strings.ToLower(asString(p["type"]))
	server := asString(p["server"])
	port := asInt(p["port"])
	if name == "" || server == "" || port == 0 {
		return nil
	}

	base := map[string]any{
		"tag":         name,
		"name":        name,
		"server":      server,
		"server_port": port,
	}

	switch pType {
	case "ss", "shadowsocks":
		base["type"] = "shadowsocks"
		base["method"] = asString(p["cipher"])
		base["password"] = asString(p["password"])
		return base

	case "trojan":
		base["type"] = "trojan"
		base["password"] = asString(p["password"])
		sni := asString(p["sni"])
		if sni == "" {
			sni = server
		}
		base["tls"] = map[string]any{
			"enabled":     true,
			"server_name": sni,
			"insecure":    asBool(p["skip-cert-verify"]),
		}
		return base

	case "vless":
		base["type"] = "vless"
		base["uuid"] = asString(p["uuid"])
		if flow := asString(p["flow"]); flow != "" {
			base["flow"] = flow
		}
		if asBool(p["tls"]) {
			sni := asString(p["servername"])
			if sni == "" {
				sni = server
			}
			tlsObj := map[string]any{
				"enabled":     true,
				"server_name": sni,
				"insecure":    asBool(p["skip-cert-verify"]),
				"utls":        map[string]any{"enabled": true, "fingerprint": "chrome"},
			}
			if ropts, ok := p["reality-opts"].(map[string]any); ok {
				tlsObj["reality"] = map[string]any{
					"enabled":    true,
					"public_key": asString(ropts["public-key"]),
					"short_id":   asString(ropts["short-id"]),
				}
			}
			base["tls"] = tlsObj
		}
		if netType := asString(p["network"]); netType == "ws" {
			wsOpts, _ := p["ws-opts"].(map[string]any)
			base["transport"] = map[string]any{
				"type": "ws",
				"path": asString(wsOpts["path"]),
			}
		}
		return base

	case "hysteria2", "hy2":
		base["type"] = "hysteria2"
		base["password"] = asString(p["password"])
		sni := asString(p["sni"])
		if sni == "" {
			sni = server
		}
		base["tls"] = map[string]any{
			"enabled":     true,
			"server_name": sni,
			"insecure":    asBool(p["skip-cert-verify"]),
		}
		if obfs := asString(p["obfs"]); obfs == "salamander" {
			base["obfs"] = map[string]any{
				"type":     "salamander",
				"password": asString(p["obfs-password"]),
			}
		}
		return base

	case "tuic":
		base["type"] = "tuic"
		base["uuid"] = asString(p["uuid"])
		base["password"] = asString(p["password"])
		base["congestion_control"] = "bbr"
		sni := asString(p["sni"])
		if sni == "" {
			sni = server
		}
		base["tls"] = map[string]any{
			"enabled":     true,
			"server_name": sni,
			"insecure":    asBool(p["skip-cert-verify"]),
		}
		return base
	}
	return nil
}

// 3. 解析 Base64 / 明文分享链接 (vless://, hy2://, trojan://, ss://)
func parseURILines(text string) []map[string]any {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []map[string]any
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if node := parseSingleURI(line); node != nil {
			out = append(out, node)
		}
	}
	return out
}

func parseSingleURI(uriStr string) map[string]any {
	u, err := url.Parse(uriStr)
	if err != nil || u.Scheme == "" {
		return nil
	}
	scheme := strings.ToLower(u.Scheme)
	tag, _ := url.QueryUnescape(u.Fragment)
	if tag == "" {
		tag = u.Hostname()
	}
	port, _ := strconv.Atoi(u.Port())
	q := u.Query()

	switch scheme {
	case "vless":
		ob := map[string]any{
			"type":        "vless",
			"tag":         tag,
			"name":        tag,
			"server":      u.Hostname(),
			"server_port": port,
			"uuid":        u.User.Username(),
		}
		if flow := q.Get("flow"); flow != "" {
			ob["flow"] = flow
		}
		sec := q.Get("security")
		if sec == "tls" || sec == "reality" {
			sni := q.Get("sni")
			if sni == "" {
				sni = u.Hostname()
			}
			tlsObj := map[string]any{
				"enabled":     true,
				"server_name": sni,
				"utls":        map[string]any{"enabled": true, "fingerprint": "chrome"},
			}
			if sec == "reality" {
				tlsObj["reality"] = map[string]any{
					"enabled":    true,
					"public_key": q.Get("pbk"),
					"short_id":   q.Get("sid"),
				}
			}
			ob["tls"] = tlsObj
		}
		return ob

	case "hysteria2", "hy2":
		pwd := u.User.Username()
		if p, ok := u.User.Password(); ok && p != "" {
			pwd = pwd + ":" + p
		}
		sni := q.Get("sni")
		if sni == "" {
			sni = u.Hostname()
		}
		return map[string]any{
			"type":        "hysteria2",
			"tag":         tag,
			"name":        tag,
			"server":      u.Hostname(),
			"server_port": port,
			"password":    pwd,
			"tls": map[string]any{
				"enabled":     true,
				"server_name": sni,
				"insecure":    q.Get("insecure") == "1",
			},
		}

	case "trojan":
		sni := q.Get("sni")
		if sni == "" {
			sni = u.Hostname()
		}
		return map[string]any{
			"type":        "trojan",
			"tag":         tag,
			"name":        tag,
			"server":      u.Hostname(),
			"server_port": port,
			"password":    u.User.Username(),
			"tls": map[string]any{
				"enabled":     true,
				"server_name": sni,
				"insecure":    q.Get("allowInsecure") == "1",
			},
		}
	}
	return nil
}

func tryDecodeBase64(s string) string {
	cleaned := strings.ReplaceAll(strings.ReplaceAll(s, "-", "+"), "_", "/")
	if pad := len(cleaned) % 4; pad != 0 {
		cleaned += strings.Repeat("=", 4-pad)
	}
	if b, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
		return string(b)
	}
	return s
}

func deduplicateTags(nodes []map[string]any) []map[string]any {
	seen := make(map[string]int)
	for _, n := range nodes {
		baseTag := asString(n["tag"])
		if baseTag == "" {
			baseTag = "未命名节点"
		}
		seen[baseTag]++
		if seen[baseTag] > 1 {
			newTag := fmt.Sprintf("%s #%d", baseTag, seen[baseTag])
			n["tag"] = newTag
			n["name"] = newTag
		}
	}
	return nodes
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}
func asInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		i, _ := strconv.Atoi(val)
		return i
	}
	return 0
}
func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}