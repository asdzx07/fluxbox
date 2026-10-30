//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	GatewayIP      string `json:"gatewayIp"`
	GatewayPort    int    `json:"gatewayPort"`
	Password       string `json:"password"`
	SessionCookie  string `json:"sessionCookie"`
	AutoConnect    bool   `json:"autoConnect"`
	MinimizeToTray bool   `json:"minimizeToTray"`
}

var (
	cfgMu      sync.RWMutex
	cfgPath    string
	currConfig Config
)

func initConfig(baseDir string) {
	cfgPath = filepath.Join(baseDir, "config.json")
	currConfig = Config{
		GatewayIP:      "192.168.3.2",
		GatewayPort:    52155,
		Password:       "",
		SessionCookie:  "",
		AutoConnect:    false,
		MinimizeToTray: true,
	}

	data, err := os.ReadFile(cfgPath)
	if err == nil {
		_ = json.Unmarshal(data, &currConfig)
		if currConfig.GatewayPort == 0 {
			currConfig.GatewayPort = 52155
		}
	} else {
		saveConfigLocked()
	}
}

func getConfig() Config {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return currConfig
}

func updateConfig(fn func(c *Config)) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	fn(&currConfig)
	saveConfigLocked()
}

func saveConfigLocked() {
	data, err := json.MarshalIndent(currConfig, "", "  ")
	if err == nil {
		_ = os.WriteFile(cfgPath, data, 0644)
	}
}
