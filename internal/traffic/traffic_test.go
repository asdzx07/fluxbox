package traffic

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"fluxbox/bpf"
)

type mockBPFProvider struct {
	stats bpf.Stats
}

func (m *mockBPFProvider) GetStats() (bpf.Stats, error) {
	return m.stats, nil
}

// 验证 eBPF 统计引擎融合至流量监控体系
func TestTracker_BPFStatsIntegration(t *testing.T) {
	tracker := NewTracker(0)
	defer tracker.Close()

	mock := &mockBPFProvider{
		stats: bpf.Stats{
			DirectPackets: 10240,
			ProxyPackets:  20480,
			ShotPackets:   512,
		},
	}
	tracker.SetBPFProvider(mock)

	st := tracker.GetStats()
	if st.DirectPackets != 10240 {
		t.Fatalf("预期 DirectPackets=10240，实际为 %d", st.DirectPackets)
	}
	if st.ProxyPackets != 20480 {
		t.Fatalf("预期 ProxyPackets=20480，实际为 %d", st.ProxyPackets)
	}
	if st.ShotPackets != 512 {
		t.Fatalf("预期 ShotPackets=512，实际为 %d", st.ShotPackets)
	}
}

// 验证消费 Clash API 的 /traffic 数据流，轻量增量解析与累计逻辑
func TestTracker_TrafficStream(t *testing.T) {
	// 启动模拟的 Clash /traffic 流服务器
	streamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/traffic" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("ResponseWriter 不支持 Flusher")
			return
		}

		// 推送两批瞬时流量样本
		samples := []string{
			`{"up": 50000, "down": 100000}` + "\n",
			`{"up": 20000, "down": 40000}` + "\n",
		}

		for _, sample := range samples {
			select {
			case <-r.Context().Done():
				return
			default:
				_, _ = fmt.Fprint(w, sample)
				flusher.Flush()
				time.Sleep(100 * time.Millisecond)
			}
		}
	}))
	defer streamServer.Close()

	tracker := NewTracker(9999)
	defer tracker.Close()

	tracker.SetAPIBaseURL(streamServer.URL)

	// 等待 Tracker 消费流数据
	var lastStats Stats
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		lastStats = tracker.GetStats()
		if lastStats.TotalUp >= 70000 && lastStats.TotalDown >= 140000 {
			break
		}
	}

	if lastStats.TotalUp < 70000 || lastStats.TotalDown < 140000 {
		t.Fatalf("流式流量解析统计未达到预期: Up=%d, Down=%d, TotalUp=%d, TotalDown=%d",
			lastStats.Up, lastStats.Down, lastStats.TotalUp, lastStats.TotalDown)
	}

	// 验证 Reset 逻辑
	tracker.Reset()
	resetStats := tracker.GetStats()
	if resetStats.TotalUp != 0 || resetStats.TotalDown != 0 {
		t.Fatalf("Reset 后累计流量应为 0，实际 TotalUp=%d, TotalDown=%d", resetStats.TotalUp, resetStats.TotalDown)
	}
}

// 验证 fallbackSample 虚拟网卡降级平滑差量累积机制（无突发跳变，避免开机基数导致数 GB 突增）
func TestTracker_FallbackDeltaAccumulation(t *testing.T) {
	tempDir := t.TempDir()
	rxFile := tempDir + "/rx_bytes"
	txFile := tempDir + "/tx_bytes"

	// 模拟开机已产生 500,000,000 字节底数
	_ = os.WriteFile(rxFile, []byte("500000000"), 0644)
	_ = os.WriteFile(txFile, []byte("200000000"), 0644)

	tracker := NewTracker(0)
	defer tracker.Close()

	tracker.SetSysStatsPaths(rxFile, txFile)

	t0 := time.Now()
	// 第 1 次采样：平滑对齐开机基准，不计入累计
	tracker.fallbackSample(t0)
	st0 := tracker.GetStats()
	if st0.TotalUp != 0 || st0.TotalDown != 0 || st0.Up != 0 || st0.Down != 0 {
		t.Fatalf("首次采样预期完成基线对齐 (0 流量)，实际: %+v", st0)
	}

	// 第 2 次采样：1 秒后产生 1000 字节上传与 2000 字节下载
	t1 := t0.Add(1 * time.Second)
	_ = os.WriteFile(rxFile, []byte("500002000"), 0644)
	_ = os.WriteFile(txFile, []byte("200001000"), 0644)
	tracker.fallbackSample(t1)

	st1 := tracker.GetStats()
	if st1.TotalUp != 1000 || st1.TotalDown != 2000 {
		t.Fatalf("第 2 次差量累积不符合预期: TotalUp=%d (预期 1000), TotalDown=%d (预期 2000)", st1.TotalUp, st1.TotalDown)
	}
	if st1.Up != 1000 || st1.Down != 2000 {
		t.Fatalf("第 2 次瞬时速率计算不符合预期: Up=%d, Down=%d", st1.Up, st1.Down)
	}

	// 第 3 次采样：再隔 1 秒产生 500 字节上传与 1000 字节下载
	t2 := t1.Add(1 * time.Second)
	_ = os.WriteFile(rxFile, []byte("500003000"), 0644)
	_ = os.WriteFile(txFile, []byte("200001500"), 0644)
	tracker.fallbackSample(t2)

	st2 := tracker.GetStats()
	if st2.TotalUp != 1500 || st2.TotalDown != 3000 {
		t.Fatalf("第 3 次差量累积不符合预期: TotalUp=%d (预期 1500), TotalDown=%d (预期 3000)", st2.TotalUp, st2.TotalDown)
	}
}
