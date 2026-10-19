package api

import (
	"encoding/json"
	"net/http"

	"fluxbox/internal/store"
)

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		var incoming map[string]any
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			s.writeError(w, http.StatusBadRequest, "无效的 JSON 数据")
			return
		}

		_ = s.store.MutateSettings(func(st *store.FullSettings) {
			if netMap, ok := incoming["network"].(map[string]any); ok {
				if v, ok := netMap["ipv6"].(bool); ok {
					st.Network.IPv6 = v
				}
				if v, ok := netMap["rejectQuic"].(bool); ok {
					st.Network.RejectQuic = v
				}
				if v, ok := netMap["directBypass"].(bool); ok {
					st.Network.DirectBypass = v
				}
				if v, ok := netMap["directForNodes"].(bool); ok {
					st.Network.DirectForNodes = v
				}
				if v, ok := netMap["sniff"].(bool); ok {
					st.Network.Sniff = v
				}
				if tunMap, ok := netMap["tun"].(map[string]any); ok {
					if v, ok := tunMap["autoRedirect"].(bool); ok {
						st.Network.Tun.AutoRedirect = v
					}
					if v, ok := tunMap["aggressive"].(bool); ok {
						st.Network.Tun.Aggressive = v
					}
					if v, ok := tunMap["strictRoute"].(bool); ok {
						st.Network.Tun.StrictRoute = v
					}
				}
			}

			if dnsMap, ok := incoming["dns"].(map[string]any); ok {
				if v, ok := dnsMap["mode"].(string); ok {
					st.DNS.Mode = v
				}
				if v, ok := dnsMap["directAddress"].(string); ok {
					st.DNS.DirectAddress = v
				}
				if v, ok := dnsMap["proxy"].(string); ok {
					st.DNS.Proxy = v
				}
				if v, ok := dnsMap["proxyProtocol"].(string); ok {
					st.DNS.ProxyProtocol = v
				}
				if v, ok := dnsMap["strategy"].(string); ok {
					st.DNS.Strategy = v
				}
				if v, ok := dnsMap["split"].(bool); ok {
					st.DNS.Split = v
				}
				if v, ok := dnsMap["fakeIp"].(bool); ok {
					st.DNS.FakeIP = v
				}
				if v, ok := dnsMap["adblock"].(bool); ok {
					st.DNS.Adblock = v
				}
			}

			if kMap, ok := incoming["kernel"].(map[string]any); ok {
				if v, ok := kMap["logLevel"].(string); ok {
					st.Kernel.LogLevel = v
				}
				if v, ok := kMap["autoStart"].(bool); ok {
					st.Kernel.AutoStart = v
				}
			}
		})

		// 实时将内核 flags 推送到 eBPF
		if s.bpfEngine != nil {
			cur := s.store.GetSettings()
			_ = s.bpfEngine.SetGlobalFlags(cur.Network.DirectBypass, cur.Network.RejectQuic)
		}

		// 触发配置热重载使 strict_route 等新设置即刻生效
		go func() {
			_ = s.DeployCore(true)
		}()

		s.writeJSON(w, s.sanitizeSettings(s.store.GetSettings()))
		return

	}

	s.writeJSON(w, s.sanitizeSettings(s.store.GetSettings()))
}

func (s *Server) sanitizeSettings(st store.FullSettings) map[string]any {
	return map[string]any{
		"panel": map[string]any{
			"passwordSet": st.Panel.PasswordHash != "",
		},
		"kernel":   st.Kernel,
		"network":  st.Network,
		"dns":      st.DNS,
		"meta":     st.Meta,
		"groups":   st.Groups,
		"policies": st.Policies,
	}
}
