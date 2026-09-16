package site

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

func setup(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tpl := template.New(db)
	if err := tpl.SyncOfficial(); err != nil {
		t.Fatal(err)
	}
	ngx := nginx.New()
	ngx.Available = filepath.Join(dir, "a")
	ngx.Enabled = filepath.Join(dir, "e")
	ngx.ConfD = filepath.Join(dir, "c")
	ngx.AcmeWWW = filepath.Join(dir, "acme")
	ngx.BackupRoot = filepath.Join(dir, "b")
	if err := ngx.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	s := New(db, tpl, ngx)
	s.WWW = filepath.Join(dir, "www")
	s.SkipNX = true
	return s
}

func TestCreateStaticAndDrift(t *testing.T) {
	s := setup(t)
	st, err := s.Create(context.Background(), In{
		SiteKey: "blog", Name: "博客", Domains: []string{"blog.example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := s.ngx.Read("blog")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "server_name blog.example.test") {
		t.Fatalf("conf 缺少域名：%s", body)
	}
	if !strings.Contains(body, "lnmp_panel") {
		t.Fatal("站点 conf 应引用 lnmp_panel 日志格式")
	}
	g, _ := os.ReadFile(filepath.Join(s.ngx.ConfD, "lnmp-panel.conf"))
	if !strings.Contains(string(g), "log_format lnmp_panel") {
		t.Fatal("全局片段缺少 log_format")
	}
	if err := os.WriteFile(s.ngx.ConfPath("blog"), []byte("server{ # hand edit\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Drifted {
		t.Fatal("手改后应 drifted")
	}
	_, err = s.Update(context.Background(), st.ID, In{Name: "x", Force: false})
	if err != ErrDrift {
		t.Fatalf("无 force 应 冲突，得到 %v", err)
	}
	_, err = s.Update(context.Background(), st.ID, In{Name: "x", Force: true})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDomainUniqueAndRaw(t *testing.T) {
	s := setup(t)
	_, err := s.Create(context.Background(), In{SiteKey: "a", Domains: []string{"a.test"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Create(context.Background(), In{SiteKey: "b", Domains: []string{"a.test"}})
	if err != ErrDomain {
		t.Fatalf("域名冲突：%v", err)
	}
	st, err := s.Create(context.Background(), In{SiteKey: "c", Domains: []string{"c.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutRaw(context.Background(), st.ID, "server { listen 80; server_name c.test; }\n", false); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(st.ID)
	if !got.RawMode {
		t.Fatal("应为原文模式")
	}
}

func TestPHPSiteRendersSocket(t *testing.T) {
	s := setup(t)
	ver := "8.3"
	st, err := s.Create(context.Background(), In{SiteKey: "phpapp", Domains: []string{"php.test"}, PHPVersion: &ver})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := s.ngx.Read(st.SiteKey)
	if !strings.Contains(body, "php8.3-fpm.sock") {
		t.Fatalf("应含 php socket：%s", body)
	}
}
