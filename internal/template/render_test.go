package template

import (
	"path/filepath"
	"strings"
	"testing"

	"lnmp-panel/internal/store"
)

func TestOfficialSyncAndRender(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	if err := s.SyncOfficial(); err != nil {
		t.Fatal(err)
	}
	list, err := s.List("nginx_site")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 3 {
		t.Fatalf("官方 nginx_site 太少：%d", len(list))
	}
	r, err := s.OfficialByName("nginx_rewrite", "WordPress")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render("wp", r.Content, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "index.php") {
		t.Fatal(out)
	}
}

func TestEnsureUserDedupe(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	id1, err := s.EnsureUser("nginx_rewrite", "location / { }", nil)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.EnsureUser("nginx_rewrite", "location / { }", nil)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatal("相同内容应去重")
	}
}

func TestImportUsesGivenName(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	id, err := s.Import("nginx_rewrite", "我的规则", "location / { # unique }\n")
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "我的规则" {
		t.Fatalf("导入应保留名字，得到 %s", r.Name)
	}
	id2, err := s.Import("nginx_rewrite", "另一个名", "location / { # unique }\n")
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id {
		t.Fatal("相同哈希应返回已有 ID")
	}
}
