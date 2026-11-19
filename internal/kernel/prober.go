package kernel

import (
	"context"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// ProberConfig 双向适应度探针配置
type ProberConfig struct {
	ProxyURL        string        // 代理地址，默认 http://127.0.0.1:7891
	PrimaryURL      string        // 首选探测端点，默认 http://cp.cloudflare.com/generate_204
	SecondaryURL    string        // 备选探测端点，默认 http://www.gstatic.com/generate_204
	Interval        time.Duration // 拨测基础周期，默认 45s
	JitterRange     time.Duration // 随机抖动范围，默认 10s (即 Interval ± 5s)
	Timeout         time.Duration // 单次拨测超时，默认 4s
	FailThreshold   int           // 判定降级熔断的连续失败次数，默认 3 次
	ResumeThreshold int           // 判定恢复正常的连续成功次数，默认 2 次
}

func DefaultProberConfig() ProberConfig {
	return ProberConfig{
		ProxyURL:        "http://127.0.0.1:7891",
		PrimaryURL:      "http://cp.cloudflare.com/generate_204",
		SecondaryURL:    "http://www.gstatic.com/generate_204",
		Interval:        45 * time.Second,
		JitterRange:     10 * time.Second,
		Timeout:         4 * time.Second,
		FailThreshold:   3,
		ResumeThreshold: 2,
	}
}

// ProbeResult 单次双向适应度评估结果
type ProbeResult struct {
	EvadeScore    float64       // C_evade: 是否越过审查阻断 (1.0 成功, 0.0 阻断/超时/RST)
	ValidScore    float64       // S_valid: 服务端响应是否合法有效 (1.0 正常204/200, 0.0 畸形/劫持/报错)
	Overhead      float64       // P_overhead: 网络开销惩罚 (0.0 ~ 0.1)
	Fitness       float64       // 最终适应度评分 F(S) = 0.5*C_evade + 0.5*S_valid - P_overhead
	RTT           time.Duration // 往返耗时
	StatusCode    int           // HTTP 状态码
	Error         error         // 错误信息
	TestedURL     string        // 实际探测 URL
	ExecutionTime time.Time     // 探测时间
}

// Prober 双向适应度探针
type Prober struct {
	mu     sync.RWMutex
	cfg    ProberConfig
	client *http.Client

	lastResult         ProbeResult
	consecutiveFails   int
	consecutiveSuccess int

	// 自定义探测函数钩子，便于单元测试 mock
	customProbeFn func(ctx context.Context) ProbeResult
}

func NewProber(cfg ProberConfig) *Prober {
	proxy, _ := url.Parse(cfg.ProxyURL)
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(proxy),
		DisableKeepAlives:     true, // 短连接探测，避免连接池复用掩盖连接重置
		ResponseHeaderTimeout: cfg.Timeout,
	}

	p := &Prober{
		cfg: cfg,
		client: &http.Client{
			Transport: transport,
			Timeout:   cfg.Timeout,
		},
	}
	return p
}

// SetCustomProbeFn 用于注入单元测试 mock
func (p *Prober) SetCustomProbeFn(fn func(ctx context.Context) ProbeResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.customProbeFn = fn
}

// ProbeOnce 执行一次双向适应度评估
// 严格对标 ACM CCS 2026 CircumVolve 论文公式: F(S) = w1*C_evade + w2*S_valid - w3*P_overhead
func (p *Prober) ProbeOnce(ctx context.Context) ProbeResult {
	p.mu.RLock()
	customFn := p.customProbeFn
	p.mu.RUnlock()

	if customFn != nil {
		res := customFn(ctx)
		p.recordResult(res)
		return res
	}

	targetURL := p.cfg.PrimaryURL
	res := p.doSingleProbe(ctx, targetURL)

	// 若首选用例失败，自动尝试备选端点，避免单点误判
	if res.Error != nil && p.cfg.SecondaryURL != "" {
		secRes := p.doSingleProbe(ctx, p.cfg.SecondaryURL)
		if secRes.Error == nil {
			res = secRes
		}
	}

	p.recordResult(res)
	return res
}

func (p *Prober) doSingleProbe(ctx context.Context, targetURL string) ProbeResult {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return ProbeResult{
			EvadeScore:    0.0,
			ValidScore:    0.0,
			Fitness:       0.0,
			Error:         err,
			TestedURL:     targetURL,
			ExecutionTime: time.Now(),
		}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := p.client.Do(req)
	rtt := time.Since(start)

	if err != nil {
		// 握手失败、收到 RST 或超时，判定审查阻断生效，适应度归零
		return ProbeResult{
			EvadeScore:    0.0,
			ValidScore:    0.0,
			Overhead:      0.1,
			Fitness:       0.0,
			RTT:           rtt,
			Error:         err,
			TestedURL:     targetURL,
			ExecutionTime: time.Now(),
		}
	}
	defer resp.Body.Close()

	// 1. 规避成功: 未阻断
	cEvade := 1.0

	// 2. 服务端有效性验证: 目标必须返回 204 No Content 或 200 OK
	sValid := 0.0
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		sValid = 1.0
	}

	// 3. 网络开销惩罚 (RTT 超过 500ms 开始轻微扣分，上限 0.1)
	overhead := 0.0
	if rtt > 500*time.Millisecond {
		ms := float64(rtt.Milliseconds() - 500)
		overhead = (ms / 3000.0) * 0.1
		if overhead > 0.1 {
			overhead = 0.1
		}
	}

	// 最终 CircumVolve 适应度得分
	fitness := (0.5 * cEvade) + (0.5 * sValid) - overhead
	if fitness < 0 {
		fitness = 0
	}
	if sValid == 0.0 {
		fitness = 0.0 // 伪规避或服务端报错一票否决
	}

	return ProbeResult{
		EvadeScore:    cEvade,
		ValidScore:    sValid,
		Overhead:      overhead,
		Fitness:       fitness,
		RTT:           rtt,
		StatusCode:    resp.StatusCode,
		TestedURL:     targetURL,
		ExecutionTime: time.Now(),
	}
}

func (p *Prober) recordResult(res ProbeResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastResult = res

	// 适应度低于 0.6 视为阻断或异常
	if res.Fitness < 0.6 || res.Error != nil {
		p.consecutiveFails++
		p.consecutiveSuccess = 0
	} else {
		p.consecutiveSuccess++
		p.consecutiveFails = 0
	}
}

// GetLastResult 获取最近一次拨测记录
func (p *Prober) GetLastResult() (ProbeResult, int, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastResult, p.consecutiveFails, p.consecutiveSuccess
}

// CalculateNextInterval 计算下一次探测间隔 (注入随机抖动，防止固定频率特征暴露)
func (p *Prober) CalculateNextInterval() time.Duration {
	base := p.cfg.Interval
	jitter := p.cfg.JitterRange
	if jitter <= 0 {
		return base
	}
	half := int64(jitter / 2)
	offset := time.Duration(rand.Int63n(half*2) - half)
	next := base + offset
	if next < 5*time.Second {
		next = 5 * time.Second
	}
	return next
}
