//go:build windows

package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

var (
	lastHeartbeat int64
	clientActive  int32
)

func main() {
	exePath, err := os.Executable()
	baseDir := "."
	if err == nil {
		baseDir = filepath.Dir(exePath)
	}

	logFile, logErr := os.OpenFile(filepath.Join(baseDir, "agent.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if logErr == nil {
		log.SetOutput(logFile)
	}

	log.Printf("=================== FluxBox Agent Starting ===================")
	initConfig(baseDir)

	atomic.StoreInt64(&lastHeartbeat, time.Now().UnixMilli())

	// 自动连接逻辑
	cfg := getConfig()
	if cfg.AutoConnect && cfg.GatewayIP != "" {
		go func() {
			time.Sleep(500 * time.Millisecond)
			if !isConnectedToGateway(cfg.GatewayIP) {
				_ = connectGateway(cfg.GatewayIP)
			}
		}()
	}

	// 启动时自动登录旁路由获取/刷新会话 Cookie
	if cfg.Password != "" && cfg.GatewayIP != "" {
		go func() {
			_, _, _ = loginRemoteGateway(cfg.Password, cfg.GatewayIP, cfg.GatewayPort)
		}()
	}

	// 心跳检测定时器（当前端关闭且未开启托盘常驻时安全退出）
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			c := getConfig()
			if !c.MinimizeToTray && atomic.LoadInt32(&clientActive) == 1 {
				last := atomic.LoadInt64(&lastHeartbeat)
				if time.Now().UnixMilli()-last > 12000 {
					log.Printf("[FluxBox Agent] 检测到前端界面已关闭且未启用托盘常驻，正在还原网络并安全退出...")
					_ = disconnectGateway(c.GatewayIP)
					os.Exit(0)
				}
			}
		}
	}()

	mux := http.NewServeMux()

	// 0. 健康检查
	mux.HandleFunc("/api/local/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// 1. 本地状态 API
	mux.HandleFunc("/api/local/status", handleLocalStatus)
	mux.HandleFunc("/api/local/auth", handleLocalAuth)
	mux.HandleFunc("/api/local/connect", handleLocalConnect)
	mux.HandleFunc("/api/local/disconnect", handleLocalDisconnect)
	mux.HandleFunc("/api/local/config", handleLocalConfig)
	mux.HandleFunc("/api/local/exit", handleLocalExit)
	mux.HandleFunc("/api/local/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		atomic.StoreInt64(&lastHeartbeat, time.Now().UnixMilli())
		atomic.StoreInt32(&clientActive, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// 2. 远程 API 代理转发
	mux.HandleFunc("/remote-api/", handleRemoteAPIProxy)

	// 3. 静态 UI 文件
	uiHandler := getUIHandler(baseDir)
	mux.Handle("/", uiHandler)

	server := &http.Server{
		Addr:    "127.0.0.1:3038",
		Handler: mux,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		log.Printf("[FluxBox Agent] 收到中断信号，正在还原网络...")
		c := getConfig()
		_ = disconnectGateway(c.GatewayIP)
		os.Exit(0)
	}()

	log.Printf("🚀 FluxBox Windows Agent (Go Edition) 监听于: http://127.0.0.1:3038")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("启动 HTTP 监听失败: %v", err)
	}
}

func getUIHandler(baseDir string) http.Handler {
	candidates := []string{
		filepath.Join(baseDir, "ui"),
		filepath.Join(baseDir, "../ui"),
		"ui",
	}

	var uiDir string
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			uiDir = c
			break
		}
	}

	if uiDir == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "UI 资源目录不存在", http.StatusNotFound)
		})
	}

	fs := http.FileServer(http.Dir(uiDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		relPath := strings.TrimPrefix(r.URL.Path, "/")
		if relPath == "" {
			relPath = "index.html"
		}
		targetFile := filepath.Join(uiDir, filepath.FromSlash(relPath))
		if fi, err := os.Stat(targetFile); err != nil || fi.IsDir() {
			// 文件不存在时 fallback 到 index.html
			http.ServeFile(w, r, filepath.Join(uiDir, "index.html"))
			return
		}

		fs.ServeHTTP(w, r)
	})
}

