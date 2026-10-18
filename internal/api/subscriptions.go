package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"fluxbox/internal/store"
	"fluxbox/internal/subscription"
)

func newID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

func (s *Server) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st := s.store.GetSettings()
		var resList []map[string]any
		for _, sub := range st.Subscriptions {
			var nodeTags []string
			var sampleNodes []string
			for _, n := range st.Nodes {
				if subID, ok := n["__subscriptionId"].(string); ok && subID == sub.ID {
					if tag, ok := n["tag"].(string); ok && tag != "" {
						nodeTags = append(nodeTags, tag)
					}
				}
			}
			if len(nodeTags) > 4 {
				sampleNodes = nodeTags[:4]
			} else {
				sampleNodes = nodeTags
			}

			resList = append(resList, map[string]any{
				"id":          sub.ID,
				"name":        sub.Name,
				"url":         sub.URL,
				"enabled":     sub.Enabled,
				"nodeCount":   len(nodeTags),
				"nodeTags":    nodeTags,
				"sampleNodes": sampleNodes,
				"updatedAt":   formatTimeVal(sub.UpdatedAt),
			})
		}
		s.writeJSON(w, map[string]any{
			"subscriptions": resList,
			"totalNodes":    len(st.Nodes),
		})

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
			s.writeError(w, http.StatusBadRequest, "请填写有效的 http(s) 订阅地址")
			return
		}

		subID := newID("sub")
		name := req.Name
		if name == "" {
			name = fmt.Sprintf("订阅 %d", len(s.store.GetSettings().Subscriptions)+1)
		}

		parsedNodes, fetchErr := subscription.FetchAndParse(req.URL)
		if fetchErr != nil {
			s.writeError(w, http.StatusBadGateway, fmt.Sprintf("拉取订阅失败: %v", fetchErr))
			return
		}

		for _, n := range parsedNodes {
			n["__subscriptionId"] = subID
		}

		nowStr := time.Now().Format("2006-01-02 15:04:05")
		newSub := subscription.Subscription{
			ID:        subID,
			Name:      name,
			URL:       req.URL,
			Enabled:   true,
			NodeCount: len(parsedNodes),
			UpdatedAt: nowStr,
		}

		_ = s.store.MutateSettings(func(st *store.FullSettings) {
			st.Subscriptions = append(st.Subscriptions, newSub)
			st.Nodes = append(st.Nodes, parsedNodes...)
		})

		_ = s.DeployCore(true)
		s.writeJSON(w, map[string]any{
			"id":        subID,
			"nodeCount": len(parsedNodes),
			"ok":        true,
		})
	}
}

func (s *Server) handleSubscriptionAction(w http.ResponseWriter, r *http.Request) {
	// 提取 /api/subscriptions/:id
	path := strings.TrimPrefix(r.URL.Path, "/api/subscriptions/")
	parts := strings.Split(path, "/")
	id := parts[0]

	if len(parts) == 2 && parts[1] == "refresh" && r.Method == http.MethodPost {
		s.handleSubscriptionRefresh(w, r, id)
		return
	}

	switch r.Method {
	case http.MethodPut:
		var req struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		if id == "all" {
			_ = s.store.MutateSettings(func(st *store.FullSettings) {
				for i := range st.Subscriptions {
					st.Subscriptions[i].Enabled = req.Enabled
				}
			})
		} else {
			_ = s.store.MutateSettings(func(st *store.FullSettings) {
				for i := range st.Subscriptions {
					if st.Subscriptions[i].ID == id {
						st.Subscriptions[i].Enabled = req.Enabled
						break
					}
				}
			})
		}
		_ = s.DeployCore(true)
		s.writeJSON(w, map[string]any{"ok": true, "id": id, "enabled": req.Enabled})

	case http.MethodDelete:
		_ = s.store.MutateSettings(func(st *store.FullSettings) {
			var keptSubs []subscription.Subscription
			for _, sub := range st.Subscriptions {
				if sub.ID != id {
					keptSubs = append(keptSubs, sub)
				}
			}
			st.Subscriptions = keptSubs

			var keptNodes []map[string]any
			for _, n := range st.Nodes {
				if subID, ok := n["__subscriptionId"].(string); !ok || subID != id {
					keptNodes = append(keptNodes, n)
				}
			}
			st.Nodes = keptNodes
		})
		_ = s.DeployCore(true)
		s.writeJSON(w, map[string]any{"ok": true})
	}
}

func (s *Server) handleSubscriptionRefresh(w http.ResponseWriter, r *http.Request, id string) {
	st := s.store.GetSettings()
	var targetSub *subscription.Subscription
	for _, sub := range st.Subscriptions {
		if sub.ID == id {
			c := sub
			targetSub = &c
			break
		}
	}
	if targetSub == nil {
		s.writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}

	parsedNodes, err := subscription.FetchAndParse(targetSub.URL)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, fmt.Sprintf("拉取更新失败: %v", err))
		return
	}

	for _, n := range parsedNodes {
		n["__subscriptionId"] = id
	}

	nowStr := time.Now().Format("2006-01-02 15:04:05")
	_ = s.store.MutateSettings(func(st *store.FullSettings) {
		for i := range st.Subscriptions {
			if st.Subscriptions[i].ID == id {
				st.Subscriptions[i].UpdatedAt = nowStr
				st.Subscriptions[i].NodeCount = len(parsedNodes)
				break
			}
		}
		var keptNodes []map[string]any
		for _, n := range st.Nodes {
			if subID, ok := n["__subscriptionId"].(string); !ok || subID != id {
				keptNodes = append(keptNodes, n)
			}
		}
		st.Nodes = append(keptNodes, parsedNodes...)
	})

	_ = s.DeployCore(true)
	s.writeJSON(w, map[string]any{
		"ok":        true,
		"id":        id,
		"nodeCount": len(parsedNodes),
		"updatedAt": nowStr,
	})
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	st := s.store.GetSettings()
	subMap := make(map[string]subscription.Subscription)
	for _, sub := range st.Subscriptions {
		subMap[sub.ID] = sub
	}

	var out []map[string]any
	for _, n := range st.Nodes {
		item := make(map[string]any)
		for k, v := range n {
			item[k] = v
		}
		subID, _ := n["__subscriptionId"].(string)
		sub := subMap[subID]
		item["subscriptionId"] = subID
		item["subscriptionName"] = sub.Name
		item["subscriptionEnabled"] = sub.Enabled
		out = append(out, item)
	}

	s.writeJSON(w, map[string]any{"nodes": out})
}

func formatTimeVal(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val > 1e11 {
			return time.UnixMilli(int64(val)).Format("2006-01-02 15:04:05")
		}
		return time.Unix(int64(val), 0).Format("2006-01-02 15:04:05")
	case int64:
		if val > 1e11 {
			return time.UnixMilli(val).Format("2006-01-02 15:04:05")
		}
		return time.Unix(val, 0).Format("2006-01-02 15:04:05")
	default:
		return fmt.Sprintf("%v", val)
	}
}
