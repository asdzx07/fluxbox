package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fluxbox/bpf"
	"fluxbox/internal/configgen"
	"fluxbox/internal/dnsmasq"
	"fluxbox/internal/kernel"
	"fluxbox/internal/netstack"
	"fluxbox/internal/store"
)

type Server struct {
	store      *store.Store
	bpfEngine  *bpf.Engine
	supervisor *kernel.Supervisor
	startTime  time.Time
	deployMu   sync.Mutex
	loginFails sync.Map
}

func NewServer(st *store.Store, bpfEng *bpf.Engine, sup *kernel.Supervisor) *Server {
	return &Server{
		store:      st,
		bpfEngine:  bpfEng,
		supervisor: sup,
		startTime:  time.Now(),
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

		cookie, err := r.Cookie("session")
		if err != nil || !s.store.VerifyToken(cookie.Value) {
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

	in := configgen.BuildInput{
		DataDir:     dataDir,
		Settings:    settings,
		Nodes:       fullSettings.Nodes,
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
	if err := s.supervisor.CheckConfig(candidatePath); err != nil {
		_ = os.Remove(candidatePath)
		return fmt.Errorf("sing-box check failed: %w", err)
	}

	// 3. 预检通过，原子替换主配置文件
	realConfigPath := filepath.Join(dataDir, "config.json")
	if err := os.Rename(candidatePath, realConfigPath); err != nil {
		return fmt.Errorf("apply config failed: %w", err)
	}
	s.supervisor.SetConfigPath(realConfigPath)

	// 4. 重启或启动内核
	if restart {
		_ = s.supervisor.Restart()
	} else {
		_ = s.supervisor.Start()
	}

	// 5. 确保网络栈与 DNS 劫持生效
	_ = netstack.Setup()
	hijackPort := fullSettings.DNS.HijackPort
	if hijackPort == 0 {
		hijackPort = 36053
	}
	_ = dnsmasq.Hijack(hijackPort)

	// 6. 将节点域名/IP注入 eBPF 直连表
	if s.bpfEngine != nil {
		_ = s.bpfEngine.SetGlobalFlags(fullSettings.Network.DirectBypass, fullSettings.Network.RejectQuic)
		for _, domain := range nodeDomains {
			_ = s.bpfEngine.AddDirectCIDR(domain)
		}
		// 同步内网客户端分流策略
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
