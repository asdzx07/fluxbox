package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
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

		// 收集未显式关联订阅 ID 的节点
		var orphanNodes []string
		for _, n := range st.Nodes {
			subID, _ := n["__subscriptionId"].(string)
			if subID == "" {
				subID, _ = n["subscriptionId"].(string)
			}
			if subID == "" {
				subID, _ = n["subscription_id"].(string)
			}
			if subID == "" {
				subID, _ = n["subId"].(string)
			}
			if subID == "" {
				if tag, ok := n["tag"].(string); ok && tag != "" {
					orphanNodes = append(orphanNodes, tag)
				}
			}
		}

		for idx, sub := range st.Subscriptions {
			var nodeTags []string
			for _, n := range st.Nodes {
				subID, _ := n["__subscriptionId"].(string)
				if subID == "" {
					subID, _ = n["subscriptionId"].(string)
				}
				if subID == "" {
					subID, _ = n["subscription_id"].(string)
				}
				if subID == "" {
					subID, _ = n["subId"].(string)
				}
				if subID == sub.ID {
					if tag, ok := n["tag"].(string); ok && tag != "" {
						nodeTags = append(nodeTags, tag)
					}
				}
			}
			// 容错：如果该订阅未匹配到节点，但有旧项目迁移过来的孤儿节点，自动挂载
			if len(nodeTags) == 0 && len(orphanNodes) > 0 && (len(st.Subscriptions) == 1 || sub.Enabled || idx == 0) {
				nodeTags = orphanNodes
				orphanNodes = nil
			}
			nCount := len(nodeTags)
			if nCount == 0 && sub.NodeCount > 0 {
				nCount = sub.NodeCount
			}
			if len(nodeTags) == 0 && len(sub.NodeTags) > 0 {
				nodeTags = sub.NodeTags
				nCount = len(nodeTags)
			}

			var sampleNodes []string
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
				"nodeCount":   nCount,
				"nodes":       nodeTags, // 兼容前端 app.js s.nodes?.length
				"nodeTags":    nodeTags,
				"sampleNodes": sampleNodes,
				"updatedAt":   formatTimeVal(sub.UpdatedAt),
				"userinfo":    sub.Userinfo,
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

		parsedNodes, userinfo, fetchErr := subscription.FetchAndParseWithInfo(req.URL)
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
			Userinfo:  userinfo,
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
			"userinfo":  userinfo,
		})
	}
}

func (s *Server) handleSubscriptionAction(w http.ResponseWriter, r *http.Request) {
	// 提取 /api/subscriptions/:id
	path := strings.TrimPrefix(r.URL.Path, "/api/subscriptions/")
	parts := strings.Split(path, "/")
	id := parts[0]

	if len(parts) == 2 && parts[1] == "refresh" {
		if r.Method != http.MethodPost {
			s.writeError(w, http.StatusMethodNotAllowed, "仅支持 POST 请求")
			return
		}
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

	default:
		s.writeError(w, http.StatusMethodNotAllowed, "仅支持 PUT 或 DELETE 请求")
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

	parsedNodes, userinfo, err := subscription.FetchAndParseWithInfo(targetSub.URL)
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
				if userinfo != nil {
					st.Subscriptions[i].Userinfo = userinfo
				}
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
		"userinfo":  userinfo,
	})
}

// SyncSubscriptions 刷新全部已启用的订阅节点与 Userinfo 配额，并自动热重载内核
func (s *Server) SyncSubscriptions() (int, error) {
	st := s.store.GetSettings()
	if len(st.Subscriptions) == 0 {
		return 0, nil
	}

	updatedCount := 0
	nowStr := time.Now().Format("2006-01-02 15:04:05")

	for _, sub := range st.Subscriptions {
		if !sub.Enabled || sub.URL == "" {
			continue
		}

		parsedNodes, userinfo, err := subscription.FetchAndParseWithInfo(sub.URL)
		if err != nil {
			log.Printf("⚠️  [订阅同步] 刷新订阅 %s (%s) 失败: %v", sub.Name, sub.URL, err)
			continue
		}

		for _, n := range parsedNodes {
			n["__subscriptionId"] = sub.ID
		}

		_ = s.store.MutateSettings(func(settings *store.FullSettings) {
			subFound := false
			for i := range settings.Subscriptions {
				if settings.Subscriptions[i].ID == sub.ID {
					settings.Subscriptions[i].UpdatedAt = nowStr
					settings.Subscriptions[i].NodeCount = len(parsedNodes)
					if userinfo != nil {
						settings.Subscriptions[i].Userinfo = userinfo
					}
					subFound = true
					break
				}
			}
			if !subFound {
				return
			}
			var keptNodes []map[string]any
			for _, n := range settings.Nodes {
				if subID, ok := n["__subscriptionId"].(string); !ok || subID != sub.ID {
					keptNodes = append(keptNodes, n)
				}
			}
			settings.Nodes = append(keptNodes, parsedNodes...)
		})

		updatedCount++
	}

	if updatedCount > 0 {
		_ = s.DeployCore(true)
	}

	return updatedCount, nil
}

func (s *Server) handleSubscriptionsSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "仅支持 POST 请求")
		return
	}
	count, err := s.SyncSubscriptions()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("批量同步失败: %v", err))
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "updated": count})
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
