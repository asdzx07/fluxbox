package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"fluxbox/internal/configgen"
	"fluxbox/internal/flip"
	"fluxbox/internal/store"
)

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st := s.store.GetSettings()
		var availableNodes []string
		for _, n := range st.Nodes {
			if tag, ok := n["tag"].(string); ok && tag != "" {
				availableNodes = append(availableNodes, tag)
			}
		}
		s.writeJSON(w, map[string]any{
			"groups":         st.Groups,
			"availableNodes": availableNodes,
		})

	case http.MethodPut:
		var req struct {
			Groups []configgen.GroupConfig `json:"groups"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "无效的 groups 数据")
			return
		}
		_ = s.store.MutateSettings(func(st *store.FullSettings) {
			st.Groups = req.Groups
		})
		_ = s.DeployCore(true)
		s.writeJSON(w, map[string]any{"ok": true, "groups": req.Groups})
	}
}

func (s *Server) handlePolicies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st := s.store.GetSettings()
		var targets []map[string]string
		targets = append(targets,
			map[string]string{"value": "builtin-direct", "label": "直连"},
			map[string]string{"value": "builtin-block", "label": "拒绝"},
		)
		for _, g := range st.Groups {
			if g.Enabled {
				targets = append(targets, map[string]string{"value": g.ID, "label": g.Name})
			}
		}

		var pols []map[string]any
		for _, p := range st.Policies {
			pols = append(pols, map[string]any{
				"id":           p.ID,
				"name":         p.Name,
				"target":       p.Target,
				"rulesets":     p.Rulesets,
				"domainSuffix": p.DomainSuffix,
				"enabled":      p.Enabled,
				"flipTag":      "obflip-" + p.ID,
			})
		}
		s.writeJSON(w, map[string]any{
			"policies": pols,
			"targets":  targets,
		})

	case http.MethodPut:
		var req struct {
			Policies []configgen.PolicyConfig `json:"policies"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "无效的 policies 数据")
			return
		}

		oldPolicies := s.store.GetSettings().Policies
		onlyToggle := isOnlyToggleChange(oldPolicies, req.Policies)

		_ = s.store.MutateSettings(func(st *store.FullSettings) {
			st.Policies = req.Policies
		})

		flipDir := filepath.Join(s.store.GetDataDir(), "flip")
		for _, p := range req.Policies {
			_ = flip.WritePolicyFlip(flipDir, p.ID, p.Enabled)
		}

		if onlyToggle {
			// 仅开关变动：通过 Flip 文件秒级热重载，无需重启 sing-box 内核，0 毫秒断流！
			s.writeJSON(w, map[string]any{"ok": true, "hotReloaded": true, "policies": req.Policies})
			return
		}

		// 结构发生变动（添加规则/修改域名/改变出口）：触发完整部署
		_ = s.DeployCore(true)
		s.writeJSON(w, map[string]any{"ok": true, "deployed": true, "policies": req.Policies})
	}
}

func (s *Server) handlePolicyReset(w http.ResponseWriter, r *http.Request) {
	next := store.DefaultPolicies()
	_ = s.store.MutateSettings(func(st *store.FullSettings) {
		st.Policies = next
	})

	flipDir := filepath.Join(s.store.GetDataDir(), "flip")
	for _, p := range next {
		_ = flip.WritePolicyFlip(flipDir, p.ID, p.Enabled)
	}

	_ = s.DeployCore(true)
	s.writeJSON(w, map[string]any{"ok": true, "policies": next})
}

func (s *Server) handlePolicyToggle(w http.ResponseWriter, r *http.Request) {
	// /api/policies/:id/toggle 或 /api/policies/:id
	path := strings.TrimPrefix(r.URL.Path, "/api/policies/")
	parts := strings.Split(path, "/")
	id := parts[0]

	var req struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	_ = s.store.MutateSettings(func(st *store.FullSettings) {
		for i := range st.Policies {
			if st.Policies[i].ID == id {
				st.Policies[i].Enabled = req.Enabled
				break
			}
		}
	})

	flipDir := filepath.Join(s.store.GetDataDir(), "flip")
	_ = flip.WritePolicyFlip(flipDir, id, req.Enabled)

	s.writeJSON(w, map[string]any{"ok": true, "id": id, "enabled": req.Enabled})
}

func isOnlyToggleChange(oldList, newList []configgen.PolicyConfig) bool {
	if len(oldList) != len(newList) {
		return false
	}
	for i := range oldList {
		a := oldList[i]
		b := newList[i]
		if a.ID != b.ID || a.Name != b.Name || a.Target != b.Target {
			return false
		}
		if len(a.Rulesets) != len(b.Rulesets) || len(a.DomainSuffix) != len(b.DomainSuffix) {
			return false
		}
	}
	return true
}
