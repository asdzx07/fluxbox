package traffic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fluxbox/bpf"
)

// BPFStatsProvider 定义获取底层 eBPF 网卡数据包统计的接口
type BPFStatsProvider interface {
	GetStats() (bpf.Stats, error)
}

// Stats 汇总应用层吞吐与底层 eBPF 数据包双轨统计
type Stats struct {
	Up            int64  `json:"up"`
	Down          int64  `json:"down"`
	TotalUp       int64  `json:"totalUp"`
	TotalDown     int64  `json:"totalDown"`
	UpSpeed       int64  `json:"upSpeed"`
	DownSpeed     int64  `json:"downSpeed"`
	Upload        int64  `json:"upload"`
	Download      int64  `json:"download"`
	DirectPackets uint64 `json:"directPackets"`
	ProxyPackets  uint64 `json:"proxyPackets"`
	ShotPackets   uint64 `json:"shotPackets"`
}

type Tracker struct {
	mu           sync.RWMutex
	lastTotalUp  int64
	lastTotalDn  int64
	lastSample   time.Time
	curUpRate    int64
	curDnRate    int64
	curTotalUp   int64
	curTotalDn   int64
	baseUp       int64
	baseDn       int64
	clashAPIPort int
	apiBaseURL   string // 用于单元测试自定义地址，默认 http://127.0.0.1:<port>
	customRxPath string
	customTxPath string
	bpfProvider  BPFStatsProvider
	streamActive atomic.Bool
	ctx          context.Context
	cancel       context.CancelFunc
	stopOnce     sync.Once
}

func NewTracker(clashPort int) *Tracker {
	if clashPort <= 0 {
		clashPort = 9095
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &Tracker{
		clashAPIPort: clashPort,
		lastSample:   time.Now(),
		ctx:          ctx,
		cancel:       cancel,
	}
	go t.trafficStreamLoop()
	go t.fallbackLoop()
	return t
}

// SetBPFProvider 挂载 eBPF 统计引擎
func (t *Tracker) SetBPFProvider(provider BPFStatsProvider) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bpfProvider = provider
}

// SetAPIBaseURL 覆盖 Clash API 基地址 (主要用于单测 mock)
func (t *Tracker) SetAPIBaseURL(baseURL string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.apiBaseURL = baseURL
}

// SetSysStatsPaths 自定义虚拟网卡统计文件路径 (主要用于单测 mock)
func (t *Tracker) SetSysStatsPaths(rxPath, txPath string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.customRxPath = rxPath
	t.customTxPath = txPath
}

func (t *Tracker) sysPaths() (rx, tx string) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	rx = "/sys/class/net/fluxbox-tun/statistics/rx_bytes"
	tx = "/sys/class/net/fluxbox-tun/statistics/tx_bytes"
	if t.customRxPath != "" {
		rx = t.customRxPath
	}
	if t.customTxPath != "" {
		tx = t.customTxPath
	}
	return
}

func (t *Tracker) getTrafficURL() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.apiBaseURL != "" {
		return strings.TrimRight(t.apiBaseURL, "/") + "/traffic"
	}
	return fmt.Sprintf("http://127.0.0.1:%d/traffic", t.clashAPIPort)
}

// trafficStreamLoop 保持长连接消费 Clash API 的 /traffic 数据流，彻底替代每秒反序列化 /connections 大对象的低效轮询
func (t *Tracker) trafficStreamLoop() {
	client := &http.Client{
		Timeout: 0, // 流式长连接通过看门狗动态检测
	}

	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		targetURL := t.getTrafficURL()
		streamCtx, streamCancel := context.WithCancel(t.ctx)

		req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, targetURL, nil)
		if err != nil {
			streamCancel()
			time.Sleep(1 * time.Second)
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			streamCancel()
			t.streamActive.Store(false)
			select {
			case <-t.ctx.Done():
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			streamCancel()
			t.streamActive.Store(false)
			select {
			case <-t.ctx.Done():
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}

		t.streamActive.Store(true)

		// 看门狗：Clash API 默认每秒推送一条，若超 5 秒无数据则主动关闭断开防假死
		watchdog := time.AfterFunc(5*time.Second, func() {
			streamCancel()
		})

		scanner := bufio.NewScanner(resp.Body)
		// 每次扫描一行 JSON: {"up": 1024, "down": 2048}
		for scanner.Scan() {
			watchdog.Reset(5 * time.Second)
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var payload struct {
				Up   int64 `json:"up"`
				Down int64 `json:"down"`
			}
			if err := json.Unmarshal(line, &payload); err == nil {
				now := time.Now()
				t.mu.Lock()
				t.curUpRate = payload.Up
				t.curDnRate = payload.Down
				t.curTotalUp += payload.Up
				t.curTotalDn += payload.Down
				t.lastSample = now
				t.mu.Unlock()
			}
		}

		watchdog.Stop()
		_ = resp.Body.Close()
		streamCancel()
		t.streamActive.Store(false)

		select {
		case <-t.ctx.Done():
			return
		case <-time.After(1 * time.Second):
		}
	}
}

