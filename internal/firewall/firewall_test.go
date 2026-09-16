package firewall

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lnmp-panel/internal/exec"
	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

func setupFW(t *testing.T) *Service {
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
	st := settings.New(db)
	_ = st.Set("listen_port", "18443")
	_ = st.Set("ops_ip_cidrs", `["10.9.9.9/32"]`)
	s := New(db, tpl, st)
	s.SkipNFT = true
	s.PanelPort = 18443
	s.Canonical = filepath.Join(dir, "nftables.conf")
	s.Draft = filepath.Join(dir, "draft.nft")
	s.SystemNFT = filepath.Join(dir, "system.nft")
	s.BackupRoot = filepath.Join(dir, "bak")
	s.Window = 80 * time.Millisecond
	return s
}

func TestFormAlwaysHasSSHAndPanel(t *testing.T) {
	s := setupFW(t)
	in := Form{SSHPort: 22, PanelPort: 18443, InputPolicy: "drop", PublicHTTP: true}
	if err := s.Put(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	v, err := s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.Content, "tcp dport 22") || !strings.Contains(v.Content, "tcp dport 18443") {
		t.Fatalf("缺保护端口：%s", v.Content)
	}
	if !v.SSHProtected || !v.PanelProtected {
		t.Fatal(v)
	}
}

func TestCIDRInjectRejected(t *testing.T) {
	s := setupFW(t)
	in := Form{SSHPort: 22, PanelPort: 18443, InputPolicy: "drop", AllowCIDRs: []string{"1.2.3.4; drop"}}
	if err := s.Put(context.Background(), in); err == nil {
		t.Fatal("应拒绝")
	}
}

func TestRawModeBlocksForm(t *testing.T) {
	s := setupFW(t)
	if err := s.PutRaw(context.Background(), "flush ruleset\n", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), Form{SSHPort: 22, PanelPort: 18443, InputPolicy: "drop"}); err != ErrRawMode {
		t.Fatalf("got %v", err)
	}
	if err := s.UseTemplate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), Form{SSHPort: 22, PanelPort: 18443, InputPolicy: "drop"}); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmWindowAutoRollback(t *testing.T) {
	s := setupFW(t)
	if err := s.Put(context.Background(), Form{SSHPort: 22, PanelPort: 18443, InputPolicy: "drop"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get()
	if !v.Pending {
		t.Fatal("应进入确认窗口")
	}
	time.Sleep(200 * time.Millisecond)
	v, _ = s.Get()
	if v.Pending {
		t.Fatal("超时应自动回滚")
	}
}

func TestConfirmStopsTimer(t *testing.T) {
	s := setupFW(t)
	s.Window = time.Second
	if err := s.Put(context.Background(), Form{SSHPort: 22, PanelPort: 18443, InputPolicy: "drop"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Confirm(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get()
	if v.Pending {
		t.Fatal("确认后不应 pending")
	}
}

func TestBanSkipsOpsIP(t *testing.T) {
	s := setupFW(t)
	var argv [][]string
	s.SkipNFT = false
	s.Runner = func(ctx context.Context, d time.Duration, a ...string) (exec.Result, error) {
		argv = append(argv, append([]string{}, a...))
		return exec.Result{}, nil
	}
	if err := s.Ban("10.9.9.9"); err != nil {
		t.Fatal(err)
	}
	for _, a := range argv {
		if strings.Contains(strings.Join(a, " "), "10.9.9.9") {
			t.Fatalf("运维 IP 不应写入 nft：%v", a)
		}
	}
	if err := s.Ban("8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range argv {
		if strings.Contains(strings.Join(a, " "), "8.8.8.8") {
			found = true
		}
	}
	if !found {
		t.Fatal("非运维 IP 应写入 login_ban")
	}
}
