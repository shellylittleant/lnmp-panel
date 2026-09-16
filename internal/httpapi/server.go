package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"lnmp-panel/internal/audit"
	"lnmp-panel/internal/auth"
	"lnmp-panel/internal/env"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/site"
	"lnmp-panel/internal/ssl"
	"lnmp-panel/internal/template"
	"lnmp-panel/internal/webui"
)

const cookieName = "lnmp_panel_session"

type Server struct {
	Prefix    string
	Auth      *auth.Service
	Audit     *audit.Service
	Settings  *settings.Service
	Jobs      *jobs.Service
	Started   time.Time
	Listen    string
	Restart   func()
	OS        env.OSInfo
	Tpl       *template.Service
	Sites     *site.Service
	Nginx     *nginx.Service
	SSL       *ssl.Service
	Installer *env.NginxInstaller
}

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	api.HandleFunc("POST /api/v1/auth/totp/verify", s.handleTOTPVerify)
	api.HandleFunc("POST /api/v1/auth/logout", s.requireAuth(s.handleLogout))
	api.HandleFunc("GET /api/v1/auth/session", s.requireAuth(s.handleSession))
	api.HandleFunc("GET /api/v1/auth/sessions", s.requireAuth(s.handleListSessions))
	api.HandleFunc("DELETE /api/v1/auth/sessions/{id}", s.requireAuth(s.handleKick))
	api.HandleFunc("POST /api/v1/auth/password", s.requireAuth(s.handlePassword))
	api.HandleFunc("POST /api/v1/auth/totp/setup", s.requireAuth(s.handleTOTPSetup))
	api.HandleFunc("POST /api/v1/auth/totp/enable", s.requireAuth(s.handleTOTPEnable))
	api.HandleFunc("POST /api/v1/auth/totp/disable", s.requireAuth(s.handleTOTPDisable))
	api.HandleFunc("GET /api/v1/settings", s.requireAuth(s.handleGetSettings))
	api.HandleFunc("PUT /api/v1/settings", s.requireAuth(s.handlePutSettings))
	api.HandleFunc("GET /api/v1/status", s.requireAuth(s.handleStatus))
	api.HandleFunc("GET /api/v1/audit", s.requireAuth(s.handleAudit))
	api.HandleFunc("POST /api/v1/panel/restart", s.requireAuth(s.handleRestart))
	api.HandleFunc("GET /api/v1/jobs/{id}", s.requireAuth(s.handleJob))
	api.HandleFunc("GET /api/v1/env", s.requireAuth(s.handleEnv))
	api.HandleFunc("POST /api/v1/env/nginx/install", s.requireAuth(s.handleNginxInstall))
	api.HandleFunc("GET /api/v1/templates", s.requireAuth(s.handleTemplates))
	api.HandleFunc("POST /api/v1/templates", s.requireAuth(s.handleTemplateCreate))
	api.HandleFunc("GET /api/v1/templates/{id}", s.requireAuth(s.handleTemplateGet))
	api.HandleFunc("PUT /api/v1/templates/{id}", s.requireAuth(s.handleTemplatePut))
	api.HandleFunc("DELETE /api/v1/templates/{id}", s.requireAuth(s.handleTemplateDelete))
	api.HandleFunc("POST /api/v1/templates/{id}/export", s.requireAuth(s.handleTemplateExport))
	api.HandleFunc("POST /api/v1/templates/import", s.requireAuth(s.handleTemplateImport))
	api.HandleFunc("GET /api/v1/sites", s.requireAuth(s.handleSites))
	api.HandleFunc("POST /api/v1/sites", s.requireAuth(s.handleSiteCreate))
	api.HandleFunc("GET /api/v1/sites/{id}", s.requireAuth(s.handleSiteGet))
	api.HandleFunc("PUT /api/v1/sites/{id}", s.requireAuth(s.handleSitePut))
	api.HandleFunc("DELETE /api/v1/sites/{id}", s.requireAuth(s.handleSiteDelete))
	api.HandleFunc("POST /api/v1/sites/{id}/enable", s.requireAuth(s.handleSiteEnable))
	api.HandleFunc("POST /api/v1/sites/{id}/disable", s.requireAuth(s.handleSiteDisable))
	api.HandleFunc("GET /api/v1/sites/{id}/nginx", s.requireAuth(s.handleSiteNginxGet))
	api.HandleFunc("PUT /api/v1/sites/{id}/nginx", s.requireAuth(s.handleSiteNginxPut))
	api.HandleFunc("POST /api/v1/sites/{id}/nginx/use-template", s.requireAuth(s.handleSiteUseTpl))
	api.HandleFunc("GET /api/v1/sites/{id}/redirects", s.requireAuth(s.handleRedirects))
	api.HandleFunc("PUT /api/v1/sites/{id}/redirects", s.requireAuth(s.handleRedirects))
	api.HandleFunc("POST /api/v1/sites/{id}/ssl/issue", s.requireAuth(s.handleSSLIssue))
	api.HandleFunc("POST /api/v1/sites/{id}/ssl/renew", s.requireAuth(s.handleSSLIssue))
	api.HandleFunc("GET /api/v1/certs", s.requireAuth(s.handleCerts))

	inner := http.NewServeMux()
	inner.Handle("/api/", s.withSecurity(s.auditWrites(api)))
	inner.Handle("/", s.withSecurity(s.spa()))

	root := http.NewServeMux()
	pfx := "/" + strings.Trim(s.Prefix, "/")
	root.Handle(pfx+"/", http.StripPrefix(pfx, inner))
	root.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	return root
}

