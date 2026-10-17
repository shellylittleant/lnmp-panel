package nginx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAndReloadRequiresInstall(t *testing.T) {
	s := New()
	s.Bin = "/no/such/nginx-bin"
	err := s.TestAndReload(context.Background())
	if err == nil || !strings.Contains(err.Error(), "未安装") {
		t.Fatalf("未安装应报错：%v", err)
	}
	_ = time.Second
}

func TestCatchallAndMaintenance(t *testing.T) {
	dir := t.TempDir()
	s := New()
	s.Available = filepath.Join(dir, "avail")
	s.Enabled = filepath.Join(dir, "en")
	s.ConfD = filepath.Join(dir, "conf.d")
	s.AcmeWWW = filepath.Join(dir, "acme")
	s.BackupRoot = filepath.Join(dir, "bak")
	s.HAWWW = filepath.Join(dir, "ha-www")
	if err := s.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSite("demo", "server{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.Enable("demo"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnterCatchall404("server { return 404; }\n"); err != nil {
		t.Fatal(err)
	}
	if s.Mode() != "catchall_404" {
		t.Fatalf("mode %s", s.Mode())
	}
	ents, _ := os.ReadDir(s.Enabled)
	if len(ents) != 1 || ents[0].Name() != CatchallFile {
		t.Fatalf("enabled=%v", ents)
	}
	if err := s.EnterMaintenance(); err != nil {
		t.Fatal(err)
	}
	if s.Mode() != "maintenance" {
		t.Fatalf("mode %s", s.Mode())
	}
	if err := s.EnableListed([]string{"demo"}); err != nil {
		t.Fatal(err)
	}
	if s.Mode() != "sites" {
		t.Fatalf("mode %s", s.Mode())
	}
}
