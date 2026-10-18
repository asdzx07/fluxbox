package api

import (
	"net/http"
	"strings"
)

// RegisterRoutes 注册全部 API 路由并绑定至 mux
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// 公共健康与认证路由
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/api/auth/setup", s.handleAuthSetup)
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("/api/auth/me", s.handleAuthMe)
	mux.HandleFunc("/api/auth/change-password", s.handleAuthChangePassword)

	// 系统与概览
	mux.HandleFunc("/api/overview", s.handleOverview)
	mux.HandleFunc("/api/system/stats", s.handleSystemStats)
	mux.HandleFunc("/api/system/version", s.handleSystemVersion)
	mux.HandleFunc("/api/system/update", s.handleSystemUpdate)
	mux.HandleFunc("/api/traffic", s.handleTraffic)
	mux.HandleFunc("/api/traffic/reset", s.handleTrafficReset)

	// 全局设置
	mux.HandleFunc("/api/settings", s.handleSettings)

	// 订阅与节点
	mux.HandleFunc("/api/subscriptions", s.handleSubscriptions)
	mux.HandleFunc("/api/subscriptions/", s.handleSubscriptionAction)
	mux.HandleFunc("/api/nodes", s.handleNodes)
	mux.HandleFunc("/api/nodes/status", s.handleNodesStatus)
	mux.HandleFunc("/api/nodes/select", s.handleNodesSelect)
	mux.HandleFunc("/api/nodes/latency", s.handleNodesLatency)
	mux.HandleFunc("/api/nodes/latency/batch", s.handleNodesLatencyBatch)

	// 分组与策略
	mux.HandleFunc("/api/groups", s.handleGroups)
	mux.HandleFunc("/api/policies", s.handlePolicies)
	mux.HandleFunc("/api/policies/reset", s.handlePolicyReset)
	mux.HandleFunc("/api/policies/", s.handlePolicyToggle)

	// 规则集管理
	mux.HandleFunc("/api/ruleset-subs", s.handleRulesetSubs)
	mux.HandleFunc("/api/ruleset-subs/", s.handleRulesetSubAction)
	mux.HandleFunc("/api/rulesets/status", s.handleRulesetsStatus)
	mux.HandleFunc("/api/rulesets/refresh", s.handleRulesetsRefresh)
	mux.HandleFunc("/api/adblock/refresh", s.handleAdblockRefresh)

	// 客户端分流
	mux.HandleFunc("/api/clients", s.handleClients)
	mux.HandleFunc("/api/clients/scan", s.handleClientsScan)
	mux.HandleFunc("/api/clients/", s.handleClientDelete)

	// 部署与内核管控
	mux.HandleFunc("/api/deploy", s.handleDeploy)
	mux.HandleFunc("/api/teardown", s.handleTeardown)
	mux.HandleFunc("/api/kernel/log", s.handleKernelLog)
	mux.HandleFunc("/api/kernel/latest", s.handleKernelLatest)
	mux.HandleFunc("/api/kernel/", s.handleKernelAction)

	// 连接监控
	mux.HandleFunc("/api/connections", s.handleConnections)
	mux.HandleFunc("/api/connections/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.handleConnectionDelete(w, r)
		}
	})

	// Clash Controller 透明反向代理
	mux.Handle("/api/controller/", s.handleControllerProxy())
	mux.Handle("/api/controller", s.handleControllerProxy())
}

// Router 返回包含完整认证中间件的 http.Handler
func (s *Server) Router(staticHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	if staticHandler != nil {
		mux.Handle("/", staticHandler)
	}

	return s.AuthMiddleware(mux)
}

func init() {
	// 确保 strings 导入有效
	_ = strings.Clone("")
}
