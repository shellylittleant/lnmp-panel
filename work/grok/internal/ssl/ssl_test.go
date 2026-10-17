package ssl

import (
	"context"
	"path/filepath"
	"testing"

	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/site"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

func TestIssueTwiceOneRowAndNotAfter(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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
	_ = ngx.EnsureLayout()
	sites := site.New(db, tpl, ngx)
	sites.WWW = filepath.Join(dir, "www")
	sites.CertDir = filepath.Join(dir, "certs")
	sites.SkipNX = true
	st, err := sites.Create(context.Background(), site.In{SiteKey: "c1", Domains: []string{"c1.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, sites)
	svc.Dir = sites.CertDir
	svc.AcmeWWW = ngx.AcmeWWW
	svc.Issuer = SelfSigner{}
	if err := svc.Issue(context.Background(), st.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Issue(context.Background(), st.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM certificates WHERE site_id=?`, st.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应只剩一行，得到 %d", n)
	}
	var exp string
	if err := db.SQL.QueryRow(`SELECT expires_at FROM certificates WHERE site_id=?`, st.ID).Scan(&exp); err != nil {
		t.Fatal(err)
	}
	if exp == "" {
		t.Fatal("到期时间空")
	}
}

func TestDeleteSiteWithCert(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tpl := template.New(db)
	_ = tpl.SyncOfficial()
	ngx := nginx.New()
	ngx.Available = filepath.Join(dir, "a")
	ngx.Enabled = filepath.Join(dir, "e")
	ngx.ConfD = filepath.Join(dir, "c")
	ngx.AcmeWWW = filepath.Join(dir, "acme")
	ngx.BackupRoot = filepath.Join(dir, "b")
	_ = ngx.EnsureLayout()
	sites := site.New(db, tpl, ngx)
	sites.WWW = filepath.Join(dir, "www")
	sites.CertDir = filepath.Join(dir, "certs")
	sites.SkipNX = true
	st, err := sites.Create(context.Background(), site.In{SiteKey: "c2", Domains: []string{"c2.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, sites)
	svc.Dir = sites.CertDir
	svc.AcmeWWW = ngx.AcmeWWW
	svc.Issuer = SelfSigner{}
	if err := svc.Issue(context.Background(), st.ID); err != nil {
		t.Fatal(err)
	}
	if err := sites.Delete(context.Background(), st.ID, false); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.SQL.QueryRow(`SELECT COUNT(1) FROM sites`).Scan(&n)
	if n != 0 {
		t.Fatal("站点应删除")
	}
	_ = db.SQL.QueryRow(`SELECT COUNT(1) FROM certificates`).Scan(&n)
	if n != 0 {
		t.Fatal("证书行应级联删除")
	}
}
