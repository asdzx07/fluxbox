package main

import (
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type InterfaceInfo struct {
	Alias string `json:"alias"`
	IP    string `json:"ip"`
	Index int    `json:"index"`
}

var (
	statusMu      sync.Mutex
	cachedConn    bool
	lastCheckTime time.Time
	pingMu        sync.Mutex
	cachedLatency *int
	lastPingTime  time.Time
)

func runHiddenCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func getActiveInterface() InterfaceInfo {
	ifaces, err := net.Interfaces()
	if err != nil {
		return InterfaceInfo{Alias: "以太网", IP: "192.168.3.25", Index: 19}
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(iface.Name)
		if strings.Contains(name, "loopback") || strings.Contains(name, "vethernet") ||
			strings.Contains(name, "virtual") || strings.Contains(name, "tun") ||
			strings.Contains(name, "tap") || strings.Contains(name, "wsl") {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				if ip4 := ipNet.IP.To4(); ip4 != nil {
					return InterfaceInfo{
						Alias: iface.Name,
						IP:    ip4.String(),
						Index: iface.Index,
					}
				}
			}
		}
	}

	return InterfaceInfo{Alias: "以太网", IP: "192.168.3.25", Index: 19}
}

func isConnectedToGateway(gatewayIP string) bool {
	statusMu.Lock()
	defer statusMu.Unlock()

	if time.Since(lastCheckTime) < 1*time.Second {
		return cachedConn
	}

	out, err := runHiddenCommand("route", "print", "0.0.0.0")
	if err != nil {
		return false
	}

	connected := parseRoutePrint(out, gatewayIP)
	cachedConn = connected
	lastCheckTime = time.Now()
	return connected
}

func setGatewayCache(connected bool) {
	statusMu.Lock()
	defer statusMu.Unlock()
	cachedConn = connected
	lastCheckTime = time.Now()
}

func parseRoutePrint(out, gatewayIP string) bool {
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 5 && fields[0] == "0.0.0.0" && fields[1] == "0.0.0.0" && fields[2] == gatewayIP {
			return true
		}
	}
	return false
}

func connectGateway(gatewayIP string) error {
	iface := getActiveInterface()
	alias := iface.Alias
	ip := iface.IP

	// 1. 设置网卡静态网关为旁路由
	_, _ = runHiddenCommand("netsh", "interface", "ip", "set", "address", fmt.Sprintf("name=%s", alias), "static", ip, "255.255.255.0", gatewayIP, "1")

	// 2. 设置唯一 DNS 为旁路由
	_, _ = runHiddenCommand("netsh", "interface", "ip", "set", "dns", fmt.Sprintf("name=%s", alias), "static", gatewayIP)

	// 3. 刷新 DNS 缓存
	_, _ = runHiddenCommand("ipconfig", "/flushdns")

	setGatewayCache(true)
	return nil
}

func disconnectGateway(gatewayIP string) error {
	iface := getActiveInterface()
	names := []string{iface.Alias, "以太网", "WLAN", "Wi-Fi", "Ethernet"}
	seen := make(map[string]bool)

	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		_, _ = runHiddenCommand("netsh", "interface", "ip", "set", "address", fmt.Sprintf("name=%s", name), "source=dhcp")
		_, _ = runHiddenCommand("netsh", "interface", "ip", "set", "dns", fmt.Sprintf("name=%s", name), "source=dhcp")
	}

	_, _ = runHiddenCommand("route", "delete", "0.0.0.0", "mask", "128.0.0.0", gatewayIP)
	_, _ = runHiddenCommand("route", "delete", "128.0.0.0", "mask", "128.0.0.0", gatewayIP)
	_, _ = runHiddenCommand("route", "delete", "0.0.0.0", gatewayIP)
	_, _ = runHiddenCommand("ipconfig", "/flushdns")

	setGatewayCache(false)
	return nil
}

var pingRe = regexp.MustCompile(`(?i)(?:time|时间)[=<](\d+)ms`)

func testPing(gatewayIP string) *int {
	pingMu.Lock()
	defer pingMu.Unlock()

	if time.Since(lastPingTime) < 3*time.Second && cachedLatency != nil {
		return cachedLatency
	}
	lastPingTime = time.Now()

	go func() {
		out, err := runHiddenCommand("ping", "-n", "1", "-w", "600", gatewayIP)
		pingMu.Lock()
		defer pingMu.Unlock()
		if err == nil {
			m := pingRe.FindStringSubmatch(out)
			if len(m) >= 2 {
				lat, err := strconv.Atoi(m[1])
				if err == nil {
					cachedLatency = &lat
					return
				}
			}
			val := 1
			cachedLatency = &val
		} else {
			cachedLatency = nil
		}
	}()

	return cachedLatency
}
