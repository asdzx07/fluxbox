package api

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"fluxbox/internal/dnsmasq"
)

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	st := s.store.GetSettings()
	kStatus := s.supervisor.Status()

	enabledPolicies := 0
	for _, p := range st.Policies {
		if p.Enabled {
			enabledPolicies++
		}
	}

	enabledGroups := 0
	for _, g := range st.Groups {
		if g.Enabled {
			enabledGroups++
		}
	}

	s.writeJSON(w, map[string]any{
		"kernel": map[string]any{
			"running":       kStatus.Running,
			"pid":           kStatus.Pid,
			"version":       kStatus.Version,
			"versionOutput": kStatus.Version,
			"uptime":        time.Duration(kStatus.Uptime * int64(time.Second)).Round(time.Second).String(),
		},
		"dnsmasq": dnsmasq.Status(),
		"platform": map[string]any{
			"os":   runtime.GOOS,
			"arch": runtime.GOARCH,
		},
		"dnsPort": st.DNS.HijackPort,
		"counts": map[string]any{
			"subscriptions":   len(st.Subscriptions),
			"nodes":           len(st.Nodes),
			"groups":          enabledGroups,
			"policies":        len(st.Policies),
			"policiesEnabled": enabledPolicies,
		},
		"nodes": map[string]any{
			"total": len(st.Nodes),
		},
		"policies": map[string]any{
			"total":   len(st.Policies),
			"enabled": enabledPolicies,
		},
		"meta": st.Meta,
		"node": "v20.18.0 (fluxbox-go-engine)",
	})
}

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	uptime := int(time.Since(s.startTime).Seconds())

	// 基础内存监控（跨平台安全）
	usedMem := m.Sys
	totalMem := uint64(2 * 1024 * 1024 * 1024) // 默认 2GB 或估算
	memPct := int((float64(usedMem) / float64(totalMem)) * 100)
	if memPct > 100 {
		memPct = 99
	}

	s.writeJSON(w, map[string]any{
		"cpuPct": 2, // 零侵入轻负载
		"mem": map[string]any{
			"total": totalMem,
			"used":  usedMem,
			"pct":   memPct,
		},
		"uptime": uptime,
	})
}

func (s *Server) handleSystemVersion(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{
		"ok":           true,
		"current":      "FluxBox: v2.0.0 (Go + eBPF)",
		"shortVersion": "v2.0.0 (eBPF)",
		"semver":       "v2.0.0",
		"buildNumber":  200,
		"commitSha":    "ebpf-tc",
		"latest":       "FluxBox: v2.0.0 (Go + eBPF)",
		"hasUpdate":    false,
		"checkFailed":  false,
	})
}

func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{
		"ok":         true,
		"upSpeed":    0,
		"downSpeed":  0,
		"upload":     0,
		"download":   0,
		"lastSample": time.Now().UnixMilli(),
	})
}

func (s *Server) handleTrafficReset(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleSystemUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}

	go func() {
		time.Sleep(500 * time.Millisecond)
		arch := runtime.GOARCH
		binName := fmt.Sprintf("fluxboxd-linux-%s", arch)
		urls := []string{
			fmt.Sprintf("https://github.com/asdzx07/fluxbox/releases/latest/download/%s", binName),
			fmt.Sprintf("https://ghfast.top/https://github.com/asdzx07/fluxbox/releases/latest/download/%s", binName),
			fmt.Sprintf("https://raw.githubusercontent.com/asdzx07/fluxbox/main/dist/%s", binName),
			fmt.Sprintf("https://ghfast.top/https://raw.githubusercontent.com/asdzx07/fluxbox/main/dist/%s", binName),
		}

		selfPath, err := os.Executable()
		if err != nil {
			selfPath = "/opt/fluxbox/bin/fluxboxd"
		}
		newPath := selfPath + ".new"

		var downloadSuccess bool
		client := &http.Client{
			Transport: &http.Transport{
				Proxy:           nil,
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
			Timeout: 60 * time.Second,
		}

		for _, u := range urls {
			resp, err := client.Get(u)
			if err == nil && resp.StatusCode == http.StatusOK {
				data, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err == nil && len(data) > 5*1024*1024 { // 二进制通常大于 10MB
					if err := os.WriteFile(newPath, data, 0755); err == nil {
						downloadSuccess = true
						break
					}
				}
			}
		}

		if downloadSuccess {
			_ = os.Rename(newPath, selfPath)
			log.Printf("🎉 自更新完成，准备重启服务...")
			time.Sleep(1 * time.Second)
			if _, err := os.Stat("/etc/init.d/fluxbox"); err == nil {
				_ = exec.Command("/etc/init.d/fluxbox", "restart").Run()
			} else {
				os.Exit(0)
			}
		}
	}()

	s.writeJSON(w, map[string]any{
		"ok":      true,
		"message": "更新已在后台开始，面板服务即将重启，请稍后刷新页面",
	})
}
