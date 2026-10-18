package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ClashAPIURL = "http://127.0.0.1:9095"

func (s *Server) clashRequest(method, path string, body []byte) ([]byte, int, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(method, ClashAPIURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	return respBytes, resp.StatusCode, err
}

func (s *Server) handleNodesStatus(w http.ResponseWriter, r *http.Request) {
	raw, _, err := s.clashRequest(http.MethodGet, "/proxies", nil)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("读取内核节点状态失败: %v", err))
		return
	}

	var data struct {
		Proxies map[string]map[string]any `json:"proxies"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		s.writeError(w, http.StatusBadGateway, "解析内核响应失败")
		return
	}

	var groups []map[string]any
	var nodes []map[string]any
	groupTypes := map[string]bool{
		"Selector": true, "URLTest": true, "Fallback": true, "LoadBalance": true,
	}

	for name, p := range data.Proxies {
		if name == "GLOBAL" {
			continue
		}
		pType, _ := p["type"].(string)
		if groupTypes[pType] {
			groups = append(groups, map[string]any{
				"name":    name,
				"type":    pType,
				"now":     p["now"],
				"members": p["all"],
			})
		} else if pType != "Direct" && pType != "Reject" && pType != "Compatible" {
			nodes = append(nodes, map[string]any{
				"name": name,
				"type": pType,
				"udp":  p["udp"] != false,
			})
		}
	}

	sort.Slice(groups, func(i, j int) bool {
		tI, _ := groups[i]["type"].(string)
		tJ, _ := groups[j]["type"].(string)
		if (tI == "Selector") != (tJ == "Selector") {
			return tI == "Selector"
		}
		return groups[i]["name"].(string) < groups[j]["name"].(string)
	})

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i]["name"].(string) < nodes[j]["name"].(string)
	})

	s.writeJSON(w, map[string]any{
		"groups": groups,
		"nodes":  nodes,
	})
}

func (s *Server) handleNodesSelect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Group string `json:"group"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Group == "" || req.Name == "" {
		s.writeError(w, http.StatusBadRequest, "缺少 group 或 name")
		return
	}

	body, _ := json.Marshal(map[string]string{"name": req.Name})
	_, code, err := s.clashRequest(http.MethodPut, "/proxies/"+url.PathEscape(req.Group), body)
	if err != nil || code >= 400 {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("切换失败 (code: %d)", code))
		return
	}

	// 切换节点后主动关闭已有连接，防止浏览器长连接复用旧节点
	_, _, _ = s.clashRequest(http.MethodDelete, "/connections", nil)

	s.writeJSON(w, map[string]any{
		"ok":    true,
		"group": req.Group,
		"name":  req.Name,
	})
}

func (s *Server) handleNodesLatency(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		s.writeError(w, http.StatusBadRequest, "缺少 name")
		return
	}
	testURL := r.URL.Query().Get("url")
	if testURL == "" {
		testURL = "http://www.gstatic.com/generate_204"
	}
	timeout := 2800
	if tStr := r.URL.Query().Get("timeout"); tStr != "" {
		if t, err := strconv.Atoi(tStr); err == nil && t > 0 {
			timeout = t
		}
	}

	path := fmt.Sprintf("/proxies/%s/delay?url=%s&timeout=%d", url.PathEscape(name), url.QueryEscape(testURL), timeout)
	raw, _, err := s.clashRequest(http.MethodGet, path, nil)
	if err != nil {
		s.writeJSON(w, map[string]any{"name": name, "delay": nil, "error": err.Error()})
		return
	}

	var res struct {
		Delay int `json:"delay"`
	}
	if err := json.Unmarshal(raw, &res); err == nil && res.Delay > 0 {
		s.writeJSON(w, map[string]any{"name": name, "delay": res.Delay, "error": nil})
	} else {
		s.writeJSON(w, map[string]any{"name": name, "delay": nil, "error": "timeout or unreachable"})
	}
}

func (s *Server) handleNodesLatencyBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names   []string `json:"names"`
		URL     string   `json:"url"`
		Timeout int      `json:"timeout"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Timeout <= 0 {
		req.Timeout = 2800
	}
	if req.URL == "" {
		req.URL = "http://www.gstatic.com/generate_204"
	}

	results := make(map[string]map[string]any)
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, 10) // 最多 10 并发
	for _, name := range req.Names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			path := fmt.Sprintf("/proxies/%s/delay?url=%s&timeout=%d", url.PathEscape(n), url.QueryEscape(req.URL), req.Timeout)
			raw, _, err := s.clashRequest(http.MethodGet, path, nil)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[n] = map[string]any{"delay": nil, "error": err.Error()}
				return
			}
			var res struct {
				Delay int `json:"delay"`
			}
			if err := json.Unmarshal(raw, &res); err == nil && res.Delay > 0 {
				results[n] = map[string]any{"delay": res.Delay, "error": nil}
			} else {
				results[n] = map[string]any{"delay": nil, "error": "timeout or unreachable"}
			}
		}(name)
	}
	wg.Wait()

	s.writeJSON(w, map[string]any{"results": results})
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		raw, _, err := s.clashRequest(http.MethodGet, "/connections", nil)
		if err != nil {
			s.writeJSON(w, map[string]any{
				"ok":            false,
				"connections":   []any{},
				"uploadTotal":   0,
				"downloadTotal": 0,
				"error":         err.Error(),
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)

	case http.MethodDelete:
		_, _, _ = s.clashRequest(http.MethodDelete, "/connections", nil)
		s.writeJSON(w, map[string]any{"ok": true})
	}
}

func (s *Server) handleConnectionDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/connections/")
	_, _, _ = s.clashRequest(http.MethodDelete, "/connections/"+url.PathEscape(id), nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleControllerProxy() http.Handler {
	target, _ := url.Parse(ClashAPIURL)
	proxy := httputil.NewSingleHostReverseProxy(target)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/controller")
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		proxy.ServeHTTP(w, r)
	})
}
