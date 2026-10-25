package api

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"fluxbox/internal/configgen"
	"fluxbox/internal/dnsmasq"
	"fluxbox/internal/kernel"
	"fluxbox/internal/netstack"
	"fluxbox/internal/store"
)


func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Restart bool `json:"restart"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := s.DeployCore(req.Restart); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "message": "部署成功"})
}

func (s *Server) handleTeardown(w http.ResponseWriter, r *http.Request) {
	_ = s.supervisor.Stop()
	_ = netstack.Teardown()
	_ = dnsmasq.Restore()
	s.writeJSON(w, map[string]any{"ok": true, "message": "网络规则已彻底还原，服务已停止"})
}

func (s *Server) handleKernelAction(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, "/api/kernel/")

	switch action {
	case "start":
		if err := s.supervisor.Start(); err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = netstack.Setup()
		_ = dnsmasq.Hijack(s.store.GetSettings().DNS.HijackPort)
		s.writeJSON(w, map[string]any{"ok": true, "running": true})

	case "stop":
		_ = s.supervisor.Stop()
		_ = netstack.Teardown()
		_ = dnsmasq.Restore()
		s.writeJSON(w, map[string]any{"ok": true, "running": false})

	case "restart":
		if err := s.supervisor.Restart(); err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = netstack.Setup()
		_ = dnsmasq.Hijack(s.store.GetSettings().DNS.HijackPort)
		s.writeJSON(w, map[string]any{"ok": true, "running": true})

	case "install":
		s.handleKernelInstall(w, r)
		return

	default:
		s.writeError(w, http.StatusBadRequest, "未知内核操作")
	}
}

func (s *Server) handleKernelLog(w http.ResponseWriter, r *http.Request) {
	lines := 200
	if lStr := r.URL.Query().Get("lines"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			lines = l
		}
	}
	s.writeJSON(w, map[string]any{"log": s.supervisor.GetLogs(lines)})
}

func (s *Server) handleKernelLatest(w http.ResponseWriter, r *http.Request) {
	cur := s.supervisor.Version()
	latest := queryLatestSingBoxTag()
	if latest == "" {
		latest = cur
	}

	cleanCur := cleanVersionTag(cur)
	cleanLat := cleanVersionTag(latest)

	hasUpdate := false
	if cleanCur != "" && cleanLat != "" && cleanCur != cleanLat {
		if isVersionHigher(cleanLat, cleanCur) {
			hasUpdate = true
		}
	}

	displayCur := cleanCur
	if displayCur != "" && !strings.HasPrefix(displayCur, "v") {
		displayCur = "v" + displayCur
	}
	displayLatest := cleanLat
	if displayLatest != "" && !strings.HasPrefix(displayLatest, "v") {
		displayLatest = "v" + displayLatest
	}

	s.writeJSON(w, map[string]any{
		"ok":        true,
		"current":   displayCur,
		"latest":    displayLatest,
		"hasUpdate": hasUpdate,
	})
}

func cleanVersionTag(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "sing-box version ")
	s = strings.TrimPrefix(s, "sing-box ")
	fields := strings.Fields(s)
	if len(fields) > 0 {
		s = fields[0]
	}
	return strings.TrimPrefix(s, "v")
}

func isVersionHigher(target, current string) bool {
	tBase, tPre := splitPre(target)
	cBase, cPre := splitPre(current)

	tNums := parseVersionNums(tBase)
	cNums := parseVersionNums(cBase)

	for i := 0; i < 3; i++ {
		if tNums[i] > cNums[i] {
			return true
		}
		if tNums[i] < cNums[i] {
			return false
		}
	}

	// 主版本号相同情况
	if tPre == "" && cPre != "" {
		return true // 正式版高于预发布版
	}
	if tPre != "" && cPre == "" {
		return false // 预发布版低于正式版
	}
	if tPre == cPre {
		return false
	}

	// 比较 pre-release 内部数字 (例如 alpha.13 > alpha.12)
	tPreNum := extractTrailingNumber(tPre)
	cPreNum := extractTrailingNumber(cPre)
	return tPreNum > cPreNum
}

func splitPre(v string) (base, pre string) {
	if idx := strings.Index(v, "-"); idx >= 0 {
		return v[:idx], v[idx+1:]
	}
	return v, ""
}

func parseVersionNums(v string) [3]int {
	var nums [3]int
	parts := strings.Split(v, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		nums[i], _ = strconv.Atoi(parts[i])
	}
	return nums
}

func extractTrailingNumber(s string) int {
	parts := strings.Split(s, ".")
	if len(parts) > 0 {
		n, _ := strconv.Atoi(parts[len(parts)-1])
		return n
	}
	return 0
}

func (s *Server) handleKernelInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	version := strings.TrimSpace(req.Version)
	if version == "" || version == "latest" {
		version = queryLatestSingBoxTag()
		if version == "" {
			version = s.supervisor.Version()
		}
	}
	if version == "" {
		s.writeError(w, http.StatusBadRequest, "未能获取到有效的目标版本号")
		return
	}

	binPath := s.supervisor.GetBinPath()
	if binPath == "" {
		binPath = "/opt/fluxbox/bin/sing-box"
	}

	if err := installSingBoxBinary(version, binPath); err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("内核更新失败: %v", err))
		return
	}

	_ = s.supervisor.Restart()
	newVer := s.supervisor.Version()

	s.writeJSON(w, map[string]any{
		"ok":        true,
		"version":   newVer,
		"restarted": true,
	})
}

func queryLatestSingBoxTag() string {
	client := &http.Client{Timeout: 5 * time.Second}
	urls := []string{
		"https://api.github.com/repos/SagerNet/sing-box/releases?per_page=10",
		"https://ghfast.top/https://api.github.com/repos/SagerNet/sing-box/releases?per_page=10",
	}

	for _, u := range urls {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "FluxBox")
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var list []struct {
				TagName    string `json:"tag_name"`
				Prerelease bool   `json:"prerelease"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&list); err == nil && len(list) > 0 {
				resp.Body.Close()
				// 优先寻找最新的 1.15 预发布版本
				for _, r := range list {
					if strings.HasPrefix(r.TagName, "v1.15.") || strings.HasPrefix(r.TagName, "1.15.") {
						return r.TagName
					}
				}
				// 兜底返回排在最前面的最新发布项（非固定稳定版）
				return list[0].TagName
			}
			resp.Body.Close()
		}
	}
	return ""
}


