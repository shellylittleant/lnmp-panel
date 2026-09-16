package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"lnmp-panel/internal/auth"
	"lnmp-panel/internal/settings"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if locked, until, err := s.Auth.Locked(ip); err != nil {
		writeErr(w, 500, "internal", "锁定检查失败")
		return
	} else if locked {
		writeErr(w, 423, "locked", "该 IP 已锁定至 "+until.Format(time.RFC3339))
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	res, err := s.Auth.Login(ip, r.UserAgent(), in.Username, in.Password)
	if errors.Is(err, auth.ErrLocked) {
		writeErr(w, 423, "locked", "该 IP 已锁定")
		return
	}
	if errors.Is(err, auth.ErrInvalidCreds) {
		writeErr(w, 401, "unauthorized", "用户名或密码错误")
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "登录失败")
		return
	}
	if res.NeedTOTP {
		writeOK(w, map[string]any{"need_totp": true, "ticket": res.Ticket})
		return
	}
	s.setCookie(w, res.Session)
	writeOK(w, map[string]any{"need_totp": false})
}

func (s *Server) handleTOTPVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ticket string `json:"ticket"`
		Code   string `json:"code"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	sess, err := s.Auth.VerifyTOTPLogin(clientIP(r), r.UserAgent(), in.Ticket, in.Code)
	if errors.Is(err, auth.ErrLocked) {
		writeErr(w, 423, "locked", "该 IP 已锁定")
		return
	}
	if err != nil {
		writeErr(w, 401, "unauthorized", "验证码错误")
		return
	}
	s.setCookie(w, sess)
	writeOK(w, map[string]any{})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		_ = s.Auth.Logout(c.Value)
	}
	s.clearCookie(w)
	writeOK(w, map[string]any{})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.sessionFrom(r)
	if err != nil {
		writeErr(w, 401, "unauthorized", "未登录")
		return
	}
	u, _ := s.Auth.Username()
	totpOn, _ := s.Auth.TOTPEnabled()
	writeOK(w, map[string]any{
		"username":     u,
		"expires_at":   sess.ExpiresAt.Format(time.RFC3339),
		"csrf_token":   sess.CSRF,
		"totp_enabled": totpOn,
		"ip":           sess.IP,
	})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Auth.ListSessions()
	if err != nil {
		writeErr(w, 500, "internal", "查询失败")
		return
	}
	cur, _ := s.sessionFrom(r)
	items := make([]map[string]any, 0, len(list))
	for _, it := range list {
		items = append(items, map[string]any{
			"id":         it.ID,
			"ip":         it.IP,
			"user_agent": it.UserAgent,
			"created_at": it.CreatedAt.Format(time.RFC3339),
			"expires_at": it.ExpiresAt.Format(time.RFC3339),
			"current":    cur != nil && cur.ID == it.ID,
		})
	}
	writeOK(w, map[string]any{"items": items})
}

func (s *Server) handleKick(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, _ := s.sessionFrom(r)
	if id == "current" && sess != nil {
		id = sess.ID
	}
	if err := s.Auth.Logout(id); err != nil {
		writeErr(w, 500, "internal", "操作失败")
		return
	}
	if sess != nil && sess.ID == id {
		s.clearCookie(w)
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Old string `json:"old_password"`
		New string `json:"new_password"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	err := s.Auth.ChangePassword(in.Old, in.New)
	if errors.Is(err, auth.ErrInvalidCreds) {
		writeErr(w, 401, "unauthorized", "旧密码错误")
		return
	}
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	if sess, err := s.sessionFrom(r); err == nil {
		_ = s.Auth.InvalidateOtherSessions(sess.ID)
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	st, err := s.Auth.SetupTOTP()
	if errors.Is(err, auth.ErrTOTPActive) {
		writeErr(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "生成失败")
		return
	}
	writeOK(w, st)
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Auth.EnableTOTP(in.Code); err != nil {
		writeErr(w, 400, "invalid", "验证码错误")
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Auth.DisableTOTP(in.Password); err != nil {
		writeErr(w, 401, "unauthorized", "密码错误")
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	p, err := s.Settings.Public()
	if err != nil {
		writeErr(w, 500, "internal", "读取失败")
		return
	}
	writeOK(w, p)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var u settings.Update
	if err := readJSON(w, r, &u); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Settings.Apply(u, clientIP(r)); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	needRestart := u.BindMode != nil
	writeOK(w, map[string]any{"need_restart": needRestart})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	var metrics any
	if s.Monitor != nil {
		sm, err := s.Monitor.Latest()
		if err != nil {
			sm, _ = s.Monitor.Snapshot()
		}
		metrics = sm
	}
	writeOK(w, map[string]any{
		"hostname":    host,
		"os":          s.OS.Pretty,
		"os_id":       s.OS.ID,
		"os_version":  s.OS.VersionID,
		"go":          runtime.Version(),
		"pid":         os.Getpid(),
		"uptime_sec":  int(time.Since(s.Started).Seconds()),
		"listen":      s.Listen,
		"path_prefix": s.Prefix,
		"services":    map[string]string{"nginx": nginxStatus(s), "php": phpStatus(s), "mysql": mysqlStatus(s)},
		"metrics":     metrics,
	})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, err := s.Audit.Query(q.Get("from"), q.Get("to"), q.Get("action"), q.Get("ip"), limit)
	if err != nil {
		writeErr(w, 500, "internal", "查询失败")
		return
	}
	writeOK(w, map[string]any{"items": items})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	writeOK(w, map[string]any{"message": "面板即将重启"})
	if s.Restart != nil {
		go func() {
			time.Sleep(200 * time.Millisecond)
			s.Restart()
		}()
	}
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, err := s.Jobs.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "not_found", "任务不存在")
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "查询失败")
		return
	}
	writeOK(w, j)
}

func nginxStatus(s *Server) string {
	if s.Nginx == nil || !s.Nginx.Installed() {
		return "未安装"
	}
	return "已安装"
}

func phpStatus(s *Server) string {
	if s.PHP == nil {
		return "未检测"
	}
	return s.PHP.StatusLine()
}

func mysqlStatus(s *Server) string {
	if s.MySQL == nil {
		return "未检测"
	}
	return s.MySQL.StatusLine(context.Background())
}
