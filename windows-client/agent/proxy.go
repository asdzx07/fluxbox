package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var cookieRe = regexp.MustCompile(`(?i)(session|mybox_session|fluxbox_session)=([^;]+)`)

func loginRemoteGateway(password, gatewayIP string, gatewayPort int) (bool, string, error) {
	if password == "" {
		return false, "密码不能为空", nil
	}

	reqBody, _ := json.Marshal(map[string]string{"password": password})
	targetURL := fmt.Sprintf("http://%s:%d/api/auth/login", gatewayIP, gatewayPort)

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(reqBody))
	if err != nil {
		return false, err.Error(), err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("连接旁路由失败 (%v)", err), err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var tokenVal string
		for _, c := range resp.Cookies() {
			if strings.EqualFold(c.Name, "session") || strings.EqualFold(c.Name, "mybox_session") || strings.EqualFold(c.Name, "fluxbox_session") {
				tokenVal = c.Value
				break
			}
		}
		if tokenVal == "" {
			for _, hdr := range resp.Header["Set-Cookie"] {
				m := cookieRe.FindStringSubmatch(hdr)
				if len(m) >= 3 {
					tokenVal = m[2]
					break
				}
			}
		}

		if tokenVal != "" {
			cookieHeader := fmt.Sprintf("session=%s; mybox_session=%s", tokenVal, tokenVal)
			updateConfig(func(c *Config) {
				c.Password = password
				c.SessionCookie = cookieHeader
				c.GatewayIP = gatewayIP
				c.GatewayPort = gatewayPort
			})
			return true, "登录成功", nil
		}
		return true, "登录成功 (免密)", nil
	} else if resp.StatusCode == http.StatusUnauthorized {
		return false, "密码错误，请检查旁路由管理密码", nil
	} else if resp.StatusCode == http.StatusConflict {
		return true, "旁路由无需密码", nil
	}

	body, _ := io.ReadAll(resp.Body)
	var errObj struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &errObj)
	if errObj.Error != "" {
		return false, errObj.Error, nil
	}
	return false, fmt.Sprintf("旁路由返回 HTTP %d", resp.StatusCode), nil
}

func handleRemoteAPIProxy(w http.ResponseWriter, r *http.Request) {
	cfg := getConfig()
	targetPath := strings.TrimPrefix(r.URL.Path, "/remote-api")
	targetURL := fmt.Sprintf("http://%s:%d/api%s", cfg.GatewayIP, cfg.GatewayPort, targetPath)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for k, vv := range r.Header {
		if strings.EqualFold(k, "Host") {
			continue
		}
		for _, v := range vv {
			proxyReq.Header.Add(k, v)
		}
	}
	proxyReq.Host = fmt.Sprintf("%s:%d", cfg.GatewayIP, cfg.GatewayPort)

	// 强制注入已保存的 Session Cookie（双重名称护航）
	if cfg.SessionCookie != "" {
		cookieVal := cfg.SessionCookie
		if strings.HasPrefix(cookieVal, "session=") && !strings.Contains(cookieVal, "mybox_session=") {
			token := strings.TrimPrefix(cookieVal, "session=")
			token = strings.Split(token, ";")[0]
			cookieVal = fmt.Sprintf("session=%s; mybox_session=%s", token, token)
		} else if strings.HasPrefix(cookieVal, "mybox_session=") && !strings.Contains(cookieVal, "session=") {
			token := strings.TrimPrefix(cookieVal, "mybox_session=")
			token = strings.Split(token, ";")[0]
			cookieVal = fmt.Sprintf("session=%s; mybox_session=%s", token, token)
		}
		proxyReq.Header.Set("Cookie", cookieVal)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": fmt.Sprintf("连接旁路由失败 (%v)", err),
		})
		return
	}
	defer resp.Body.Close()

	// 捕获可能刷新的 Set-Cookie
	if setCookies := resp.Header["Set-Cookie"]; len(setCookies) > 0 {
		for _, sc := range setCookies {
			m := cookieRe.FindStringSubmatch(sc)
			if len(m) >= 3 {
				token := m[2]
				updateConfig(func(c *Config) {
					c.SessionCookie = fmt.Sprintf("session=%s; mybox_session=%s", token, token)
				})
				break
			}
		}
	}

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
