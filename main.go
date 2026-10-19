package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"fluxbox/bpf"
	"fluxbox/internal/api"
	"fluxbox/internal/dnsmasq"
	"fluxbox/internal/kernel"
	"fluxbox/internal/netstack"
	"fluxbox/internal/store"
)

// 将美化后的 panel/ 目录完整打包进 Go 二进制文件
//go:embed panel/*
var panelFS embed.FS

var (
	Version   = "v2.0.0-ebpf"
	Commit    = "dev"
	startTime = time.Now()
)

func main() {
	// 清理软路由可能残留的代理环境变量，防止自请求死锁 (如 dial 127.0.0.1:7891)
	os.Unsetenv("http_proxy")
	os.Unsetenv("https_proxy")
	os.Unsetenv("all_proxy")
	os.Unsetenv("HTTP_PROXY")
	os.Unsetenv("HTTPS_PROXY")
	os.Unsetenv("ALL_PROXY")

	log.Printf("🚀 正在启动 FluxBox (%s, commit: %s, runtime: %s/%s)...", Version, Commit, runtime.GOOS, runtime.GOARCH)

	// 1. 确定数据目录 (生产环境默认 /opt/fluxbox/data，开发环境自动回退当前 ./data)
	dataDir := os.Getenv("FLUXBOX_DATA_DIR")
	if dataDir == "" {
		if runtime.GOOS == "linux" {
			dataDir = "/opt/fluxbox/data"
		} else {
			dataDir = "./data"
		}
	}
	_ = os.MkdirAll(dataDir, 0755)

	// 2. 初始化持久化存储
	st, err := store.NewStore(dataDir)
	if err != nil {
		log.Fatalf("❌ 初始化数据存储失败: %v", err)
	}
	log.Printf("📂 数据存储已就绪: %s", dataDir)

	// 3. 初始化并挂载 eBPF 引擎 (支持环境变量 FLUXBOX_IFACE 动态指定)
	iface := os.Getenv("FLUXBOX_IFACE")
	if iface == "" {
		iface = "br-lan"
	}

	var bpfEngine *bpf.Engine
	bpfEngine, err = bpf.NewEngine(iface)
	if err != nil {
		log.Printf("⚠️  eBPF 引擎初始化跳过 (非 Linux root 或缺少内核 BTF 支持): %v", err)
	} else {
		if err := bpfEngine.AttachTC(); err != nil {
			log.Printf("⚠️  挂载 TC Ingress 到 %s 失败: %v", iface, err)
		} else {
			log.Printf("⚡ FluxBox eBPF 引擎已挂载至网卡 %s (TC Direct-Action 模式)", iface)
		}
	}

	// 4. 初始化 sing-box 内核进程守护器
	configPath := filepath.Join(dataDir, "config.json")
	binPath := os.Getenv("SINGBOX_PATH")
	if binPath == "" && runtime.GOOS == "linux" {
		binPath = "/opt/fluxbox/bin/sing-box"
	}
	sup := kernel.NewSupervisor(binPath, configPath)

	// 5. 初始化 HTTP API 服务
	apiServer := api.NewServer(st, bpfEngine, sup)

	// 6. 若配置了自动启动且存在节点配置，在启动时自动执行一次配置下发
	go func() {
		settings := st.GetSettings()
		if settings.Kernel.AutoStart && (len(settings.Subscriptions) > 0 || len(settings.Nodes) > 0) {
			log.Printf("🔄 检测到已有配置与节点，正在自动拉起内核与网络栈...")
			if err := apiServer.DeployCore(false); err != nil {
				log.Printf("⚠️  开机自动部署提示: %v", err)
			}
		}
	}()

	// 7. 配置静态资源与路由
	subFS, _ := fs.Sub(panelFS, "panel")
	staticHandler := http.FileServer(http.FS(subFS))
	router := apiServer.Router(staticHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "52155"
	}

	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// 8. 监听优雅退出信号 (SIGINT, SIGTERM) 彻底防止软路由断网！
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("⚡ FluxBox 控制面板已启动: http://0.0.0.0:%s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ HTTP 服务启动失败: %v", err)
		}
	}()

	// 等待退出信号
	sig := <-sigChan
	log.Printf("🛑 收到退出信号 (%v)，正在安全清理网络栈与还原配置...", sig)

	// 优雅还原与退出
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)

	_ = sup.Stop()
	_ = netstack.Teardown()
	_ = dnsmasq.Restore()
	if bpfEngine != nil {
		_ = bpfEngine.Close()
	}

	log.Printf("✅ FluxBox 已安全退出，局域网网络已恢复默认原生状态。")
}