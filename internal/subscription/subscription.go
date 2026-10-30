package subscription

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// Userinfo 对应机场订阅流量与有效期信息 (Subscription-Userinfo 响应头)
type Userinfo struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	Expire   int64 `json:"expire"`
}

// Subscription 对应前端 /api/subscriptions 的数据结构
type Subscription struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	URL       string           `json:"url"`
	Enabled   bool             `json:"enabled"`
	NodeCount int              `json:"nodeCount"`
	NodeTags  []string         `json:"nodeTags"`
	Nodes     []map[string]any `json:"nodes"`
	UpdatedAt any              `json:"updatedAt"`
	AddedAt   any              `json:"addedAt,omitempty"`
	Format    string           `json:"format,omitempty"`
	Userinfo  *Userinfo        `json:"userinfo,omitempty"`
}

// ParseSubscriptionUserinfo 解析 HTTP 响应头 Subscription-Userinfo
func ParseSubscriptionUserinfo(raw string) *Userinfo {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	info := &Userinfo{}
	hasAny := false
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ';' || r == ','
	})
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		valStr := strings.Trim(strings.TrimSpace(kv[1]), "\"'")
		val, err := strconv.ParseInt(valStr, 10, 64)
		if err != nil {
			if f, fErr := strconv.ParseFloat(valStr, 64); fErr == nil {
				val = int64(f)
			} else {
				continue
			}
		}
		switch key {
		case "upload":
			info.Upload = val
			hasAny = true
		case "download":
			info.Download = val
			hasAny = true
		case "total":
			info.Total = val
			hasAny = true
		case "expire":
			info.Expire = val
			hasAny = true
		}
	}
	if !hasAny {
		return nil
	}
	return info
}

// FetchAndParse 拉取远程订阅并自动识别 sing-box JSON / Clash YAML / Base64 URI (兼容旧签名)
func FetchAndParse(subURL string) ([]map[string]any, error) {
	nodes, _, err := FetchAndParseWithInfo(subURL)
	return nodes, err
}