func installSingBoxBinary(tag, targetBinPath string) error {
	rawVer := strings.TrimPrefix(tag, "v")
	arch := runtime.GOARCH
	archMap := map[string]string{
		"amd64": "amd64",
		"arm64": "arm64",
		"arm":   "armv7",
		"386":   "386",
	}
	a, ok := archMap[arch]
	if !ok {
		a = "amd64"
	}

	archiveName := fmt.Sprintf("sing-box-%s-linux-%s.tar.gz", rawVer, a)
	urls := []string{
		fmt.Sprintf("https://github.com/SagerNet/sing-box/releases/download/%s/%s", tag, archiveName),
		fmt.Sprintf("https://ghfast.top/https://github.com/SagerNet/sing-box/releases/download/%s/%s", tag, archiveName),
	}

	client := &http.Client{Timeout: 90 * time.Second}
	var resp *http.Response
	var err error

	for _, u := range urls {
		resp, err = client.Get(u)
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
	}

	if resp == nil || resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载内核归档包失败 (HTTP %v)", err)
	}
	defer resp.Body.Close()

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("解压 gzip 失败: %v", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	var found bool
	tmpTarget := targetBinPath + ".new"

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 tar 失败: %v", err)
		}

		if !header.FileInfo().IsDir() && (header.Name == "sing-box" || strings.HasSuffix(header.Name, "/sing-box")) {
			outFile, err := os.OpenFile(tmpTarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return fmt.Errorf("写入临时文件失败: %v", err)
			}
			_, copyErr := io.Copy(outFile, tarReader)
			outFile.Close()
			if copyErr != nil {
				_ = os.Remove(tmpTarget)
				return fmt.Errorf("解压提取 sing-box 失败: %v", copyErr)
			}
			found = true
			break
		}
	}

	if !found {
		_ = os.Remove(tmpTarget)
		return fmt.Errorf("在下载的归档包中未找到 sing-box 可执行文件")
	}

	if err := os.Rename(tmpTarget, targetBinPath); err != nil {
		// 跨分区 fallback
		data, err := os.ReadFile(tmpTarget)
		if err != nil {
			return err
		}
		if err := os.WriteFile(targetBinPath, data, 0755); err != nil {
			return err
		}
		_ = os.Remove(tmpTarget)
	}

	kernel.EnsureMuslCompat()
	log.Printf("🎉 sing-box %s 已成功安装至 %s", tag, targetBinPath)
	return nil
}


