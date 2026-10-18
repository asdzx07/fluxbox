package store

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fluxbox/internal/configgen"
	"fluxbox/internal/subscription"
)

type PanelAuth struct {
	PasswordHash  string `json:"passwordHash"`
	PasswordSalt  string `json:"passwordSalt"`
	SessionSecret string `json:"sessionSecret"`
}

type NetworkConfig struct {
	IPv6           bool   `json:"ipv6"`
	RejectQuic     bool   `json:"rejectQuic"`
	DirectBypass   bool   `json:"directBypass"`
	DirectForNodes bool   `json:"directForNodes"`
	Sniff          bool   `json:"sniff"`
	BypassPorts    string `json:"bypassPorts"`
}

type DNSConfig struct {
	Mode          string   `json:"mode"`
	Split         bool     `json:"split"`
	Direct        string   `json:"direct"`
	DirectAddress string   `json:"directAddress"`
	Proxy         string   `json:"proxy"`
	ProxyProtocol string   `json:"proxyProtocol"`
	FakeIP        bool     `json:"fakeIp"`
	FakeIPRange   string   `json:"fakeIpRange"`
	Strategy      string   `json:"strategy"`
	HijackPort    int      `json:"hijackPort"`
	Adblock       bool     `json:"adblock"`
	AdblockAllow  []string `json:"adblockAllow"`
	AdblockCustom []string `json:"adblockCustom"`
}

type KernelConfig struct {
	Version     string `json:"version"`
	AutoStart   bool   `json:"autoStart"`
	LogLevel    string `json:"logLevel"`
	ClashSecret string `json:"clashSecret"`
}

type MetaConfig struct {
	LastDeployAt    *int64 `json:"lastDeployAt"`
	LastDeployError string `json:"lastDeployError"`
}

type FullSettings struct {
	Panel         PanelAuth                 `json:"panel"`
	Kernel        KernelConfig              `json:"kernel"`
	Network       NetworkConfig             `json:"network"`
	DNS           DNSConfig                 `json:"dns"`
	Subscriptions []subscription.Subscription `json:"subscriptions"`
	RulesetSubs   []configgen.RulesetSub    `json:"rulesetSubs"`
	Nodes         []map[string]any          `json:"nodes"`
	Groups        []configgen.GroupConfig   `json:"groups"`
	Policies      []configgen.PolicyConfig  `json:"policies"`
	Meta          MetaConfig                `json:"meta"`
}

type ClientInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Mode   string `json:"mode"` // "rule" | "direct" | "proxy"
	Online bool   `json:"online"`
}

type Store struct {
	mu       sync.RWMutex
	dataDir  string
	settings FullSettings
	clients  []ClientInfo
}

func DefaultPolicies() []configgen.PolicyConfig {
	return []configgen.PolicyConfig{
		{
			ID:     "p-speed",
			Name:   "测速",
			Target: "all-manual",
			DomainSuffix: []string{
				"speedtest.net", "speedtest.cn", "fast.com", "speedcheck.org", "ustc.edu.cn",
				"ipip.net", "ipinfo.io", "ip-api.com", "ipleak.net", "ip-score.com", "ipw.cn",
				"ifconfig.me", "ip.sb", "ipify.org", "ip125.com", "ip111.cn", "ping0.cc",
				"browserleaks.com", "browserleaks.org", "dnsleaktest.com", "whoer.net",
			},
			Enabled: true,
		},
		{ID: "p-ai", Name: "AI", Target: "all-auto", Rulesets: []string{"geosite-category-ai-!cn"}, Enabled: true},
		{ID: "p-youtube", Name: "Youtube", Target: "all-auto", Rulesets: []string{"geosite-youtube"}, Enabled: true},
		{ID: "p-tiktok", Name: "TikTok", Target: "all-auto", Rulesets: []string{"geosite-tiktok", "geosite-tiktok@!cn"}, Enabled: true},
		{ID: "p-netflix", Name: "Netflix", Target: "all-auto", Rulesets: []string{"geosite-netflix"}, Enabled: true},
		{ID: "p-github", Name: "Github", Target: "all-auto", Rulesets: []string{"geosite-github"}, Enabled: true},
		{ID: "p-google", Name: "Google", Target: "all-auto", Rulesets: []string{"geosite-google"}, Enabled: true},
		{ID: "p-microsoft", Name: "Microsoft", Target: "all-auto", Rulesets: []string{"geosite-microsoft"}, Enabled: true},
		{ID: "p-apple", Name: "Apple", Target: "all-auto", Rulesets: []string{"geosite-apple"}, Enabled: true},
		{ID: "p-games", Name: "游戏", Target: "all-auto", Rulesets: []string{"geosite-steam", "geosite-sony"}, Enabled: true},
		{ID: "p-cn", Name: "国内", Target: "builtin-direct", Rulesets: []string{"geosite-cn"}, Enabled: true},
		{ID: "p-cnip", Name: "国内IP", Target: "builtin-direct", Rulesets: []string{"geoip-cn"}, Enabled: true},
	}
}

