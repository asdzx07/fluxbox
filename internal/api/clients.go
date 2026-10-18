package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"fluxbox/internal/store"
)

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		clients := s.store.GetClients()
		if len(clients) == 0 {
			// 若为空尝试扫描一次
			clients = scanArpClients()
			_ = s.store.SaveClients(clients)
		}
		s.writeJSON(w, map[string]any{"ok": true, "clients": clients})

	case http.MethodPost:
		var req struct {
			Clients []store.ClientInfo `json:"clients"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "无效的 clients 数组")
			return
		}

		_ = s.store.SaveClients(req.Clients)

		// 实时同步至 Linux 内核 eBPF lan_clients_map
		if s.bpfEngine != nil {
			for _, c := range req.Clients {
				_ = s.bpfEngine.UpdateClientMode(c.IP, c.Mode)
			}
		}

		s.writeJSON(w, map[string]any{"ok": true, "clients": req.Clients})
	}
}

func (s *Server) handleClientsScan(w http.ResponseWriter, r *http.Request) {
	clients := scanArpClients()
	saved := s.store.GetClients()

	modeMap := make(map[string]string)
	nameMap := make(map[string]string)
	for _, c := range saved {
		modeMap[c.IP] = c.Mode
		nameMap[c.IP] = c.Name
	}

	for i := range clients {
		if mode, ok := modeMap[clients[i].IP]; ok {
			clients[i].Mode = mode
		}
		if name, ok := nameMap[clients[i].IP]; ok && name != "" {
			clients[i].Name = name
		}
	}

	_ = s.store.SaveClients(clients)
	s.writeJSON(w, map[string]any{"ok": true, "clients": clients, "count": len(clients)})
}

func (s *Server) handleClientDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/clients/")
	saved := s.store.GetClients()
	var kept []store.ClientInfo
	var deletedIP string
	for _, c := range saved {
		if c.ID != id && c.IP != id {
			kept = append(kept, c)
		} else {
			deletedIP = c.IP
		}
	}
	_ = s.store.SaveClients(kept)

	if deletedIP != "" && s.bpfEngine != nil {
		_ = s.bpfEngine.UpdateClientMode(deletedIP, "rule")
	}

	s.writeJSON(w, map[string]any{"ok": true, "clients": kept})
}

func scanArpClients() []store.ClientInfo {
	var list []store.ClientInfo

	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return list
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	isFirst := true
	for scanner.Scan() {
		if isFirst {
			isFirst = false // 忽略标题行
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 4 {
			ip := fields[0]
			flags := fields[2]
			mac := fields[3]
			if flags == "0x2" && mac != "00:00:00:00:00:00" {
				list = append(list, store.ClientInfo{
					ID:     "c-" + strings.ReplaceAll(mac, ":", ""),
					Name:   ip,
					IP:     ip,
					Mode:   "rule",
					Online: true,
				})
			}
		}
	}
	return list
}
