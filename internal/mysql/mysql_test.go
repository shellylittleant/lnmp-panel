package mysql

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

func setupMySQL(t *testing.T) *Service {
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
	s := New(db, tpl, jobs.New(db))
	s.SkipExec = true
	s.CNFPath = filepath.Join(dir, "lnmp-panel.cnf")
	s.Defaults = filepath.Join(dir, "root.cnf")
	s.BackupRoot = filepath.Join(dir, "bak")
	s.TmpDir = filepath.Join(dir, "tmp")
	return s
}

func TestIdentRejectsInject(t *testing.T) {
	for _, n := range []string{"a;drop", "a b", "a`b", "a'b", "", "foo-bar", "foo.bar"} {
		if ValidIdent(n) == nil {
			t.Fatalf("应拒绝 %q", n)
		}
	}
	if err := ValidIdent("blog_1"); err != nil {
		t.Fatal(err)
	}
	if ValidHost("localhost") != nil || ValidHost("%") != nil {
		t.Fatal("host")
	}
	if ValidHost("a;b") == nil || ValidHost("a b") == nil {
		t.Fatal("应拒绝坏 host")
	}
}

func TestCreateDropDBAndUser(t *testing.T) {
	s := setupMySQL(t)
	ctx := context.Background()
	d, err := s.CreateDB(ctx, "blog", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Charset != "utf8mb4" {
		t.Fatal(d)
	}
	if _, err := s.CreateDB(ctx, "blog", "", ""); err != ErrDup {
		t.Fatalf("got %v", err)
	}
	if _, err := s.CreateDB(ctx, "x;drop", "", ""); err == nil {
		t.Fatal("注入")
	}
	u, err := s.CreateUser(ctx, "blogu", "localhost", "GoodPass1", []string{"blog"})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Databases) != 1 || u.Databases[0] != "blog" {
		t.Fatalf("%+v", u)
	}
	if _, err := s.CreateUser(ctx, "bad;u", "localhost", "GoodPass1", nil); err == nil {
		t.Fatal("用户名注入")
	}
	if err := s.SetPassword(ctx, u.ID, "NewPassw0"); err != nil {
		t.Fatal(err)
	}
	if err := s.DropDB(ctx, "blog"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListDB()
	if len(list) != 0 {
		t.Fatalf("%v", list)
	}
}

func TestCNFFormVsRaw(t *testing.T) {
	s := setupMySQL(t)
	ctx := context.Background()
	old := syscall.Umask(0077)
	defer syscall.Umask(old)
	if err := s.WriteDefaultCNF(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(s.CNFPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0644 {
		t.Fatalf("perm=%o", st.Mode().Perm())
	}
	before, _ := s.tpl.List("mysql_cnf")
	in := defaultCNF()
	in.MaxConnections = "200"
	if err := s.PutCNF(ctx, in, false); err != nil {
		t.Fatal(err)
	}
	after, _ := s.tpl.List("mysql_cnf")
	if len(after) != len(before) {
		t.Fatal("表单不应产生用户模板")
	}
	body, _ := os.ReadFile(s.CNFPath)
	if !strings.Contains(string(body), "200") {
		t.Fatalf("%s", body)
	}
	if err := s.PutCNFRaw(ctx, "[mysqld]\nmax_connections = 50\n", false); err != nil {
		t.Fatal(err)
	}
	after2, _ := s.tpl.List("mysql_cnf")
	if len(after2) != len(before)+1 {
		t.Fatalf("原文应产生用户模板 %d -> %d", len(before), len(after2))
	}
	if err := s.PutCNF(ctx, defaultCNF(), false); err != ErrRawMode {
		t.Fatalf("got %v", err)
	}
	if err := os.WriteFile(s.CNFPath, []byte("[mysqld]\n#drift\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCNFRaw(ctx, "[mysqld]\nx=1\n", false); err != ErrDrift {
		t.Fatalf("got %v", err)
	}
	if err := s.UseCNFTemplate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCNF(ctx, defaultCNF(), false); err != nil {
		t.Fatal(err)
	}
}

func TestMariaDBRefuse(t *testing.T) {
	s := setupMySQL(t)
	if err := s.Refuse(context.Background()); err != nil {
		t.Fatal(err)
	}
}
