package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/store"
)

func TestWriteMySQLRepoUsesOfficialAnd2025Key(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &StackInstaller{
		Jobs:     jobs.New(db),
		SkipExec: true,
		Codename: "noble",
		KeyPath:  filepath.Join(dir, "mysql.asc"),
		ListPath: filepath.Join(dir, "mysql-official.list"),
		HTTPGet: func(url string) ([]byte, error) {
			if !strings.Contains(url, "RPM-GPG-KEY-mysql-2025") {
				t.Fatalf("应下载 2025 密钥，得到 %s", url)
			}
			return []byte("KEYDATA"), nil
		},
	}
	if err := s.writeMySQLRepo(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.ListPath)
	txt := string(b)
	if !strings.Contains(txt, "repo.mysql.com/apt/ubuntu") || !strings.Contains(txt, "noble") || !strings.Contains(txt, "mysql-8.4-lts") {
		t.Fatalf("源内容不对：%s", txt)
	}
	if strings.Contains(txt, "archive.ubuntu.com") {
		t.Fatal("不得回退 Ubuntu 源")
	}
	k, _ := os.ReadFile(s.KeyPath)
	if string(k) != "KEYDATA" {
		t.Fatal(string(k))
	}
}

func TestMySQLSourceByArch(t *testing.T) {
	s := &StackInstaller{Arch: "arm64"}
	if s.useOfficialMySQL() || s.MySQLSource() != "ubuntu" {
		t.Fatal("arm64 应用 Ubuntu 源")
	}
	s.Arch = "amd64"
	if !s.useOfficialMySQL() || s.MySQLSource() != "official" {
		t.Fatal("amd64 应用官方源")
	}
	s.Arch = "386"
	if !s.useOfficialMySQL() {
		t.Fatal("386")
	}
}

func TestArm64MySQLInstallDoesNotWriteOfficialRepo(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &StackInstaller{
		Jobs:     jobs.New(db),
		SkipExec: true,
		Arch:     "arm64",
		Codename: "noble",
		KeyPath:  filepath.Join(dir, "mysql.asc"),
		ListPath: filepath.Join(dir, "mysql-official.list"),
		HTTPGet: func(url string) ([]byte, error) {
			t.Fatal("arm64 不应下载官方密钥")
			return nil, nil
		},
	}
	id, err := s.Jobs.Start("mysql-install")
	if err != nil {
		t.Fatal(err)
	}
	s.runMySQL(context.Background(), id)
	if _, err := os.Stat(s.ListPath); err == nil {
		t.Fatal("arm64 不得写官方源")
	}
	j, _ := s.Jobs.Get(id)
	if j.Phase != "done" {
		t.Fatalf("%+v", j)
	}
}
