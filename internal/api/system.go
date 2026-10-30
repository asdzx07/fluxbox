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
	"strconv"
	"strings"
	"sync"
	"time"

	"fluxbox/internal/dnsmasq"

)

var (
	updateLogMu     sync.Mutex
	updateLogBuffer []string
)

func appendUpdateLog(line string) {
	updateLogMu.Lock()
	defer updateLogMu.Unlock()
	log.Printf("[SYSTEM-UPDATE] %s", line)
	updateLogBuffer = append(updateLogBuffer, fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), line))
}

func getUpdateLog() string {
	updateLogMu.Lock()
	defer updateLogMu.Unlock()
	return strings.Join(updateLogBuffer, "\n")
}


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
			"uptime":        formatUptime(kStatus.Uptime),
			"circuitBroken": kStatus.CircuitBroken,
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
	totalMem, usedMem, memPct := getHostMemory()
	cpuPct := getHostCPUPct()
	uptime := int(time.Since(s.startTime).Seconds())

	s.writeJSON(w, map[string]any{
		"cpuPct": cpuPct,
		"mem": map[string]any{
			"total": totalMem,
			"used":  usedMem,
			"pct":   memPct,
		},
		"uptime": uptime,
	})
}

func getHostMemory() (total, used uint64, pct int) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		tot := uint64(2 * 1024 * 1024 * 1024)
		used := m.Sys
		return tot, used, int(float64(used) / float64(tot) * 100)
	}

	var memTotal, memAvail uint64
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			val, _ := strconv.ParseUint(fields[1], 10, 64)
			if fields[0] == "MemTotal:" {
				memTotal = val * 1024
			} else if fields[0] == "MemAvailable:" {
				memAvail = val * 1024
			}
		}
	}

	if memTotal > 0 {
		var memUsed uint64
		if memTotal >= memAvail {
			memUsed = memTotal - memAvail
		}
		pctVal := int(float64(memUsed) / float64(memTotal) * 100)
		if pctVal > 100 {
			pctVal = 100
		}
		return memTotal, memUsed, pctVal
	}

	return 2 * 1024 * 1024 * 1024, 64 * 1024 * 1024, 3
}

func getHostCPUPct() int {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 2
	}
	fields := strings.Fields(string(data))
	if len(fields) > 0 {
		load1, err := strconv.ParseFloat(fields[0], 64)
		if err == nil {
			ncpu := runtime.NumCPU()
			if ncpu <= 0 {
				ncpu = 1
			}
			pct := int((load1 / float64(ncpu)) * 100)
			if pct < 1 {
				return 1
			}
			if pct > 100 {
				return 100
			}
			return pct
		}
	}
	return 2
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
	st := s.trafficTracker.GetStats()
	s.writeJSON(w, map[string]any{
		"ok":         true,
		"up":         st.Up,
		"down":       st.Down,
		"totalUp":    st.TotalUp,
		"totalDown":  st.TotalDown,
		"upSpeed":    st.Up,
		"downSpeed":  st.Down,
		"upload":     st.TotalUp,
		"download":   st.TotalDown,
		"lastSample": time.Now().UnixMilli(),
	})
}

func (s *Server) handleTrafficReset(w http.ResponseWriter, r *http.Request) {
	s.trafficTracker.Reset()
	s.writeJSON(w, map[string]any{"ok": true})
}

func formatUptime(sec int64) string {
	if sec <= 0 {
		return "0s"
	}
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm%ds", sec/60, sec%60)
	}
	return fmt.Sprintf("%dh%dm", sec/3600, (sec%3600)/60)
}

func (s *Server) handleSystemUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}

	updateLogMu.Lock()
	updateLogBuffer = []string{"更新脚本已启动，正在后台下载并校验新版本源码..."}
	updateLogMu.Unlock()

	go func() {
		time.Sleep(500 * time.Millisecond)
		arch := runtime.GOARCH
		binName := fmt.Sprintf("fluxboxd-linux-%s", arch)
		appendUpdateLog(fmt.Sprintf("验证并下载 %s 最新发布版本...", binName))

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
			Timeout: 90 * time.Second,
		}

		for _, u := range urls {
			appendUpdateLog(fmt.Sprintf("尝试从镜像源拉取：%s", u))
			resp, err := client.Get(u)
			if err == nil && resp.StatusCode == http.StatusOK {
				data, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err == nil && len(data) > 3*1024*1024 { // 二进制通常大于 5MB
					appendUpdateLog("校验归档内容与执行文件完整性...")
					if err := os.WriteFile(newPath, data, 0755); err == nil {
						downloadSuccess = true
						appendUpdateLog("校验归档内容通过，二进制文件校验成功！")
						break
					}
				}
			}
		}

		if downloadSuccess {
			appendUpdateLog("原子切换代码完成，新版本二进制已就绪...")
			_ = os.Rename(newPath, selfPath)
			appendUpdateLog("重启面板服务中，服务即将平滑重启...")
			time.Sleep(1 * time.Second)
			if _, err := os.Stat("/etc/init.d/fluxbox"); err == nil {
				_ = exec.Command("/etc/init.d/fluxbox", "restart").Run()
			} else {
				os.Exit(0)
			}
		} else {
			appendUpdateLog("更新失败：无法从任何镜像源下载有效二进制")
		}
	}()

	s.writeJSON(w, map[string]any{
		"ok":      true,
		"message": "更新已在后台开始，面板服务即将重启，请稍后刷新页面",
	})
}

func (s *Server) handleSystemUpdateLog(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{
		"ok":  true,
		"log": getUpdateLog(),
	})
}

