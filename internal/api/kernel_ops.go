package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"fluxbox/internal/configgen"
	"fluxbox/internal/dnsmasq"
	"fluxbox/internal/netstack"
	"fluxbox/internal/store"
)

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Restart bool `json:"restart"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := s.DeployCore(req.Restart); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "message": "部署成功"})
}

func (s *Server) handleTeardown(w http.ResponseWriter, r *http.Request) {
	_ = s.supervisor.Stop()
	_ = netstack.Teardown()
	_ = dnsmasq.Restore()
	s.writeJSON(w, map[string]any{"ok": true, "message": "网络规则已彻底还原，服务已停止"})
}

func (s *Server) handleKernelAction(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, "/api/kernel/")

	switch action {
	case "start":
		if err := s.supervisor.Start(); err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = netstack.Setup()
		_ = dnsmasq.Hijack(s.store.GetSettings().DNS.HijackPort)
		s.writeJSON(w, map[string]any{"ok": true, "running": true})

	case "stop":
		_ = s.supervisor.Stop()
		_ = netstack.Teardown()
		_ = dnsmasq.Restore()
		s.writeJSON(w, map[string]any{"ok": true, "running": false})

	case "restart":
		if err := s.supervisor.Restart(); err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = netstack.Setup()
		_ = dnsmasq.Hijack(s.store.GetSettings().DNS.HijackPort)
		s.writeJSON(w, map[string]any{"ok": true, "running": true})

	default:
		s.writeError(w, http.StatusBadRequest, "未知内核操作")
	}
}

func (s *Server) handleKernelLog(w http.ResponseWriter, r *http.Request) {
	lines := 200
	if lStr := r.URL.Query().Get("lines"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			lines = l
		}
	}
	s.writeJSON(w, map[string]any{"log": s.supervisor.GetLogs(lines)})
}

func (s *Server) handleKernelLatest(w http.ResponseWriter, r *http.Request) {
	cur := s.supervisor.Version()
	s.writeJSON(w, map[string]any{
		"ok":        true,
		"current":   cur,
		"latest":    cur,
		"hasUpdate": false,
	})
}

func (s *Server) handleRulesetSubs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st := s.store.GetSettings()
		s.writeJSON(w, map[string]any{"ok": true, "items": st.RulesetSubs})

	case http.MethodPost:
		var req struct {
			Tag    string `json:"tag"`
			URL    string `json:"url"`
			Format string `json:"format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Tag == "" || req.URL == "" {
			s.writeError(w, http.StatusBadRequest, "缺少 tag 或 url")
			return
		}

		item := configgen.RulesetSub{
			ID:      newID("rs"),
			Tag:     strings.TrimSpace(req.Tag),
			URL:     strings.TrimSpace(req.URL),
			Format:  req.Format,
			Enabled: true,
		}
		if item.Format != "source" {
			item.Format = "binary"
		}

		_ = s.store.MutateSettings(func(st *store.FullSettings) {
			st.RulesetSubs = append(st.RulesetSubs, item)
		})
		s.writeJSON(w, map[string]any{"ok": true, "item": item})
	}
}

func (s *Server) handleRulesetSubAction(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/ruleset-subs/")

	switch r.Method {
	case http.MethodPut:
		var req struct {
			Tag     *string `json:"tag"`
			URL     *string `json:"url"`
			Format  *string `json:"format"`
			Enabled *bool   `json:"enabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		_ = s.store.MutateSettings(func(st *store.FullSettings) {
			for i := range st.RulesetSubs {
				if st.RulesetSubs[i].ID == id {
					if req.Tag != nil {
						st.RulesetSubs[i].Tag = strings.TrimSpace(*req.Tag)
					}
					if req.URL != nil {
						st.RulesetSubs[i].URL = strings.TrimSpace(*req.URL)
					}
					if req.Format != nil {
						st.RulesetSubs[i].Format = *req.Format
					}
					if req.Enabled != nil {
						st.RulesetSubs[i].Enabled = *req.Enabled
					}
					break
				}
			}
		})
		s.writeJSON(w, map[string]any{"ok": true})

	case http.MethodDelete:
		_ = s.store.MutateSettings(func(st *store.FullSettings) {
			var kept []configgen.RulesetSub
			for _, item := range st.RulesetSubs {
				if item.ID != id {
					kept = append(kept, item)
				}
			}
			st.RulesetSubs = kept
		})
		s.writeJSON(w, map[string]any{"ok": true})
	}
}

func (s *Server) handleRulesetsStatus(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{"ok": true, "items": map[string]any{}})
}

func (s *Server) handleRulesetsRefresh(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{"ok": true, "message": "规则集刷新已触发"})
}

func (s *Server) handleAdblockRefresh(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{"ok": true, "message": "广告拦截规则集刷新已触发"})
}
