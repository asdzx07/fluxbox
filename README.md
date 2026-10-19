# FluxBox 2.0 ⚡

> **现代高性能 Linux eBPF 旁路加速 + 官方原版 sing-box 软路由代理管理平台**

[![Release](https://img.shields.io/badge/version-v2.0.0--ebpf-blue.svg)](https://github.com/asdzx07/fluxbox/releases)
[![Kernel](https://img.shields.io/badge/eBPF-TC%20Direct--Action-success.svg)](https://github.com/asdzx07/fluxbox)
[![Memory](https://img.shields.io/badge/Memory-~84MB%20Stable-brightgreen.svg)](https://github.com/asdzx07/fluxbox)
[![sing-box](https://img.shields.io/badge/sing--box-official%20binary-orange.svg)](https://github.com/SagerNet/sing-box)
[![Platform](https://img.shields.io/badge/platform-OpenWrt%20%7C%20iStoreOS%20%7C%20Linux-green.svg)](https://github.com/asdzx07/fluxbox)
[![License](https://img.shields.io/badge/license-MIT-purple.svg)](./LICENSE)

---

## 📖 简介

**FluxBox 2.0** 是一次深度的工程级架构跃迁。项目彻底抛弃了 Node.js 运行时依赖，采用 **Go 语言 + Linux eBPF (TC 层) + 官方原版 sing-box** 深度协同的混合旁路架构。

整个后端、调度引擎、eBPF 字节码以及现代化的 Web 前端控制面板全部打包为**单一无依赖的纯静态二进制文件（约 10MB）**，即拷即用，开箱即跑！

通过深度内核级旁路优化，全套系统（`fluxboxd` 守护面板 + `sing-box` 代理内核 + eBPF 引擎）的真实物理内存（VmRSS）从传统的 400MB~500MB **极限压缩并稳态锁死在 ~84 MB**，系统可用内存高达 85% 以上，千兆满速跑满时 CPU 占用近乎归零！

---

## 🌟 核心突破与技术亮点

### 1. ⚡ 「三级火箭」eBPF 内核旁路加速引擎
吸纳 dae 内核网络设计精髓，在网卡驱动层（TC Ingress）构筑三级流水线，让国内与直连流量在网卡驱动层线速硬件直出，**真正做到 0 损耗完全不进 sing-box 用户态内核**：
* 🚀 **一级短路 (FakeIP 位运算)**：以纳秒级位运算快速判定 198.18.0.0/15 地址段，秒级打上标记直通 sing-box，跳过所有查表开销；
* 🚀 **二级自学习 (LRU Hash Map)**：容量为 8,192 的内核级 `learned_ip_map`。首次连接由 sing-box 判定为“直连”后，后台协程自动将其 IP 刻入内核 LRU 表，后续该 IP 的全部大文件下载/高并发流量在网卡层享受 $O(1)$ 硬件旁路；
* 🚀 **三级静态兜底 (LPM Trie 树)**：系统启动时一次性向内核挂载 **8,805 条中国大陆 IPv4 CIDR 网段**，国内裸 IP、游戏、语音通话首包即享网卡硬件直出。

### 2. 🪶 极限内存瘦身（从 500MB 暴降至 ~80MB）
* 🛡️ **注入 GOMEMLIMIT 紧箍咒**：为 `sing-box` 进程注入 `GOMEMLIMIT=55MiB` 与 `GOGC=40`，强制 Go 运行时在加载并构建完规则集内存树后，立刻触发 GC 并通过系统调用将物理内存彻底交还给 Linux 内核；
* 🛡️ **内核与用户态解耦，剔除冗余**：既然 eBPF 已经在网卡 TC 层接管了 8,805 条大陆 CIDR 硬件旁路，`sing-box` 用户态彻底剔除 `geoip-cn.srs` 规则集与庞大的广告规则集，杜绝数十万条内存二叉树重复驻留；
* 🛡️ **面板守护自身锁死**：`fluxboxd` 通过 `runtime/debug` 将堆内存软上限锁死在 20MB（实测仅 13MB~21MB），杜绝长期运行内存膨胀；
* 🛡️ **实测双进程合计常驻物理内存仅 ~84 MB**，系统已用内存降至 190MB 以内，可用率高达 86% 以上！

### 3. 🎯 50 字节 Flip 秒级热重载（游戏测速 0 毫秒断流）
* 将每个策略的开闭状态解耦为磁盘上 50 字节的微型规则集（`obflip-*.json`）；
* 切换分流策略或热重载时，无需重启 sing-box 或重载巨型配置文件，**0 毫秒无感瞬间切换，局域网测速与联机游戏不断线**。

### 4. 📱 设备级精准分流（秒级动态生效）
* 在控制面板上可随时为内网各 IP 设备独立分配分流策略（如：NAS 强制直连、电视全局代理、工作机规则分流）；
* 策略直接下发到 Linux 内核 BPF Map，**毫秒级即时生效，无需重载任何服务**。

### 5. 💻 Windows 新一代原生伴侣客户端
* 提供单文件独立启动器 `FluxBox.exe` + 纯 Go 伴侣后台 `fluxbox-agent.exe`（完全免除 Node.js 安装）；
* **优先调用 Microsoft Edge 独立原生 App 模式渲染**：与用户日常使用的 Google Chrome 浏览器彻底物理隔离，呈现为独立的桌面窗口，绝不弹标签页、退出时绝不误杀用户日常浏览器；
* 提供一键恢复 Windows 原生网络（DHCP / 自动 DNS）快捷机制，退出即还原，防止断网。

---

## 📊 真实硬件物理内存（VmRSS）对比

| 模块 / 进程 | 优化前状态 | **FluxBox 2.0 瘦身后实测** | 性能提升 |
| :--- | :--- | :--- | :--- |
| **`fluxboxd` 守护进程** | 110.7 MB | **13.4 MB** | **内存占用下降 88% ⬇️** |
| **`sing-box` 代理核心** | 179.4 MB | **70.8 MB** | **内存占用下降 60% ⬇️** |
| **核心双进程总内存** | **290.1 MB** | **仅 84.2 MB** | **节约超 205 MB 物理内存！** |
| **系统实际已用内存 (used)** | 401 MB | **191 MB** | **整机内存大幅瘦身 ⬇️** |
| **系统可用内存 (Available)** | 1,534 MB | **1,749 MB (1.71 GB)** | **可用率高达 86.2% 🚀** |

---

## 🚀 极速一键安装

在软路由或 Linux 终端以 `root` 权限执行以下一键命令（3 秒极速安装）：

### OpenWrt / iStoreOS（安装为 procd 系统服务）：
```sh
curl -fsSL https://raw.githubusercontent.com/asdzx07/fluxbox/main/scripts/install.sh | sh
```

### Debian / Ubuntu / Linux（安装为 systemd 系统服务）：
```sh
curl -fsSL https://raw.githubusercontent.com/asdzx07/fluxbox/main/scripts/install.sh | sudo sh
```

安装完成后，打开浏览器访问：
```
http://<软路由IP>:52155
```
首次打开设置管理员密码即可进入全新控制台。

---

## 🏗️ 架构拓扑图

```text
                     ┌─────────────────────────────────────────┐
                     │          内嵌 Web 控制面板 (SPA)         │
                     │   仪表盘 / 节点管理 / 策略组 / 内网分流   │
                     └────────────────────┬────────────────────┘
                                          │ HTTP (:52155)
     ┌────────────────────────────────────▼────────────────────────────────────┐
     │                    FluxBox 守护进程 (Go 编写, ~13MB)                     │
     │  ┌───────────────────────┐  ┌───────────────────────┐  ┌─────────────┐  │
     │  │  RESTful API & 认证   │  │  sing-box 进程守护器   │  │ JSON 存储层  │  │
     │  │  LRU 直连 IP 自动学习  │  │  (GOMEMLIMIT 紧箍咒)  │  │  Flip 热重载 │  │
     │  └───────────────────────┘  └───────────────────────┘  └─────────────┘  │
     └────────────────────────────────────┬────────────────────────────────────┘
                                          │ 
                ┌─────────────────────────┴─────────────────────────┐
                ▼                                                   ▼
┌─────────────────────────────────┐                 ┌─────────────────────┐
│   Linux 内核 eBPF TC 拦截层      │                 │   sing-box 核心     │
│  ┌───────────────────────────┐  │                 │    (~70MB 稳态)     │
│  │ 1. FakeIP (198.18/15)     │  │  TProxy 策略路由 │  ┌────────────────┐ │
│  │    纳秒级位运算快速标记     │  ├────────────────►│  │ 出站节点调度    │ │
│  └───────────────────────────┘  │  (fwmark 0x2023)│  │ FakeIP DNS 缓存 │ │
│  ┌───────────────────────────┐  │                 │  │ 策略规则集分流   │ │
│  │ 2. LRU Hash 直连自学习     │  │                 │  └─────────────────┘ │
│  │    (8,192 容量 O(1) 旁路)  │  │                 └──────────────────────┘
│  └───────────────────────────┘  │
│  ┌───────────────────────────┐  │
│  │ 3. 8,805 大陆 CIDR 兜底    │  │
│  │    (TC_ACT_OK 网卡硬件直出)│  │
│  └───────────────────────────┘  │
└─────────────────────────────────┘
```

---

## 🛠️ 本地编译与构建

FluxBox 采用标准 Go 工程结构，可在 Windows / Linux / macOS 上直接交叉编译：

```bash
# 编译 Linux x86_64 生产版本 (适用 iStoreOS / OpenWrt x86 / PC)
make build-amd64

# 编译 Linux ARM64 生产版本 (适用 NanoPi R2S / R4S / R5S / 斐讯 N1 等)
make build-arm64

# 一键打包双架构 Release
make release
```

---

## 📄 License

本项目遵循 [MIT License](./LICENSE) 开源协议。
