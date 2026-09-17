package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"lnmp-panel/internal/audit"
	"lnmp-panel/internal/auth"
	"lnmp-panel/internal/cron"
	"lnmp-panel/internal/env"
	"lnmp-panel/internal/firewall"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/logview"
	"lnmp-panel/internal/migrate"
	"lnmp-panel/internal/monitor"
	"lnmp-panel/internal/mysql"
	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/php"
	"lnmp-panel/internal/scheduler"
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
	Stack     *env.StackInstaller
	PHP       *php.Service
	MySQL     *mysql.Service
	Firewall  *firewall.Service
	Logs      *logview.Service
	Monitor   *monitor.Service
	Cron      *cron.Service
	Scheduler *scheduler.Scheduler
	Migrate   *migrate.Service
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
	api.HandleFunc("POST /api/v1/env/php/repo", s.requireAuth(s.handlePHPRepo))
	api.HandleFunc("POST /api/v1/env/mysql/install", s.requireAuth(s.handleMySQLInstall))
	api.HandleFunc("GET /api/v1/php/versions", s.requireAuth(s.handlePHPVersions))
	api.HandleFunc("POST /api/v1/php/versions/{ver}/install", s.requireAuth(s.handlePHPInstall))
	api.HandleFunc("GET /api/v1/php/versions/{ver}/extensions", s.requireAuth(s.handlePHPExtGet))
	api.HandleFunc("POST /api/v1/php/versions/{ver}/extensions", s.requireAuth(s.handlePHPExtAdd))
	api.HandleFunc("DELETE /api/v1/php/versions/{ver}/extensions/{name}", s.requireAuth(s.handlePHPExtDel))
	api.HandleFunc("GET /api/v1/php/versions/{ver}/ini", s.requireAuth(s.handlePHPIniGet))
	api.HandleFunc("PUT /api/v1/php/versions/{ver}/ini", s.requireAuth(s.handlePHPIniPut))
	api.HandleFunc("PUT /api/v1/php/versions/{ver}/ini/raw", s.requireAuth(s.handlePHPIniRaw))
	api.HandleFunc("POST /api/v1/php/versions/{ver}/ini/use-template", s.requireAuth(s.handlePHPIniUseTpl))
	api.HandleFunc("GET /api/v1/php/versions/{ver}/pool", s.requireAuth(s.handlePHPPoolGet))
	api.HandleFunc("PUT /api/v1/php/versions/{ver}/pool", s.requireAuth(s.handlePHPPoolPut))
	api.HandleFunc("PUT /api/v1/php/versions/{ver}/pool/raw", s.requireAuth(s.handlePHPPoolRaw))
	api.HandleFunc("POST /api/v1/php/versions/{ver}/pool/use-template", s.requireAuth(s.handlePHPPoolUseTpl))
	api.HandleFunc("GET /api/v1/mysql/databases", s.requireAuth(s.handleMySQLDBs))
	api.HandleFunc("POST /api/v1/mysql/databases", s.requireAuth(s.handleMySQLDBCreate))
	api.HandleFunc("DELETE /api/v1/mysql/databases/{name}", s.requireAuth(s.handleMySQLDBDrop))
	api.HandleFunc("POST /api/v1/mysql/databases/{name}/export", s.requireAuth(s.handleMySQLExport))
	api.HandleFunc("POST /api/v1/mysql/databases/{name}/import", s.requireAuth(s.handleMySQLImport))
	api.HandleFunc("GET /api/v1/mysql/users", s.requireAuth(s.handleMySQLUsers))
	api.HandleFunc("POST /api/v1/mysql/users", s.requireAuth(s.handleMySQLUserCreate))
	api.HandleFunc("PUT /api/v1/mysql/users/{id}/password", s.requireAuth(s.handleMySQLUserPass))
	api.HandleFunc("DELETE /api/v1/mysql/users/{id}", s.requireAuth(s.handleMySQLUserDel))
	api.HandleFunc("GET /api/v1/mysql/cnf", s.requireAuth(s.handleMySQLCNFGet))
	api.HandleFunc("PUT /api/v1/mysql/cnf", s.requireAuth(s.handleMySQLCNFPut))
	api.HandleFunc("GET /api/v1/mysql/cnf/raw", s.requireAuth(s.handleMySQLCNFGet))
	api.HandleFunc("PUT /api/v1/mysql/cnf/raw", s.requireAuth(s.handleMySQLCNFRaw))
	api.HandleFunc("POST /api/v1/mysql/cnf/use-template", s.requireAuth(s.handleMySQLCNFUseTpl))
	api.HandleFunc("GET /api/v1/firewall", s.requireAuth(s.handleFirewallGet))
	api.HandleFunc("PUT /api/v1/firewall", s.requireAuth(s.handleFirewallPut))
	api.HandleFunc("GET /api/v1/firewall/raw", s.requireAuth(s.handleFirewallRawGet))
	api.HandleFunc("PUT /api/v1/firewall/raw", s.requireAuth(s.handleFirewallRawPut))
	api.HandleFunc("POST /api/v1/firewall/apply", s.requireAuth(s.handleFirewallApply))
	api.HandleFunc("POST /api/v1/firewall/confirm", s.requireAuth(s.handleFirewallConfirm))
	api.HandleFunc("POST /api/v1/firewall/rollback", s.requireAuth(s.handleFirewallRollback))
	api.HandleFunc("POST /api/v1/firewall/ban", s.requireAuth(s.handleFirewallBan))
	api.HandleFunc("POST /api/v1/firewall/use-template", s.requireAuth(s.handleFirewallUseTpl))
	api.HandleFunc("GET /api/v1/logs/sources", s.requireAuth(s.handleLogSources))
	api.HandleFunc("GET /api/v1/logs/query", s.requireAuth(s.handleLogQuery))
	api.HandleFunc("GET /api/v1/logs/follow", s.requireAuth(s.handleLogFollow))
	api.HandleFunc("GET /api/v1/logs/stats", s.requireAuth(s.handleLogStats))
	api.HandleFunc("GET /api/v1/logs/rotate", s.requireAuth(s.handleLogRotateGet))
	api.HandleFunc("PUT /api/v1/logs/rotate", s.requireAuth(s.handleLogRotatePut))
	api.HandleFunc("GET /api/v1/metrics/current", s.requireAuth(s.handleMetricsCurrent))
	api.HandleFunc("GET /api/v1/metrics/history", s.requireAuth(s.handleMetricsHistory))
	api.HandleFunc("GET /api/v1/cron", s.requireAuth(s.handleCronList))
	api.HandleFunc("POST /api/v1/cron", s.requireAuth(s.handleCronCreate))
	api.HandleFunc("PUT /api/v1/cron/{id}", s.requireAuth(s.handleCronPut))
	api.HandleFunc("DELETE /api/v1/cron/{id}", s.requireAuth(s.handleCronDelete))
	api.HandleFunc("POST /api/v1/cron/{id}/run", s.requireAuth(s.handleCronRun))
	api.HandleFunc("GET /api/v1/scheduler", s.requireAuth(s.handleScheduler))
	api.HandleFunc("POST /api/v1/migrate/token", s.requireAuth(s.handleMigrateToken))
	api.HandleFunc("POST /api/v1/migrate/connect", s.requireAuth(s.handleMigrateConnect))
	api.HandleFunc("POST /api/v1/migrate/preflight", s.requireAuth(s.handleMigratePreflight))
	api.HandleFunc("POST /api/v1/migrate/start", s.requireAuth(s.handleMigrateStart))
	api.HandleFunc("GET /api/v1/migrate/status", s.requireAuth(s.handleMigrateStatus))
	api.HandleFunc("POST /api/v1/migrate/cancel", s.requireAuth(s.handleMigrateCancel))
	api.HandleFunc("POST /api/v1/migrate/accept", s.handleMigrateAccept)
	api.HandleFunc("POST /api/v1/migrate/peer/preflight", s.handleMigratePeerPreflight)
	api.HandleFunc("POST /api/v1/migrate/peer/apply", s.handleMigratePeerApply)
	api.HandleFunc("POST /api/v1/migrate/peer/cancel", s.handleMigratePeerCancel)
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
		if strings.HasPrefix(r.URL.Path, "/api/v1/migrate/accept") || strings.HasPrefix(r.URL.Path, "/api/v1/migrate/peer/") {
			next.ServeHTTP(w, r)
			return
		}
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

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
