package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"


	"fluxbox/bpf"
	"fluxbox/internal/configgen"
	"fluxbox/internal/dnsmasq"
	"fluxbox/internal/kernel"
	"fluxbox/internal/netstack"
	"fluxbox/internal/store"
	"fluxbox/internal/traffic"
)

type Server struct {
	store          *store.Store
	bpfEngine      *bpf.Engine
	supervisor     *kernel.Supervisor
	trafficTracker *traffic.Tracker
	startTime      time.Time
	deployMu       sync.Mutex
	loginFails     sync.Map
	syncStopChan   chan struct{}
	syncStopOnce   sync.Once
}

func NewServer(st *store.Store, bpfEng *bpf.Engine, sup *kernel.Supervisor) *Server {
	tr := traffic.NewTracker(9095)
	if bpfEng != nil {
		tr.SetBPFProvider(bpfEng)
	}
	return &Server{
		store:          st,
		bpfEngine:      bpfEng,
		supervisor:     sup,
		trafficTracker: tr,
		startTime:      time.Now(),
		syncStopChan:   make(chan struct{}),
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg})
}

// AuthMiddleware 校验 Session Cookie
func (s *Server) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// 公开无需拦截的路由
		if path == "/api/health" ||
			path == "/api/auth/status" ||
			path == "/api/auth/setup" ||
			path == "/api/auth/login" ||
			!stringsHasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		if !s.store.IsPasswordSet() {
			next.ServeHTTP(w, r)
			return
		}

		var token string
		if c, err := r.Cookie("session"); err == nil && c.Value != "" {
			token = c.Value
		} else if c, err := r.Cookie("mybox_session"); err == nil && c.Value != "" {
			token = c.Value
		} else if c, err := r.Cookie("fluxbox_session"); err == nil && c.Value != "" {
			token = c.Value
		} else if authHdr := r.Header.Get("Authorization"); strings.HasPrefix(authHdr, "Bearer ") {
			token = strings.TrimPrefix(authHdr, "Bearer ")
		}

		if token == "" || !s.store.VerifyToken(token) {
			s.writeError(w, http.StatusUnauthorized, "未授权或登录已过期")
			return
		}


		next.ServeHTTP(w, r)
	})
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[0:len(prefix)] == prefix
}

