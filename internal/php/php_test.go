package php

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

func setupPHP(t *testing.T) (*Service, string) {
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
	s.BaseDir = filepath.Join(dir, "php")
	s.RunDir = filepath.Join(dir, "run")
	s.BinDir = filepath.Join(dir, "sbin")
	s.BackupRoot = filepath.Join(dir, "bak")
	s.PPACheck = func() bool { return true }
	return s, dir
}

func installFake(t *testing.T, s *Service, ver string) {
	t.Helper()
	if err := os.MkdirAll(s.BinDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.bin(ver), []byte("fpm"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := s.afterInstall(context.Background(), ver); err != nil {
		t.Fatal(err)
	}
}

func TestValidVersion(t *testing.T) {
	if ValidVersion("8.3") != nil || ValidVersion("8.2") != nil || ValidVersion("8.10") != nil {
		t.Fatal("应接受 8.2+")
	}
	for _, v := range []string{"8.1", "8.0", "7.4", "8", "php8.3", "8.3.1", "8.3;rm"} {
		if ValidVersion(v) == nil {
			t.Fatalf("应拒绝 %s", v)
		}
	}
}

func TestExtForbiddenAndInject(t *testing.T) {
	s, _ := setupPHP(t)
	if _, err := s.pkgForExt("fpm"); err != ErrForbidden {
		t.Fatalf("got %v", err)
	}
	if _, err := s.pkgForExt("gd;rm"); err != ErrExt {
		t.Fatalf("got %v", err)
	}
	pkg, err := s.pkgForExt("mysqli")
	if err != nil || pkg != "mysql" {
		t.Fatalf("%s %v", pkg, err)
	}
}

func TestINIFormDoesNotCreateUserTemplate(t *testing.T) {
	s, _ := setupPHP(t)
	installFake(t, s, "8.3")
	before, _ := s.tpl.List("php_ini")
	in := prodINI()
	in.MemoryLimit = "512M"
	if err := s.PutINI(context.Background(), "8.3", in, false); err != nil {
		t.Fatal(err)
	}
	after, _ := s.tpl.List("php_ini")
	if len(after) != len(before) {
		t.Fatalf("表单保存不应产生用户模板 %d -> %d", len(before), len(after))
	}
	v, err := s.GetINI("8.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.Content, "512M") {
		t.Fatalf("content=%s", v.Content)
	}
}

func TestINIRawCreatesTemplateAndBlocksForm(t *testing.T) {
	s, _ := setupPHP(t)
	installFake(t, s, "8.3")
	before, _ := s.tpl.List("php_ini")
	if err := s.PutINIRaw(context.Background(), "8.3", "; raw\nmemory_limit = 128M\n", false); err != nil {
		t.Fatal(err)
	}
	after, _ := s.tpl.List("php_ini")
	if len(after) != len(before)+1 {
		t.Fatalf("原文应产生用户模板 %d -> %d", len(before), len(after))
	}
	if err := s.PutINI(context.Background(), "8.3", prodINI(), false); err != ErrRawMode {
		t.Fatalf("原文模式表单应 409，got %v", err)
	}
	if err := s.UseINITemplate(context.Background(), "8.3"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutINI(context.Background(), "8.3", prodINI(), false); err != nil {
		t.Fatal(err)
	}
}

func TestINIDriftAndInject(t *testing.T) {
	s, _ := setupPHP(t)
	installFake(t, s, "8.3")
	p := s.iniPath("8.3")
	if err := os.WriteFile(p, append([]byte(";drift\n"), mustRead(t, p)...), 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.PutINI(context.Background(), "8.3", prodINI(), false); err != ErrDrift {
		t.Fatalf("got %v", err)
	}
	if err := s.PutINI(context.Background(), "8.3", prodINI(), true); err != nil {
		t.Fatal(err)
	}
	bad := prodINI()
	bad.MemoryLimit = "256M; system"
	if err := s.PutINI(context.Background(), "8.3", bad, false); err == nil {
		t.Fatal("应拒绝注入")
	}
}

func TestPoolAndUmask(t *testing.T) {
	s, _ := setupPHP(t)
	old := syscall.Umask(0077)
	defer syscall.Umask(old)
	installFake(t, s, "8.2")
	st, err := os.Stat(s.poolPath("8.2"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0644 {
		t.Fatalf("pool perm=%o", st.Mode().Perm())
	}
	ist, err := os.Stat(s.iniPath("8.2"))
	if err != nil {
		t.Fatal(err)
	}
	if ist.Mode().Perm() != 0644 {
		t.Fatalf("ini perm=%o", ist.Mode().Perm())
	}
	before, _ := s.tpl.List("php_fpm_pool")
	if err := s.PutPool(context.Background(), "8.2", defaultPool("8.2", s.Socket("8.2")), false); err != nil {
		t.Fatal(err)
	}
	after, _ := s.tpl.List("php_fpm_pool")
	if len(after) != len(before) {
		t.Fatal("pool 表单不应产生用户模板")
	}
}

func TestInstallUnavailableVersion(t *testing.T) {
	s, _ := setupPHP(t)
	_, err := s.StartInstall(context.Background(), "8.9")
	if err != ErrUnavailable {
		t.Fatalf("got %v", err)
	}
}

func TestPPARequired(t *testing.T) {
	s, _ := setupPHP(t)
	s.PPACheck = func() bool { return false }
	_, err := s.StartInstall(context.Background(), "8.3")
	if err != ErrPPA {
		t.Fatalf("got %v", err)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