func (s *Server) spa() http.Handler {
	ui, err := webui.FS()
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, 500, "internal", "前端资源缺失")
		})
	}
	fileServer := http.FileServer(http.FS(ui))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			fileServer.ServeHTTP(w, r)
			return
		}
		base := "/" + strings.Trim(s.Prefix, "/") + "/"
		b, err := webui.IndexHTML(base)
		if err != nil {
			writeErr(w, 500, "internal", "前端资源缺失")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	})
}

func (s *Server) withSecurity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		r.Header.Set("X-Request-Id", newID(8))
		ip := clientIP(r)
		ok, err := s.Settings.IPAllowed(ip)
		if err != nil {
			writeErr(w, 500, "internal", "白名单检查失败")
			return
		}
		if !ok {
			writeErr(w, http.StatusForbidden, "forbidden", "当前 IP 不在运维白名单内")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auditWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &statusWriter{ResponseWriter: w, code: 200}
		next.ServeHTTP(rw, r)
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			return
		}
		actor := "anonymous"
		if c, err := r.Cookie(cookieName); err == nil {
			if sess, err := s.Auth.GetSession(c.Value, clientIP(r)); err == nil {
				if u, err := s.Auth.Username(); err == nil {
					actor = u
				}
				_ = sess
			}
		}
		action := r.Method + " " + r.URL.Path
		detail := ""
		if rw.code >= 400 {
			detail = "失败"
		}
		_ = s.Audit.Append(clientIP(r), actor, action, r.URL.Path, detail, r.Header.Get("X-Request-Id"))
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.sessionFrom(r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "未登录或会话已失效")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-CSRF-Token") != sess.CSRF {
				writeErr(w, http.StatusForbidden, "forbidden", "CSRF 校验失败")
				return
			}
		}
		h(w, r)
	}
}

func (s *Server) sessionFrom(r *http.Request) (*auth.Session, error) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil, auth.ErrNoSession
	}
	return s.Auth.GetSession(c.Value, clientIP(r))
}

func (s *Server) setCookie(w http.ResponseWriter, sess *auth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    sess.ID,
		Path:     "/" + strings.Trim(s.Prefix, "/") + "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  sess.ExpiresAt,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/" + strings.Trim(s.Prefix, "/") + "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func newID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