// FetchAndParseWithInfo 拉取远程订阅并同时解析返回节点列表与 Subscription-Userinfo 配额信息
func FetchAndParseWithInfo(subURL string) ([]map[string]any, *Userinfo, error) {
	rawBytes, headers, err := fetchRawBytes(subURL)
	if err != nil {
		return nil, nil, fmt.Errorf("拉取订阅失败: %w", err)
	}

	var userinfo *Userinfo
	if headers != nil {
		userinfo = ParseSubscriptionUserinfo(headers.Get("Subscription-Userinfo"))
	}

	// 去除可能存在的 UTF-8 BOM 头与首尾空白
	content := strings.TrimSpace(strings.TrimPrefix(string(rawBytes), "\xef\xbb\xbf"))

	// 1. 尝试作为原生 sing-box JSON 解析
	if strings.HasPrefix(content, "{") {
		if nodes, err := parseSingBoxJSON([]byte(content)); err == nil && len(nodes) > 0 {
			return deduplicateTags(nodes), userinfo, nil
		}
	}

	// 2. 尝试作为 Clash / Mihomo YAML 解析 (包含 proxies: 字段)
	if strings.Contains(content, "proxies:") {
		if nodes, err := parseClashYAML([]byte(content)); err == nil && len(nodes) > 0 {
			return deduplicateTags(nodes), userinfo, nil
		}
	}

	// 3. 尝试作为 Base64 或明文 URI 分享链接解析 (vless://, hy2://, trojan://, ss://, vmess://)
	decoded := tryDecodeBase64(content)
	// 如果 Base64 解码后实际上是 Clash YAML (部分机场采用此编码)
	if strings.Contains(decoded, "proxies:") {
		if nodes, err := parseClashYAML([]byte(decoded)); err == nil && len(nodes) > 0 {
			return deduplicateTags(nodes), userinfo, nil
		}
	}
	// 如果 Base64 解码后是 sing-box JSON
	if strings.HasPrefix(strings.TrimSpace(decoded), "{") {
		if nodes, err := parseSingBoxJSON([]byte(decoded)); err == nil && len(nodes) > 0 {
			return deduplicateTags(nodes), userinfo, nil
		}
	}

	nodes := parseURILines(decoded)
	if len(nodes) == 0 {
		return nil, userinfo, fmt.Errorf("未能从订阅内容中解析出有效节点 (响应长度 %d 字节)", len(rawBytes))
	}
	return deduplicateTags(nodes), userinfo, nil
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

	case "vmess":
		uuid := asString(p["uuid"])
		if uuid == "" {
			return nil
		}
		base["type"] = "vmess"
		base["uuid"] = uuid
		alterId := asInt(p["alterId"])
		if alterId == 0 {
			alterId = asInt(p["alter_id"])
		}
		base["alter_id"] = alterId

		security := asString(p["cipher"])
		if security == "" {
			security = "auto"
		}
		base["security"] = security

		if asBool(p["tls"]) {
			sni := asString(p["servername"])
			if sni == "" {
				sni = asString(p["sni"])
			}
			if sni == "" {
				sni = server
			}
			base["tls"] = map[string]any{
				"enabled":     true,
				"server_name": sni,
				"insecure":    asBool(p["skip-cert-verify"]),
			}
		}

		if pe := asString(p["packet-encoding"]); pe != "" {
			base["packet_encoding"] = pe
		}

		netType := strings.ToLower(asString(p["network"]))
		switch netType {
		case "ws", "websocket":
			transport := map[string]any{"type": "ws"}
			var path string
			var headers map[string]any
			if wsOpts, ok := p["ws-opts"].(map[string]any); ok {
				path = asString(wsOpts["path"])
				if h, ok := wsOpts["headers"].(map[string]any); ok {
					headers = h
				} else if hStr, ok := wsOpts["headers"].(map[string]string); ok {
					headers = make(map[string]any, len(hStr))
					for k, v := range hStr {
						headers[k] = v
					}
				}
			}
			if path == "" {
				path = asString(p["ws-path"])
			}
			if headers == nil {
				if h, ok := p["ws-headers"].(map[string]any); ok {
					headers = h
				} else if hStr, ok := p["ws-headers"].(map[string]string); ok {
					headers = make(map[string]any, len(hStr))
					for k, v := range hStr {
						headers[k] = v
					}
				}
			}
			if path != "" {
				transport["path"] = path
			}
			if len(headers) > 0 {
				hMap := make(map[string]string)
				for k, v := range headers {
					hMap[k] = asString(v)
				}
				transport["headers"] = hMap
			}
			base["transport"] = transport
		case "grpc":
			transport := map[string]any{"type": "grpc"}
			var sn string
			if grpcOpts, ok := p["grpc-opts"].(map[string]any); ok {
				sn = asString(grpcOpts["grpc-service-name"])
				if sn == "" {
					sn = asString(grpcOpts["service-name"])
				}
				if sn == "" {
					sn = asString(grpcOpts["service_name"])
				}
			}
			if sn == "" {
				sn = asString(p["grpc-service-name"])
			}
			if sn != "" {
				transport["service_name"] = sn
			}
			base["transport"] = transport
		case "http", "h2":
			transport := map[string]any{"type": "http"}
			if hOpts, ok := p["http-opts"].(map[string]any); ok {
				if s, ok := hOpts["path"].(string); ok && s != "" {
					transport["path"] = s
				} else if pList, ok := hOpts["path"].([]any); ok && len(pList) > 0 {
					transport["path"] = asString(pList[0])
				} else if pList, ok := hOpts["path"].([]string); ok && len(pList) > 0 {
					transport["path"] = pList[0]
				}
			}
			base["transport"] = transport
		}
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
	uriStr = strings.TrimSpace(uriStr)
	if uriStr == "" {
		return nil
	}

	// 优先处理 vmess://base64(json)
	if strings.HasPrefix(strings.ToLower(uriStr), "vmess://") {
		b64 := strings.TrimPrefix(uriStr, "vmess://")
		b64 = strings.TrimPrefix(b64, "VMESS://")
		decoded := tryDecodeBase64(b64)
		var vm map[string]any
		if err := json.Unmarshal([]byte(decoded), &vm); err == nil {
			tag := asString(vm["ps"])
			server := asString(vm["add"])
			port := asInt(vm["port"])
			uuid := asString(vm["id"])
			if server == "" || port == 0 || uuid == "" {
				return nil
			}
			if tag == "" {
				tag = server
			}
			ob := map[string]any{
				"type":        "vmess",
				"tag":         tag,
				"name":        tag,
				"server":      server,
				"server_port": port,
				"uuid":        uuid,
				"security":    "auto",
				"alter_id":    asInt(vm["aid"]),
			}
			if asString(vm["tls"]) == "tls" {
				sni := asString(vm["sni"])
				if sni == "" {
					sni = asString(vm["host"])
				}
				if sni == "" {
					sni = server
				}
				ob["tls"] = map[string]any{"enabled": true, "server_name": sni}
			}
			if asString(vm["net"]) == "ws" {
				ob["transport"] = map[string]any{
					"type": "ws",
					"path": asString(vm["path"]),
				}
			}
			return ob
		}
	}

	// 优先处理 ss:// 全格式 (Legacy 与 SIP002)
	if strings.HasPrefix(strings.ToLower(uriStr), "ss://") {
		return parseShadowsocksURI(uriStr)
	}

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

// parseShadowsocksURI 解析标准 shadowsocks 分享链接 (支持 Legacy Base64 与 SIP002 规范)
func parseShadowsocksURI(uriStr string) map[string]any {
	var tag string
	parts := strings.SplitN(uriStr, "#", 2)
	if len(parts) == 2 {
		tag, _ = url.PathUnescape(parts[1])
		if tag == "" {
			tag, _ = url.QueryUnescape(parts[1])
		}
		if tag == "" {
			tag = strings.TrimSpace(parts[1])
		}
	}
	body := parts[0]
	if idx := strings.Index(body, "://"); idx != -1 {
		body = body[idx+3:]
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}

	var method, password, host string
	var port int

	if strings.Contains(body, "@") {
		// SIP002 规范: ss://userinfo@host:port/?plugin=...#tag
		lastAt := strings.LastIndex(body, "@")
		userInfoPart := body[:lastAt]
		hostPart := body[lastAt+1:]

		if slashIdx := strings.IndexAny(hostPart, "/?#"); slashIdx != -1 {
			hostPart = hostPart[:slashIdx]
		}

		h, pStr, err := net.SplitHostPort(hostPart)
		if err != nil {
			return nil
		}
		p, err := strconv.Atoi(pStr)
		if err != nil || p <= 0 || p > 65535 {
			return nil
		}
		host = h
		port = p

		// 使用 PathUnescape 避免将 Base64 中的 '+' 错误转义为空格
		if unescaped, err := url.PathUnescape(userInfoPart); err == nil && unescaped != "" {
			userInfoPart = unescaped
		}

		decodedUser := tryDecodeBase64(userInfoPart)
		userStr := decodedUser
		if !strings.Contains(userStr, ":") {
			userStr = userInfoPart
			if qUnescaped, err := url.QueryUnescape(userStr); err == nil && strings.Contains(qUnescaped, ":") {
				userStr = qUnescaped
			}
		}
		if colonIdx := strings.Index(userStr, ":"); colonIdx != -1 {
			method = userStr[:colonIdx]
			password = userStr[colonIdx+1:]
		} else {
			return nil
		}
	} else {
		// Legacy 格式: ss://BASE64(method:password@host:port)#tag
		// 去除末尾可能拼接的 / 或 ?query 参数后再进行 Base64 解码
		cleanBody := body
		if qIdx := strings.IndexAny(cleanBody, "/?#"); qIdx != -1 {
			cleanBody = cleanBody[:qIdx]
		}
		decoded := tryDecodeBase64(cleanBody)
		if !strings.Contains(decoded, "@") {
			return nil
		}

		if innerParts := strings.SplitN(decoded, "#", 2); len(innerParts) == 2 {
			if tag == "" {
				tag, _ = url.PathUnescape(innerParts[1])
				if tag == "" {
					tag, _ = url.QueryUnescape(innerParts[1])
				}
				if tag == "" {
					tag = strings.TrimSpace(innerParts[1])
				}
			}
			decoded = innerParts[0]
		}

		lastAt := strings.LastIndex(decoded, "@")
		userInfoPart := decoded[:lastAt]
		hostPart := decoded[lastAt+1:]

		if slashIdx := strings.IndexAny(hostPart, "/?#"); slashIdx != -1 {
			hostPart = hostPart[:slashIdx]
		}

		h, pStr, err := net.SplitHostPort(hostPart)
		if err != nil {
			return nil
		}
		p, err := strconv.Atoi(pStr)
		if err != nil || p <= 0 || p > 65535 {
			return nil
		}
		host = h
		port = p

		if colonIdx := strings.Index(userInfoPart, ":"); colonIdx != -1 {
			method = userInfoPart[:colonIdx]
			password = userInfoPart[colonIdx+1:]
		} else {
			return nil
		}
	}

	if method == "" || host == "" || password == "" || port <= 0 || port > 65535 {
		return nil
	}
	if tag == "" {
		tag = host
	}

	return map[string]any{
		"type":        "shadowsocks",
		"tag":         tag,
		"name":        tag,
		"server":      host,
		"server_port": port,
		"method":      method,
		"password":    password,
	}
}

func tryDecodeBase64(s string) string {
	if strings.Contains(s, "://") {
		return s
	}
	cleaned := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	cleaned = strings.ReplaceAll(strings.ReplaceAll(cleaned, "-", "+"), "_", "/")
	if pad := len(cleaned) % 4; pad != 0 {
		cleaned += strings.Repeat("=", 4-pad)
	}
	if b, err := base64.StdEncoding.DecodeString(cleaned); err == nil && len(b) > 0 {
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

func isLoopbackURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func fetchRawBytes(subURL string) ([]byte, http.Header, error) {
	// 1. 优先尝试直连公共 DNS 快速拉取 (自带 0x2024 免死金牌)
	data, header, directErr := doFetch(subURL, nil, 6*time.Second)
	if directErr == nil && len(data) > 0 {
		return data, header, nil
	}

	// 若为本地环回地址，不应经过 7891 代理，直接返回直连错误
	if isLoopbackURL(subURL) {
		return nil, nil, fmt.Errorf("本地环回拉取失败: %v", directErr)
	}

	// 2. 若直连失败，探测本地 7891 代理端口是否存活，若存活则自动通过代理拉取（解决海外机场被阻断）
	proxyURL, pErr := url.Parse("http://127.0.0.1:7891")
	if pErr == nil {
		conn, cErr := net.DialTimeout("tcp", "127.0.0.1:7891", 300*time.Millisecond)
		if cErr == nil {
			_ = conn.Close()
			if proxyData, proxyHeader, proxyErr := doFetch(subURL, http.ProxyURL(proxyURL), 15*time.Second); proxyErr == nil && len(proxyData) > 0 {
				return proxyData, proxyHeader, nil
			}
		}
	}

	return nil, nil, fmt.Errorf("直连拉取失败: %v (若为海外机场请先启用已有节点并热部署后再试)", directErr)
}

func doFetch(subURL string, proxyFunc func(*http.Request) (*url.URL, error), timeout time.Duration) ([]byte, http.Header, error) {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				setSocketMark(fd, 0x2024)
			})
		},
	}

	// 直连时使用带有 SO_MARK=0x2024 的直连 DNS 解析器（仅当 host 为非 IP 域名时启用，避免直接 IP 地址被外部 DNS 阻塞）
	u, _ := url.Parse(subURL)
	isPlainIP := u != nil && (net.ParseIP(u.Hostname()) != nil || isLoopbackURL(subURL))

	if proxyFunc == nil && !isPlainIP {
		dialer.Resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{
					Timeout: 3 * time.Second,
					Control: func(network, address string, c syscall.RawConn) error {
						return c.Control(func(fd uintptr) {
							setSocketMark(fd, 0x2024)
						})
					},
				}
				return d.DialContext(ctx, "udp", "223.5.5.5:53")
			},
		}
	}

	tr := &http.Transport{
		Proxy:           proxyFunc,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext:     dialer.DialContext,
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   timeout + 5*time.Second,
	}
	req, err := http.NewRequest("GET", subURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Clash.Meta/v1.18.0 Mihomo/1.18 sing-box/1.15.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("订阅返回 HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	return body, resp.Header, err
}