func (s *Server) handleRulesetSubs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st := s.store.GetSettings()
		s.writeJSON(w, map[string]any{"ok": true, "items": st.RulesetSubs})

	case http.MethodPost:
		var req struct {
			Tag    string `json:"tag"`
			URL    string `json:"url"`
			Format string `json:"format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Tag == "" || req.URL == "" {
			s.writeError(w, http.StatusBadRequest, "缺少 tag 或 url")
			return
		}

		item := configgen.RulesetSub{
			ID:      newID("rs"),
			Tag:     strings.TrimSpace(req.Tag),
			URL:     strings.TrimSpace(req.URL),
			Format:  req.Format,
			Enabled: true,
		}
		if item.Format != "source" {
			item.Format = "binary"
		}

		_ = s.store.MutateSettings(func(st *store.FullSettings) {
			st.RulesetSubs = append(st.RulesetSubs, item)
		})
		s.writeJSON(w, map[string]any{"ok": true, "item": item})
	}
}

func (s *Server) handleRulesetSubAction(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/ruleset-subs/")

	switch r.Method {
	case http.MethodPut:
		var req struct {
			Tag     *string `json:"tag"`
			URL     *string `json:"url"`
			Format  *string `json:"format"`
			Enabled *bool   `json:"enabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		_ = s.store.MutateSettings(func(st *store.FullSettings) {
			for i := range st.RulesetSubs {
				if st.RulesetSubs[i].ID == id {
					if req.Tag != nil {
						st.RulesetSubs[i].Tag = strings.TrimSpace(*req.Tag)
					}
					if req.URL != nil {
						st.RulesetSubs[i].URL = strings.TrimSpace(*req.URL)
					}
					if req.Format != nil {
						st.RulesetSubs[i].Format = *req.Format
					}
					if req.Enabled != nil {
						st.RulesetSubs[i].Enabled = *req.Enabled
					}
					break
				}
			}
		})
		s.writeJSON(w, map[string]any{"ok": true})

	case http.MethodDelete:
		_ = s.store.MutateSettings(func(st *store.FullSettings) {
			var kept []configgen.RulesetSub
			for _, item := range st.RulesetSubs {
				if item.ID != id {
					kept = append(kept, item)
				}
			}
			st.RulesetSubs = kept
		})
		s.writeJSON(w, map[string]any{"ok": true})
	}
}

func (s *Server) handleRulesetsStatus(w http.ResponseWriter, r *http.Request) {
	st := s.store.GetSettings()
	var formattedDate string
	if st.Meta.RulesetUpdatedAt != nil && *st.Meta.RulesetUpdatedAt > 0 {
		formattedDate = time.Unix(*st.Meta.RulesetUpdatedAt, 0).Format("2006-01-02 15:04")
	} else {
		// 检查本地 cache.db 的修改时间作为默认兜底
		cachePath := filepath.Join(s.store.GetDataDir(), "cache.db")
		if fi, err := os.Stat(cachePath); err == nil && fi.Size() > 0 {
			formattedDate = fi.ModTime().Format("2006-01-02 15:04")
		}
	}

	var items []map[string]any
	for _, sub := range st.RulesetSubs {
		subDate := sub.FormattedDate
		if subDate == "" {
			subDate = formattedDate
		}
		items = append(items, map[string]any{
			"id":            sub.ID,
			"tag":           sub.Tag,
			"url":           sub.URL,
			"format":        sub.Format,
			"enabled":       sub.Enabled,
			"status":        "active",
			"formattedDate": subDate,
		})
	}
	s.writeJSON(w, map[string]any{
		"ok":            true,
		"formattedDate": formattedDate,
		"items":         items,
	})
}

func (s *Server) handleRulesetsRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tag string `json:"tag"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	// 1. 删除旧的规则集缓存，强迫 sing-box 重新从远端下载最新规则文件
	cachePath := filepath.Join(s.store.GetDataDir(), "cache.db")
	_ = os.Remove(cachePath)

	// 2. 重新部署并拉起 sing-box 内核
	if err := s.DeployCore(true); err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("重新部署内核失败: %v", err))
		return
	}

	// 3. 记录更新时间戳并持久化
	now := time.Now().Unix()
	formatted := time.Now().Format("2006-01-02 15:04")
	_ = s.store.MutateSettings(func(st *store.FullSettings) {
		st.Meta.RulesetUpdatedAt = &now
		for i := range st.RulesetSubs {
			if req.Tag == "" || st.RulesetSubs[i].Tag == req.Tag {
				st.RulesetSubs[i].UpdatedAt = &now
				st.RulesetSubs[i].FormattedDate = formatted
			}
		}
	})

	s.writeJSON(w, map[string]any{
		"ok":            true,
		"hasUpdate":     true,
		"formattedDate": formatted,
		"updatedCount":  1,
		"message":       "规则集已成功刷新并重新部署",
	})
}

func (s *Server) handleAdblockRefresh(w http.ResponseWriter, r *http.Request) {
	// 1. 删除旧规则集缓存
	cachePath := filepath.Join(s.store.GetDataDir(), "cache.db")
	_ = os.Remove(cachePath)

	// 2. 重新部署并拉起 sing-box 内核
	if err := s.DeployCore(true); err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("重新部署内核失败: %v", err))
		return
	}

	// 3. 记录更新时间戳并持久化
	now := time.Now().Unix()
	formatted := time.Now().Format("2006-01-02 15:04")
	_ = s.store.MutateSettings(func(st *store.FullSettings) {
		st.Meta.RulesetUpdatedAt = &now
	})

	s.writeJSON(w, map[string]any{
		"ok":            true,
		"hasUpdate":     true,
		"formattedDate": formatted,
		"message":       "广告拦截规则集已成功重新拉取并生效",
	})
}