func DefaultGroups() []configgen.GroupConfig {
	return []configgen.GroupConfig{
		{ID: "all-auto", Name: "所有-自动", Type: "urltest", Mode: "dynamic", Enabled: true},
		{ID: "all-manual", Name: "所有-手动", Type: "selector", Mode: "dynamic", Enabled: true},
	}
}

func DefaultSettings() FullSettings {
	secBytes := make([]byte, 32)
	_, _ = rand.Read(secBytes)

	s := FullSettings{
		Panel: PanelAuth{
			SessionSecret: hex.EncodeToString(secBytes),
		},
		Kernel: KernelConfig{
			AutoStart: true,
			LogLevel:  "warn",
		},
		Network: NetworkConfig{
			IPv6:           false,
			RejectQuic:     true,
			DirectBypass:   true,
			DirectForNodes: true,
			Sniff:          true,
		},
		DNS: DNSConfig{
			Mode:          "dnsmasq",
			Split:         true,
			Direct:        "wan",
			DirectAddress: "223.5.5.5",
			Proxy:         "1.1.1.1",
			ProxyProtocol: "tcp",
			FakeIP:        true,
			FakeIPRange:   "198.19.0.0/16",
			Strategy:      "prefer_ipv4",
			HijackPort:    36053,
		},
		Subscriptions: []subscription.Subscription{},
		RulesetSubs:   []configgen.RulesetSub{},
		Nodes:         []map[string]any{},
		Groups:        DefaultGroups(),
		Policies:      DefaultPolicies(),
	}
	return s
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	st := &Store{
		dataDir: dataDir,
		settings: DefaultSettings(),
	}

	_ = st.loadFromDisk()
	return st, nil
}

func (s *Store) loadFromDisk() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	settingsPath := filepath.Join(s.dataDir, "settings.json")
	if data, err := os.ReadFile(settingsPath); err == nil {
		var loaded FullSettings
		if err := json.Unmarshal(data, &loaded); err == nil {
			// 保留默认值兜底
			if len(loaded.Groups) == 0 {
				loaded.Groups = DefaultGroups()
			}
			if len(loaded.Policies) == 0 {
				loaded.Policies = DefaultPolicies()
			}
			if loaded.DNS.HijackPort == 0 || loaded.DNS.HijackPort == 7853 {
				loaded.DNS.HijackPort = 36053
			}
			s.settings = loaded
		}
	} else {
		// 首次生成初始配置
		s.atomicSaveLocked(settingsPath, s.settings)
	}

	clientsPath := filepath.Join(s.dataDir, "clients.json")
	if data, err := os.ReadFile(clientsPath); err == nil {
		var loadedClients []ClientInfo
		if err := json.Unmarshal(data, &loadedClients); err == nil {
			s.clients = loadedClients
		}
	}

	return nil
}

func (s *Store) atomicSaveLocked(target string, v any) error {
	tmp := target + ".tmp"
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func (s *Store) SaveSettings() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.atomicSaveLocked(filepath.Join(s.dataDir, "settings.json"), s.settings)
}

