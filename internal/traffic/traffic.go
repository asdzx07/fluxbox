package traffic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Stats struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	TotalUp   int64 `json:"totalUp"`
	TotalDown int64 `json:"totalDown"`
	UpSpeed   int64 `json:"upSpeed"`
	DownSpeed int64 `json:"downSpeed"`
	Upload    int64 `json:"upload"`
	Download  int64 `json:"download"`
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
	stopChan     chan struct{}
}

func NewTracker(clashPort int) *Tracker {
	if clashPort <= 0 {
		clashPort = 9095
	}
	t := &Tracker{
		clashAPIPort: clashPort,
		lastSample:   time.Now(),
		stopChan:     make(chan struct{}),
	}
	go t.loop()
	return t
}

func (t *Tracker) loop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-t.stopChan:
			return
		case now := <-ticker.C:
			t.sample(now)
		}
	}
}

func (t *Tracker) sample(now time.Time) {
	totUp, totDn := t.fetchRawTotals()

	t.mu.Lock()
	defer t.mu.Unlock()

	dt := now.Sub(t.lastSample).Seconds()
	if dt < 0.3 {
		return
	}

	if t.lastTotalUp > 0 && totUp >= t.lastTotalUp {
		t.curUpRate = int64(float64(totUp-t.lastTotalUp) / dt)
	} else if totUp > 0 && t.lastTotalUp == 0 {
		t.curUpRate = 0
	} else {
		// 计数器回退（可能内核重启）
		t.curUpRate = 0
	}

	if t.lastTotalDn > 0 && totDn >= t.lastTotalDn {
		t.curDnRate = int64(float64(totDn-t.lastTotalDn) / dt)
	} else if totDn > 0 && t.lastTotalDn == 0 {
		t.curDnRate = 0
	} else {
		t.curDnRate = 0
	}

	t.lastTotalUp = totUp
	t.lastTotalDn = totDn
	t.lastSample = now
	t.curTotalUp = totUp
	t.curTotalDn = totDn
}

func (t *Tracker) fetchRawTotals() (up, down int64) {
	// 1. 优先尝试从 sing-box clash_api /connections 提取
	client := http.Client{Timeout: 500 * time.Millisecond}
	url := fmt.Sprintf("http://127.0.0.1:%d/connections", t.clashAPIPort)
	resp, err := client.Get(url)
	if err == nil && resp.StatusCode == http.StatusOK {
		var connResp struct {
			DownloadTotal int64 `json:"downloadTotal"`
			UploadTotal   int64 `json:"uploadTotal"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&connResp); err == nil {
			_ = resp.Body.Close()
			if connResp.DownloadTotal > 0 || connResp.UploadTotal > 0 {
				return connResp.UploadTotal, connResp.DownloadTotal
			}
		} else {
			_ = resp.Body.Close()
		}
	}

	// 2. 备选方案：从 Linux TUN 虚拟网卡读取 rx_bytes / tx_bytes
	rxBytes := readSysInt64("/sys/class/net/fluxbox-tun/statistics/rx_bytes")
	txBytes := readSysInt64("/sys/class/net/fluxbox-tun/statistics/tx_bytes")
	if rxBytes > 0 || txBytes > 0 {
		return txBytes, rxBytes
	}

	return 0, 0
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

func (t *Tracker) GetStats() Stats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	dispUp := t.curTotalUp - t.baseUp
	if dispUp < 0 {
		dispUp = t.curTotalUp
	}
	dispDn := t.curTotalDn - t.baseDn
	if dispDn < 0 {
		dispDn = t.curTotalDn
	}

	return Stats{
		Up:        t.curUpRate,
		Down:      t.curDnRate,
		TotalUp:   dispUp,
		TotalDown: dispDn,
		UpSpeed:   t.curUpRate,
		DownSpeed: t.curDnRate,
		Upload:    dispUp,
		Download:  dispDn,
	}
}

func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.baseUp = t.curTotalUp
	t.baseDn = t.curTotalDn
}

func (t *Tracker) Close() {
	close(t.stopChan)
}
