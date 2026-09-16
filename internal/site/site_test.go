package site

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"lnmp-panel/internal/exec"
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

func TestInjectRejectedNoResidue(t *testing.T) {
	s := setup(t)
	_, err := s.Create(context.Background(), In{SiteKey: "evil", Domains: []string{"evil.test { }"}})
	if err == nil {
		t.Fatal("注入域名应拒绝")
	}
	var n int
	_ = s.db.SQL.QueryRow(`SELECT COUNT(1) FROM sites`).Scan(&n)
	if n != 0 {
		t.Fatalf("失败后留下站点行 %d", n)
	}
	if _, err := os.Stat(s.ngx.ConfPath("evil")); !os.IsNotExist(err) {
		t.Fatal("失败后留下 conf")
	}
}

func TestCreateNginxTestFailCleans(t *testing.T) {
	s := setup(t)
	s.SkipNX = false
	bin := filepath.Join(t.TempDir(), "nginx")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	s.ngx.Bin = bin
	s.ngx.Runner = func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error) {
		for _, a := range argv {
			if a == "-t" {
				return exec.Result{Stderr: "injected"}, errors.New("fail")
			}
		}
		return exec.Result{}, nil
	}
	_, err := s.Create(context.Background(), In{SiteKey: "bad", Domains: []string{"bad.example.test"}})
	if err == nil {
		t.Fatal("nginx -t 失败应返回错误")
	}
	var n int
	_ = s.db.SQL.QueryRow(`SELECT COUNT(1) FROM sites`).Scan(&n)
	if n != 0 {
		t.Fatal("不应插入站点")
	}
	if _, err := os.Stat(s.ngx.ConfPath("bad")); !os.IsNotExist(err) {
		t.Fatal("应删掉 conf 与软链")
	}
}

func TestPurgeOnlyUnderWWW(t *testing.T) {
	s := setup(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	st, err := s.Create(context.Background(), In{SiteKey: "out", Domains: []string{"out.example.test"}, Root: outside})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), st.ID, true); err != ErrPurge {
		t.Fatalf("应拒绝 purge：%v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("外部目录不该被删")
	}
}

func TestRawModeBlocksFormSave(t *testing.T) {
	s := setup(t)
	st, err := s.Create(context.Background(), In{SiteKey: "raw", Domains: []string{"raw.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutRaw(context.Background(), st.ID, "server { listen 80; server_name raw.example.test; }\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(context.Background(), st.ID, In{Name: "改名"}); err != ErrRawMode {
		t.Fatalf("原文模式表单保存应 409：%v", err)
	}
}

func TestFormSaveDoesNotCreateTemplate(t *testing.T) {
	s := setup(t)
	before, _ := s.tpl.List("")
	if _, err := s.Create(context.Background(), In{SiteKey: "t1", Domains: []string{"t1.example.test"}}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.tpl.List("")
	if len(after) != len(before) {
		t.Fatalf("表单建站不应新增模板 %d -> %d", len(before), len(after))
	}
}

func TestListNoNestedQueryDeadlock(t *testing.T) {
	dir := t.TempDir()
	db, err := store.OpenConns(filepath.Join(dir, "p.db"), 1)
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
	if _, err := s.Create(context.Background(), In{SiteKey: "a", Domains: []string{"a.test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(context.Background(), In{SiteKey: "b", Domains: []string{"b.test"}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		items, err := s.List()
		if err == nil && len(items) != 2 {
			err = errors.New("want 2")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("List 在单连接下超过 2 秒，疑似嵌套查询死锁")
	}
}

func TestUseTemplateAfterRawRestoresOfficial(t *testing.T) {
	s := setup(t)
	st, err := s.Create(context.Background(), In{SiteKey: "rawback", Domains: []string{"rawback.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	origID := st.NginxTemplateID
	marker := "RAW_MARKER_LNMP_PANEL"
	raw := "server { listen 80; server_name rawback.example.test; # " + marker + "\n}\n"
	if err := s.PutRaw(context.Background(), st.ID, raw, false); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(st.ID)
	if !got.RawMode {
		t.Fatal("应为原文模式")
	}
	if origID != nil && got.NginxTemplateID != nil && *got.NginxTemplateID != *origID {
		t.Fatalf("PutRaw 不应改绑 nginx_template_id: %v -> %v", *origID, *got.NginxTemplateID)
	}
	if err := s.UseTemplate(context.Background(), st.ID); err != nil {
		t.Fatal(err)
	}
	body, err := s.ngx.Read(st.SiteKey)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, marker) {
		t.Fatalf("恢复模板后仍含原文标记：%s", body)
	}
	if !strings.Contains(body, "try_files") {
		t.Fatalf("应含官方模板特征 try_files：%s", body)
	}
	got, _ = s.Get(st.ID)
	if got.RawMode {
		t.Fatal("应退出原文模式")
	}
}

func TestEnsureRootIgnoresUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	s := setup(t)
	st, err := s.Create(context.Background(), In{SiteKey: "perm", Domains: []string{"perm.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o777 != 0o755 {
		t.Fatalf("目录权限 %o", fi.Mode()&0o777)
	}
	ii, err := os.Stat(filepath.Join(st.Root, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if ii.Mode()&0o777 != 0o644 {
		t.Fatalf("文件权限 %o", ii.Mode()&0o777)
	}
}

func TestConfHasAcmePathNotLibPanel(t *testing.T) {
	s := setup(t)
	st, err := s.Create(context.Background(), In{SiteKey: "acme", Domains: []string{"acme.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := s.ngx.Read(st.SiteKey)
	if !strings.Contains(body, "/var/www/.acme-challenge/") {
		t.Fatal(body)
	}
	if strings.Contains(body, "/var/lib/lnmp-panel/acme-www") {
		t.Fatal("旧挑战路径仍在")
	}
}