func (s *Store) GetSettings() FullSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *Store) MutateSettings(fn func(*FullSettings)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.settings)
	return s.atomicSaveLocked(filepath.Join(s.dataDir, "settings.json"), s.settings)
}

func (s *Store) GetClients() []ClientInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]ClientInfo, len(s.clients))
	copy(res, s.clients)
	return res
}

func (s *Store) SaveClients(clients []ClientInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients = clients
	return s.atomicSaveLocked(filepath.Join(s.dataDir, "clients.json"), s.clients)
}

// 转换给 configgen 模块使用的 Settings 格式
func (s *Store) ToConfiggenSettings() configgen.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var cfg configgen.Settings
	cfg.Network.IPv6 = s.settings.Network.IPv6
	cfg.Network.RejectQuic = s.settings.Network.RejectQuic
	cfg.Network.DirectBypass = s.settings.Network.DirectBypass
	cfg.Network.DirectForNodes = s.settings.Network.DirectForNodes

	cfg.DNS.Mode = s.settings.DNS.Mode
	cfg.DNS.Direct = s.settings.DNS.Direct
	cfg.DNS.DirectAddress = s.settings.DNS.DirectAddress
	cfg.DNS.Proxy = s.settings.DNS.Proxy
	cfg.DNS.Strategy = s.settings.DNS.Strategy
	cfg.DNS.ProxyProtocol = s.settings.DNS.ProxyProtocol
	cfg.DNS.Split = s.settings.DNS.Split
	cfg.DNS.FakeIP = s.settings.DNS.FakeIP
	cfg.DNS.Adblock = s.settings.DNS.Adblock
	cfg.DNS.AdblockAllow = s.settings.DNS.AdblockAllow
	cfg.DNS.AdblockCustom = s.settings.DNS.AdblockCustom

	cfg.Kernel.LogLevel = s.settings.Kernel.LogLevel
	return cfg
}

func (s *Store) GetDataDir() string {
	return s.dataDir
}

// --- 认证相关 ---

func (s *Store) IsPasswordSet() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.Panel.PasswordHash != ""
}

func (s *Store) SetPassword(password string) error {
	if len(password) < 4 {
		return fmt.Errorf("密码长度至少 4 位")
	}
	saltBytes := make([]byte, 16)
	_, _ = rand.Read(saltBytes)
	saltHex := hex.EncodeToString(saltBytes)

	hash := sha256.Sum256([]byte(password + ":" + saltHex))
	hashHex := hex.EncodeToString(hash[:])

	return s.MutateSettings(func(st *FullSettings) {
		st.Panel.PasswordSalt = saltHex
		st.Panel.PasswordHash = hashHex
	})
}

func (s *Store) VerifyPassword(password string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.settings.Panel.PasswordHash == "" {
		return true // 未设密码直接放行
	}
	hash := sha256.Sum256([]byte(password + ":" + s.settings.Panel.PasswordSalt))
	computed := hex.EncodeToString(hash[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(s.settings.Panel.PasswordHash)) == 1
}

func (s *Store) IssueToken() string {
	s.mu.RLock()
	secret := s.settings.Panel.SessionSecret
	s.mu.RUnlock()

	ts := time.Now().Unix()
	msg := fmt.Sprintf("%d:%s", ts, secret)
	sig := sha256.Sum256([]byte(msg))
	return fmt.Sprintf("%d.%s", ts, hex.EncodeToString(sig[:16]))
}

func (s *Store) VerifyToken(token string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.settings.Panel.PasswordHash == "" {
		return true // 未设密码无需鉴权
	}
	var ts int64
	var sigHex string
	_, err := fmt.Sscanf(token, "%d.%s", &ts, &sigHex)
	if err != nil {
		return false
	}
	// Token 7 天内有效
	if time.Now().Unix()-ts > 7*86400 {
		return false
	}
	msg := fmt.Sprintf("%d:%s", ts, s.settings.Panel.SessionSecret)
	sig := sha256.Sum256([]byte(msg))
	expected := hex.EncodeToString(sig[:16])
	return subtle.ConstantTimeCompare([]byte(sigHex), []byte(expected)) == 1
}