func handleLocalStatus(w http.ResponseWriter, r *http.Request) {
	cfg := getConfig()
	iface := getActiveInterface()
	connected := isConnectedToGateway(cfg.GatewayIP)
	latency := testPing(cfg.GatewayIP)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":             true,
		"connected":      connected,
		"gatewayIp":      cfg.GatewayIP,
		"gatewayPort":    cfg.GatewayPort,
		"autoConnect":    cfg.AutoConnect,
		"minimizeToTray": cfg.MinimizeToTray,
		"hasPassword":    cfg.Password != "",
		"password":       cfg.Password,
		"hasSession":     cfg.SessionCookie != "",
		"latency":        latency,
		"interface":      iface,
	})
}

func handleLocalAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		GatewayIP   string `json:"gatewayIp"`
		GatewayPort int    `json:"gatewayPort"`
		Password    string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	gwIP := body.GatewayIP
	if gwIP == "" {
		gwIP = getConfig().GatewayIP
	}
	gwPort := body.GatewayPort
	if gwPort == 0 {
		gwPort = getConfig().GatewayPort
	}
	if gwPort == 0 {
		gwPort = 52155
	}

	updateConfig(func(c *Config) {
		c.GatewayIP = strings.TrimSpace(gwIP)
		c.GatewayPort = gwPort
	})

	pwd := strings.TrimSpace(body.Password)
	if pwd == "" {
		updateConfig(func(c *Config) {
			c.Password = ""
			c.SessionCookie = ""
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"message":    "已保存为免密模式",
			"hasSession": false,
		})
		return
	}

	ok, msg, err := loginRemoteGateway(pwd, gwIP, gwPort)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "message": msg, "hasSession": ok})
}

func handleLocalConnect(w http.ResponseWriter, r *http.Request) {
	cfg := getConfig()
	err := connectGateway(cfg.GatewayIP)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	iface := getActiveInterface()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"connected": true,
		"gateway":   cfg.GatewayIP,
		"interface": iface.Alias,
		"ip":        iface.IP,
	})
}

func handleLocalDisconnect(w http.ResponseWriter, r *http.Request) {
	cfg := getConfig()
	_ = disconnectGateway(cfg.GatewayIP)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"connected": false,
		"gateway":   cfg.GatewayIP,
	})
}

func handleLocalConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AutoConnect    *bool   `json:"autoConnect"`
		MinimizeToTray *bool   `json:"minimizeToTray"`
		GatewayPort    *int    `json:"gatewayPort"`
		GatewayIP      *string `json:"gatewayIp"`
		Password       *string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	updateConfig(func(c *Config) {
		if body.AutoConnect != nil {
			c.AutoConnect = *body.AutoConnect
		}
		if body.MinimizeToTray != nil {
			c.MinimizeToTray = *body.MinimizeToTray
		}
		if body.GatewayPort != nil && *body.GatewayPort > 0 {
			c.GatewayPort = *body.GatewayPort
		}
		if body.GatewayIP != nil && *body.GatewayIP != "" {
			c.GatewayIP = strings.TrimSpace(*body.GatewayIP)
		}
		if body.Password != nil {
			c.Password = strings.TrimSpace(*body.Password)
		}
	})

	if body.Password != nil && *body.Password != "" {
		cfg := getConfig()
		go func() {
			_, _, _ = loginRemoteGateway(cfg.Password, cfg.GatewayIP, cfg.GatewayPort)
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "config": getConfig()})
}

func handleLocalExit(w http.ResponseWriter, r *http.Request) {
	cfg := getConfig()
	_ = disconnectGateway(cfg.GatewayIP)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": "网络已还原，服务即将退出"})
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
}
