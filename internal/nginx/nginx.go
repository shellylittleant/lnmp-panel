package nginx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"lnmp-panel/internal/exec"
	panelFS "lnmp-panel/internal/fs"
	"lnmp-panel/internal/template"
)

const (
	AcmeDir    = "/var/www/.acme-challenge"
	GlobalConf = `log_format lnmp_panel '$remote_addr - $remote_user [$time_local] "$request" '
                     '$status $body_bytes_sent "$http_referer" "$http_user_agent" $request_time $host';
`
)

type Service struct {
	Bin        string
	Available  string
	Enabled    string
	ConfD      string
	AcmeWWW    string
	BackupRoot string
	Runner     func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error)
}

func New() *Service {
	return &Service{
		Bin:        "/usr/sbin/nginx",
		Available:  "/etc/nginx/sites-available",
		Enabled:    "/etc/nginx/sites-enabled",
		ConfD:      "/etc/nginx/conf.d",
		AcmeWWW:    AcmeDir,
		BackupRoot: "/var/lib/lnmp-panel/backups/nginx",
		Runner:     exec.Run,
	}
}

func (s *Service) Installed() bool {
	_, err := os.Stat(s.Bin)
	return err == nil
}

func SystemdLive() bool {
	st, err := os.Stat("/run/systemd/system")
	return err == nil && st.IsDir()
}

func (s *Service) Version(ctx context.Context) string {
	if s.Runner == nil || !s.Installed() {
		return ""
	}
	r, err := s.Runner(ctx, 5*time.Second, s.Bin, "-v")
	if err != nil {
		return ""
	}
	out := r.Stderr
	if out == "" {
		out = r.Stdout
	}
	return out
}

func (s *Service) EnsureLayout() error {
	for _, d := range []string{s.Available, s.Enabled, s.ConfD, s.AcmeWWW, s.BackupRoot} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
		_ = os.Chmod(d, 0755)
	}
	return panelFS.WriteAtomic(filepath.Join(s.ConfD, "lnmp-panel.conf"), []byte(GlobalConf), 0644)
}

func (s *Service) ConfPath(siteKey string) string {
	return filepath.Join(s.Available, siteKey+".conf")
}

func (s *Service) EnabledPath(siteKey string) string {
	return filepath.Join(s.Enabled, siteKey+".conf")
}

func (s *Service) WriteSite(siteKey, content string) error {
	path := s.ConfPath(siteKey)
	_, _ = panelFS.Backup(path, s.BackupRoot)
	return panelFS.WriteAtomic(path, []byte(content), 0644)
}

func (s *Service) Restore(siteKey, backupFile string) error {
	if backupFile == "" {
		return nil
	}
	b, err := os.ReadFile(backupFile)
	if err != nil {
		return err
	}
	return panelFS.WriteAtomic(s.ConfPath(siteKey), b, 0644)
}

func (s *Service) RemoveSite(siteKey string) error {
	_ = os.Remove(s.EnabledPath(siteKey))
	_ = os.Remove(s.ConfPath(siteKey))
	return nil
}

func (s *Service) Enable(siteKey string) error {
	src := s.ConfPath(siteKey)
	dst := s.EnabledPath(siteKey)
	_ = os.Remove(dst)
	return os.Symlink(src, dst)
}

func (s *Service) Disable(siteKey string) error {
	return os.Remove(s.EnabledPath(siteKey))
}

func clip(s string) string {
	if len(s) > 2000 {
		return s[:2000]
	}
	return s
}

func (s *Service) TestAndReload(ctx context.Context) error {
	if !s.Installed() {
		return fmt.Errorf("未安装 nginx")
	}
	run := s.Runner
	if run == nil {
		run = exec.Run
	}
	r, err := run(ctx, 10*time.Second, s.Bin, "-t")
	if err != nil {
		msg := r.Stderr
		if msg == "" {
			msg = r.Stdout
		}
		return fmt.Errorf("nginx -t 失败：%s", clip(msg))
	}
	if SystemdLive() {
		r, err = run(ctx, 15*time.Second, "/usr/bin/systemctl", "reload", "nginx")
		if err == nil {
			return nil
		}
		r2, err2 := run(ctx, 15*time.Second, "/usr/bin/systemctl", "start", "nginx")
		if err2 != nil {
			return fmt.Errorf("systemctl reload/start nginx 失败：%s %s", clip(r.Stderr), clip(r2.Stderr))
		}
		return nil
	}
	r, err = run(ctx, 10*time.Second, s.Bin, "-s", "reload")
	if err == nil {
		return nil
	}
	r2, err2 := run(ctx, 10*time.Second, s.Bin)
	if err2 != nil {
		return fmt.Errorf("nginx reload/start 失败：%s %s", clip(r.Stderr), clip(r2.Stderr))
	}
	return nil
}

func (s *Service) HashOf(siteKey string) (string, error) {
	return panelFS.HashFile(s.ConfPath(siteKey))
}

func (s *Service) Read(siteKey string) (string, error) {
	b, err := os.ReadFile(s.ConfPath(siteKey))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Service) DisableDefault() {
	_ = os.Remove(filepath.Join(s.Enabled, "default"))
}

type Redirect struct {
	FromPath string
	ToURL    string
	Code     int
}

type Vars struct {
	SiteKey, Root, PHPSocket, SSLCert, SSLKey, SSLSnippet, Rewrite, ProxyPass, BasicAuthFile string
	Domains, AccessAllow, Hotlink                                                            []string
	HTTP2, HTTP3                                                                             bool
	Redirects                                                                                []Redirect
	ToURL                                                                                    string
}

func RenderSite(tpl *template.Service, nginxID, rewriteID, sslID int64, v Vars) (string, error) {
	if v.Domains == nil {
		v.Domains = []string{}
	}
	if v.AccessAllow == nil {
		v.AccessAllow = []string{}
	}
	if v.Hotlink == nil {
		v.Hotlink = []string{}
	}
	if v.Redirects == nil {
		v.Redirects = []Redirect{}
	}
	if rewriteID > 0 {
		rt, err := tpl.Get(rewriteID)
		if err != nil {
			return "", err
		}
		v.Rewrite, err = template.Render(rt.Name, rt.Content, v)
		if err != nil {
			return "", err
		}
	}
	if sslID > 0 && v.SSLCert != "" {
		st, err := tpl.Get(sslID)
		if err != nil {
			return "", err
		}
		v.SSLSnippet, err = template.Render(st.Name, st.Content, v)
		if err != nil {
			return "", err
		}
	}
	nt, err := tpl.Get(nginxID)
	if err != nil {
		return "", err
	}
	return template.Render(nt.Name, nt.Content, v)
}
