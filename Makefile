PROJECT_NAME := fluxboxd
VERSION      := $(shell cat VERSION 2>/dev/null || echo "v2.0.0-ebpf")
COMMIT_SHA   := $(shell git rev-parse --short HEAD 2>/dev/null || cat commit.sha 2>/dev/null || echo "dev")
BUILD_DIR    := dist

# Go 静态链接与极限体积压缩参数 (-s -w 剥离调试符号表)
LDFLAGS      := -s -w -X 'main.Version=$(VERSION)' -X 'main.Commit=$(COMMIT_SHA)'
GO_ENV       := CGO_ENABLED=0

# 你的 iStoreOS 测试机地址 (来自实机勘察报告)
ROUTER_HOST  ?= root@192.168.3.2
ROUTER_DIR   ?= /opt/fluxbox

.PHONY: all generate build-amd64 build-arm64 release deploy-istoreos clean

all: generate build-amd64

## 1. 将 bpf/fluxbox_tc.c 编译为标准 eBPF ELF 字节码文件 bpf/fluxbox_tc.o
generate:
	@echo "⚡ [1/2] 正在使用 Clang 编译 eBPF 内核字节码 (fluxbox_tc.c -> fluxbox_tc.o)..."
	clang -target bpf -O2 -g -Wall -Werror -c bpf/fluxbox_tc.c -o bpf/fluxbox_tc.o
	@ls -lh bpf/fluxbox_tc.o

## 2. 编译 x86_64 软路由版本 (适用 iStoreOS / OpenWrt x86 / Debian)
build-amd64:
	@echo "🚀 [2/2] 正在静态编译 $(PROJECT_NAME) (linux/amd64)..."
	@mkdir -p $(BUILD_DIR)
	$(GO_ENV) GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(PROJECT_NAME)-linux-amd64 .
	@ls -lh $(BUILD_DIR)/$(PROJECT_NAME)-linux-amd64

## 3. 编译 ARM64 软路由版本 (适用 R2S/R4S/R5S/N1/斐讯及主流 ARM 软路由)
build-arm64:
	@echo "🚀 [2/2] 正在静态编译 $(PROJECT_NAME) (linux/arm64)..."
	@mkdir -p $(BUILD_DIR)
	$(GO_ENV) GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(PROJECT_NAME)-linux-arm64 .
	@ls -lh $(BUILD_DIR)/$(PROJECT_NAME)-linux-arm64

## 4. 一键打包双架构 Release
release: generate build-amd64 build-arm64
	@echo "✅ 双架构静态二进制构建完成，产物位于 $(BUILD_DIR)/"

## 5. 一键热推送到 192.168.3.2 (iStoreOS) 并重启服务
deploy-istoreos: build-amd64
	@echo "📦 正在推送 $(PROJECT_NAME) 至 $(ROUTER_HOST):$(ROUTER_DIR)..."
	ssh $(ROUTER_HOST) "mkdir -p $(ROUTER_DIR)/bin $(ROUTER_DIR)/etc $(ROUTER_DIR)/data/flip"
	scp $(BUILD_DIR)/$(PROJECT_NAME)-linux-amd64 $(ROUTER_HOST):$(ROUTER_DIR)/bin/$(PROJECT_NAME)
	ssh $(ROUTER_HOST) "chmod +x $(ROUTER_DIR)/bin/$(PROJECT_NAME)"
	@echo "🎉 推送完成！可在路由器上执行 $(ROUTER_DIR)/bin/$(PROJECT_NAME) 启动面板"

clean:
	rm -rf $(BUILD_DIR) bpf/*_bpf*.go bpf/*.o