// fallbackLoop 当 /traffic 流不可用或异常断开时，自动降级为读取 Linux 虚拟网卡统计
func (t *Tracker) fallbackLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-t.ctx.Done():
			return
		case now := <-ticker.C:
			// 若流处于活跃状态且 2.5 秒内有最新样本，无需执行 fallback
			if t.streamActive.Load() {
				t.mu.RLock()
				gap := now.Sub(t.lastSample)
				t.mu.RUnlock()
				if gap < 2500*time.Millisecond {
					continue
				}
			}

			t.fallbackSample(now)
		}
	}
}

func (t *Tracker) fallbackSample(now time.Time) {
	rxPath, txPath := t.sysPaths()
	rxBytes := readSysInt64(rxPath)
	txBytes := readSysInt64(txPath)
	if rxBytes == 0 && txBytes == 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	totUp := txBytes
	totDn := rxBytes

	// 首次读取网卡或脱离流模式恢复时，平滑对齐基准，避免因开机累计值导致突发毛刺
	if t.lastTotalUp == 0 || t.lastTotalDn == 0 || now.Sub(t.lastSample) > 4*time.Second {
		t.lastTotalUp = totUp
		t.lastTotalDn = totDn
		t.lastSample = now
		t.curUpRate = 0
		t.curDnRate = 0
		return
	}

	dt := now.Sub(t.lastSample).Seconds()
	if dt < 0.3 {
		return
	}

	if totUp >= t.lastTotalUp {
		deltaUp := totUp - t.lastTotalUp
		t.curUpRate = int64(float64(deltaUp) / dt)
		t.curTotalUp += deltaUp
	} else {
		// 计数器回退（可能网卡重启）
		t.curUpRate = 0
	}

	if totDn >= t.lastTotalDn {
		deltaDn := totDn - t.lastTotalDn
		t.curDnRate = int64(float64(deltaDn) / dt)
		t.curTotalDn += deltaDn
	} else {
		t.curDnRate = 0
	}

	t.lastTotalUp = totUp
	t.lastTotalDn = totDn
	t.lastSample = now
}

func readSysInt64(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	val, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return val
}

// GetStats 返回当前流量速率、累计总流量以及底层 eBPF 包统计
func (t *Tracker) GetStats() Stats {
	t.mu.RLock()
	dispUp := t.curTotalUp - t.baseUp
	if dispUp < 0 {
		dispUp = t.curTotalUp
	}
	dispDn := t.curTotalDn - t.baseDn
	if dispDn < 0 {
		dispDn = t.curTotalDn
	}
	upRate := t.curUpRate
	dnRate := t.curDnRate
	bpfP := t.bpfProvider
	t.mu.RUnlock()

	var directPkts, proxyPkts, shotPkts uint64
	if bpfP != nil {
		if bs, err := bpfP.GetStats(); err == nil {
			directPkts = bs.DirectPackets
			proxyPkts = bs.ProxyPackets
			shotPkts = bs.ShotPackets
		}
	}

	return Stats{
		Up:            upRate,
		Down:          dnRate,
		TotalUp:       dispUp,
		TotalDown:     dispDn,
		UpSpeed:       upRate,
		DownSpeed:     dnRate,
		Upload:        dispUp,
		Download:      dispDn,
		DirectPackets: directPkts,
		ProxyPackets:  proxyPkts,
		ShotPackets:   shotPkts,
	}
}

// Reset 清零累计流量基准值
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.baseUp = t.curTotalUp
	t.baseDn = t.curTotalDn
}

// Close 安全退出所有监控协程
func (t *Tracker) Close() {
	t.stopOnce.Do(func() {
		t.cancel()
	})
}
