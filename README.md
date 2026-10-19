# FluxBox 2.0 ⚡

基于 **Go + Linux eBPF (TC) + TProxy + 官方原版 sing-box** 的高性能现代透明代理软路由系统。

[![Release](https://img.shields.io/badge/version-v2.0.0--ebpf-blue.svg)](https://github.com/asdzx07/fluxbox/releases)
[![Kernel](https://img.shields.io/badge/eBPF-TC%20Direct--Action-success.svg)](https://github.com/asdzx07/fluxbox)
[![sing-box](https://img.shields.io/badge/sing--box-official%20binary-orange.svg)](https://github.com/SagerNet/sing-box)
[![Platform](https://img.shields.io/badge/platform-OpenWrt%20%7C%20iStoreOS%20%7C%20Linux-green.svg)](https://github.com/asdzx07/fluxbox)
[![License](https://img.shields.io/badge/license-MIT-purple.svg)](./LICENSE)

---

## 📖 简介

**FluxBox 2.0** 是一次架构级跃迁。彻底摆脱了对 Node.js 运行时的依赖，吸纳借鉴了 **dae 内核分流思想**，采用 Go 语言与 Linux eBPF 技术重构。

整个面板与核心调度编译为**纯静态单一二进制文件（仅 14MB）**，前端 HTML/CSS/JS 全量内嵌，内存开销由传统方案的 120MB+ 骤降至 **10~15MB**，开箱即用、即拷即跑。

---

## ✨ 核心特性

| 功能模块 | 说明 |
| :--- | :--- |
| ⚡ **eBPF TC 零拷贝旁路** | 在网卡驱动层（TC Ingress）进行快速识别，局域网私网网段与国内 IP 物理级直连放行，**完全不进代理内核，0 CPU 损耗跑满线速**。 |
| 🛡️ **硬件级 QUIC (UDP 443) 阻断** | eBPF 内核单指令丢弃 UDP 443，强行逼迫浏览器无感秒级回退至极速 TCP (BBR)，彻底消灭视频缓冲卡顿。 |
| 🎯 **50 字节 Flip 秒级热重载** | 吸收实机勘察报告设计，将策略开关解耦为外部 50 字节微型规则集（`obflip-*.json`），**切换策略 0 毫秒断流，测速与游戏不断线**。 |
| 📱 **设备分流实时生效** | 局域网设备按 IP 定制策略（NAS 强制直连、电视全局代理），配置直接写入 Linux 内核 eBPF Hash Map，**毫秒级即时生效，无需重启服务**。 |
| 📦 **全协议订阅解析** | 内置通用解析器，支持 Base64、Clash YAML、sing-box JSON 多种格式；支持 VLESS (REALITY)、Hysteria 2、TUIC、Trojan、SS 等现代协议。 |
| 🌐 **DNSMasq 深度接管** | 自动接管系统 DNS 并结合 Fake-IP（`198.19.0.0/16`），屏蔽 HTTPS/SVCB 探测记录，彻底杜绝 DNS 泄漏与运营商劫持。 |
| 🛡️ **配置预检与断网自愈** | 每次下发配置前强制执行 `sing-box check` 语法预检，发现错误秒级阻断回滚；退出时自动还原网络栈与 dnsmasq，绝不让软路由意外断网。 |

---

## 🚀 极速安装

### 1. 一键安装（2.0 纯静态版）

在终端以 `root` 权限执行以下命令（3 秒极速安装）：

**OpenWrt / iStoreOS（安装为 procd 系统服务）：**
```sh
curl -fsSL https://raw.githubusercontent.com/asdzx07/fluxbox/main/scripts/install.sh | sh
```

**Debian / Ubuntu / Linux（安装为 systemd 系统服务）：**
```sh
curl -fsSL https://raw.githubusercontent.com/asdzx07/fluxbox/main/scripts/install.sh | sudo sh
```

### 2. 访问控制面板

安装完成后，在浏览器中打开：
```
http://<软路由IP>:3036
```
首次访问将提示设置管理密码，即可进入控制台。

> [!NOTE]
> **旧版本 (1.x Node.js 版本) 独立安装通道**：
> 若在老旧设备上仍需安装 1.x 旧版，可使用已归档的 `v1` 分支专属安装命令：
> ```sh
> curl -fsSL https://raw.githubusercontent.com/asdzx07/fluxbox/v1/scripts/install.sh | sh
> ```

---

## 🛠️ 本地编译与构建

FluxBox 采用标准 Go 工程结构，支持一键跨平台静态编译：

```bash
# 编译 Linux x86_64 生产版本 (适用 iStoreOS / OpenWrt x86)
make build-amd64

# 编译 Linux ARM64 生产版本 (适用 R2S / R4S / R5S / 斐讯 N1 等)
make build-arm64

# 一键打包双架构 Release
make release
```

---

## 🏗️ 架构拓扑

```
                ┌─────────────────────────────────────────┐
                │          内嵌 Web 控制面板 (SPA)        │
                │   仪表盘 / 节点管理 / 策略组 / 内网分流   │
                └────────────────────┬────────────────────┘
                                     │ HTTP (:3036)
┌────────────────────────────────────▼────────────────────────────────────┐
│                    FluxBox 守护进程 (Go 编写)                           │
│  ┌───────────────────────┐  ┌───────────────────────┐  ┌─────────────┐  │
│  │  RESTful API & 认证    │  │  sing-box 进程守护器  │  │ JSON 存储层 │  │
│  └───────────────────────┘  └───────────────────────┘  └─────────────┘  │
└────────────────────────────────────┬────────────────────────────────────┘
                                     │
           ┌─────────────────────────┴─────────────────────────┐
           ▼                                                   ▼
┌─────────────────────────────────┐                 ┌────────────────────┐
│   Linux 内核 eBPF TC 拦截层     │                 │   sing-box 核心    │
│  ┌───────────────────────────┐  │                 │                    │
│  │ 内网互访 / 大陆 IP 零拷贝 │  │  TProxy 策略路由 │  │  出站节点调度  │
│  │ (TC_ACT_OK 原生放行)      │  ├────────────────►│  │  FakeIP DNS     │
│  └───────────────────────────┘  │  (fwmark 0x2023)│  │  策略规则集     │
│  ┌───────────────────────────┐  │                 └────────────────────┘
│  │ QUIC (UDP 443) 硬件阻断   │  │
│  └───────────────────────────┘  │
└─────────────────────────────────┘
```
