package api

import (
	"encoding/json"
	"net/http"
	"time"
)

type failAttempt struct {
	startedAt time.Time
	count     int
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	isAuthed := false
	if !s.store.IsPasswordSet() {
		isAuthed = true
	} else if cookie, err := r.Cookie("session"); err == nil {
		isAuthed = s.store.VerifyToken(cookie.Value)
	}

	s.writeJSON(w, map[string]any{
		"enabled":       true,
		"authenticated": isAuthed,
		"passwordSet":   s.store.IsPasswordSet(),
	})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{"authenticated": true})
}

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	if s.store.IsPasswordSet() {
		s.writeError(w, http.StatusConflict, "面板密码已设置")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "无效的请求体")
		return
	}
	if err := s.store.SetPassword(req.Password); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	token := s.store.IssueToken()
	setAuthCookies(w, token)
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.store.IsPasswordSet() {
		s.writeError(w, http.StatusConflict, "还没有设置面板密码")
		return
	}

	clientIP := r.RemoteAddr
	now := time.Now()

	// 简易限流：15 分钟内最多尝试 5 次
	if val, ok := s.loginFails.Load(clientIP); ok {
		att := val.(*failAttempt)
		if now.Sub(att.startedAt) < 15*time.Minute && att.count >= 5 {
			s.writeError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
			return
		}
	}

	var req struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if !s.store.VerifyPassword(req.Password) {
		val, _ := s.loginFails.LoadOrStore(clientIP, &failAttempt{startedAt: now, count: 0})
		att := val.(*failAttempt)
		att.count++
		s.writeError(w, http.StatusUnauthorized, "密码错误")
		return
	}

	s.loginFails.Delete(clientIP)
	token := s.store.IssueToken()
	setAuthCookies(w, token)
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	clearAuthCookies(w)
	s.writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleAuthChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "无效的请求体")
		return
	}

	if !s.store.VerifyPassword(req.Current) {
		s.writeError(w, http.StatusUnauthorized, "当前密码错误")
		return
	}

	if err := s.store.SetPassword(req.Next); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	token := s.store.IssueToken()
	setAuthCookies(w, token)
	s.writeJSON(w, map[string]any{"ok": true})
}

func setAuthCookies(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   7 * 86400,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "mybox_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   7 * 86400,
	})
}

func clearAuthCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "mybox_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