// DeployCore 执行完整配置生成、语法检查与内核拉起
func (s *Server) DeployCore(restart bool) error {
	s.deployMu.Lock()
	defer s.deployMu.Unlock()

	settings := s.store.ToConfiggenSettings()
	fullSettings := s.store.GetSettings()
	dataDir := s.store.GetDataDir()

	// 收集处于启用状态订阅下的节点，未分类的孤儿节点默认全量保留
	enabledSubMap := make(map[string]bool)
	hasEnabledSub := false
	for _, sub := range fullSettings.Subscriptions {
		if sub.Enabled {
			enabledSubMap[sub.ID] = true
			hasEnabledSub = true
		}
	}

	var activeNodes []map[string]any
	for _, n := range fullSettings.Nodes {
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

		if subID == "" || !hasEnabledSub || enabledSubMap[subID] {
			activeNodes = append(activeNodes, n)
		}
	}
	if len(activeNodes) == 0 && len(fullSettings.Nodes) > 0 {
		activeNodes = fullSettings.Nodes
	}

	in := configgen.BuildInput{
		DataDir:     dataDir,
		Settings:    settings,
		Nodes:       activeNodes,
		Groups:      fullSettings.Groups,
		Policies:    fullSettings.Policies,
		RulesetSubs: fullSettings.RulesetSubs,
	}

	cfgBytes, nodeDomains, err := configgen.GenerateConfig(in)
	if err != nil {
		return fmt.Errorf("generate config failed: %w", err)
	}

	// 1. 写入待验证配置
	candidatePath := filepath.Join(dataDir, "config.json.candidate")
	if err := os.WriteFile(candidatePath, cfgBytes, 0644); err != nil {
		return fmt.Errorf("write candidate config failed: %w", err)
	}

	// 2. 预检配置有效性
	if s.supervisor != nil {
		if err := s.supervisor.CheckConfig(candidatePath); err != nil {
			_ = os.Remove(candidatePath)
			return fmt.Errorf("sing-box check failed: %w", err)
		}
	}

	// 3. 预检通过，原子替换主配置文件
	realConfigPath := filepath.Join(dataDir, "config.json")
	if err := os.Rename(candidatePath, realConfigPath); err != nil {
		return fmt.Errorf("apply config failed: %w", err)
	}
	if s.supervisor != nil {
		s.supervisor.SetConfigPath(realConfigPath)
	}

	// 4. 重启或启动内核
	if s.supervisor != nil {
		if restart {
			_ = s.supervisor.Restart()
		} else {
			_ = s.supervisor.Start()
		}
	}

	// 5. 确保网络栈与 DNS 劫持生效
	_ = netstack.Setup()
	hijackPort := fullSettings.DNS.HijackPort
	if hijackPort == 0 {
		hijackPort = 36053
	}
	_ = dnsmasq.Hijack(hijackPort)

	// 6. 将节点域名解析为真实 IP 注入 eBPF LPM Trie 直连表
	if s.bpfEngine != nil {
		_ = s.bpfEngine.SetGlobalFlags(fullSettings.Network.DirectBypass, fullSettings.Network.RejectQuic)
		go func(hosts []string) {
			for _, h := range hosts {
				// 若本身已是 IP 或 CIDR，直接写入
				if err := s.bpfEngine.AddDirectCIDR(h); err == nil {
					continue
				}
				// 若是节点域名，异步解析出真实 IPv4 压入 eBPF LPM 树，防止阻塞部署
				if ips, err := net.LookupIP(h); err == nil {
					for _, ip := range ips {
						if v4 := ip.To4(); v4 != nil {
							_ = s.bpfEngine.AddDirectCIDR(v4.String() + "/32")
						}
					}
				}
			}
		}(nodeDomains)

		// 同步内网客户端分流策略 (毫秒级生效)
		for _, c := range s.store.GetClients() {
			_ = s.bpfEngine.UpdateClientMode(c.IP, c.Mode)
		}
	}

	// 记录部署时间戳
	now := time.Now().UnixMilli()
	_ = s.store.MutateSettings(func(st *store.FullSettings) {
		st.Meta.LastDeployAt = &now
		st.Meta.LastDeployError = ""
	})

	return nil
}

// StartSubscriptionAutoSync 启动定时自动后台同步协程 (支持自定义间隔，默认 24h 凌晨)
func (s *Server) StartSubscriptionAutoSync(interval time.Duration) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	go func() {
		// 若为 24h 周期，优先等待至次日凌晨 4:00 执行，避开网络使用高峰
		if interval >= 24*time.Hour {
			now := time.Now()
			next4AM := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, now.Location())
			if !next4AM.After(now) {
				next4AM = next4AM.Add(24 * time.Hour)
			}
			initialWait := next4AM.Sub(now)

			select {
			case <-s.syncStopChan:
				return
			case <-time.After(initialWait):
				updated, err := s.SyncSubscriptions()
				if err != nil {
					log.Printf("⚠️  [订阅自动同步] 凌晨周期同步提示: %v", err)
				} else if updated > 0 {
					log.Printf("✅ [订阅自动同步] 凌晨周期同步完成，成功刷新 %d 个订阅与最新流量配额", updated)
				}
			}
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.syncStopChan:
				return
			case <-ticker.C:
				updated, err := s.SyncSubscriptions()
				if err != nil {
					log.Printf("⚠️  [订阅自动同步] 周期同步提示: %v", err)
				} else if updated > 0 {
					log.Printf("✅ [订阅自动同步] 周期同步完成，成功刷新 %d 个订阅与最新流量配额", updated)
				}
			}
		}
	}()
}

// StopSubscriptionAutoSync 停止定时同步协程
func (s *Server) StopSubscriptionAutoSync() {
	s.syncStopOnce.Do(func() {
		if s.syncStopChan != nil {
			close(s.syncStopChan)
		}
	})
}

// Close 优雅停止所有后台子任务 (自动同步协程、流量监控协程等)
func (s *Server) Close() {
	s.StopSubscriptionAutoSync()
	if s.trafficTracker != nil {
		s.trafficTracker.Close()
	}
}


