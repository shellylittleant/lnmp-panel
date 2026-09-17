package firewall

import (
	"context"
	"os"
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
	if err := s.Apply(context.Background(), false); err != nil {
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
	if err := s.Apply(context.Background(), false); err != nil {
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

func TestBanIPv6UsesLoginBan6(t *testing.T) {
	s := setupFW(t)
	s.SkipNFT = false
	var argv [][]string
	s.Runner = func(ctx context.Context, d time.Duration, a ...string) (exec.Result, error) {
		argv = append(argv, append([]string{}, a...))
		return exec.Result{}, nil
	}
	if err := s.Ban("2001:db8::1"); err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, a := range argv {
		joined := strings.Join(a, " ")
		if strings.Contains(joined, "login_ban6") && strings.Contains(joined, "2001:db8::1") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("%v", argv)
	}
}

func TestGetDoesNotCallSystemctl(t *testing.T) {
	s := setupFW(t)
	s.SkipNFT = false
	n := 0
	s.Runner = func(ctx context.Context, d time.Duration, a ...string) (exec.Result, error) {
		if len(a) > 0 && a[0] == "/usr/bin/systemctl" {
			n++
		}
		return exec.Result{}, nil
	}
	if _, err := s.Get(); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("Get 不应调用 systemctl，实际 %d", n)
	}
	if err := s.SetupOnce(); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("SetupOnce 应 disable ufw / enable nftables")
	}
}

func TestDumpLivePrefixedWithFlush(t *testing.T) {
	s := setupFW(t)
	s.SkipNFT = false
	s.Runner = func(ctx context.Context, d time.Duration, a ...string) (exec.Result, error) {
		return exec.Result{Stdout: "table inet lnmp_panel {\n\tchain input {\n\t\ttcp dport 22 drop\n\t}\n}\n"}, nil
	}
	dest := filepath.Join(t.TempDir(), "live.nft")
	if err := s.dumpLive(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dest)
	if !strings.HasPrefix(string(b), "flush ruleset") {
		t.Fatalf("备份必须以 flush ruleset 开头：%s", b)
	}
	if !strings.Contains(string(b), "tcp dport 22 drop") {
		t.Fatalf("应保留 dump 内容：%s", b)
	}
}

func TestRecoverPendingClearsAndAudits(t *testing.T) {
	s := setupFW(t)
	bak := filepath.Join(s.BackupRoot, "live.nft")
	if err := os.MkdirAll(s.BackupRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bak, []byte("flush ruleset\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.SQL.Exec(`UPDATE firewall_state SET pending_backup=?, confirm_deadline=? WHERE id=1`, bak, "2099-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	var got string
	s.OnRecover = func(d string) { got = d }
	if err := s.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("应写审计回调")
	}
	_, _, pending, deadline, _ := s.state()
	if pending != "" || deadline != "" {
		t.Fatalf("pending 应清空 %q %q", pending, deadline)
	}
}

func TestEnsureWritesPlaceholderWithoutHook(t *testing.T) {
	s := setupFW(t)
	if err := os.Remove(s.Canonical); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.Canonical)
	if strings.Contains(string(b), "hook input") {
		t.Fatalf("占位不得带 hook：%s", b)
	}
	if !strings.Contains(string(b), "table inet lnmp_panel") {
		t.Fatalf("%s", b)
	}
}

func TestICMPInOfficialTemplates(t *testing.T) {
	s := setupFW(t)
	if err := s.Put(context.Background(), Form{SSHPort: 22, PanelPort: 18443, InputPolicy: "drop", PublicHTTP: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get()
	if !strings.Contains(v.Content, "meta l4proto { icmp, ipv6-icmp } accept") {
		t.Fatalf("缺 ICMP：%s", v.Content)
	}
	for _, name := range []string{"主机默认", "备机白名单", "仅本机面板"} {
		rec, err := s.tpl.OfficialByName("nftables", name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(rec.Content, "meta l4proto { icmp, ipv6-icmp } accept") {
			t.Fatalf("%s 缺 ICMP", name)
		}
		if !strings.Contains(rec.Content, "login_ban6") {
			t.Fatalf("%s 缺 login_ban6", name)
		}
		if !strings.Contains(rec.Content, "ct state invalid drop") {
			t.Fatalf("%s 缺 invalid drop", name)
		}
	}
}

func TestPublicHTTPDefaultTrue(t *testing.T) {
	s := setupFW(t)
	if err := s.UseTemplate(context.Background()); err != nil {
		t.Fatal(err)
	}
	body := s.draftBody()
	if !strings.Contains(body, "tcp dport { 80, 443 }") {
		t.Fatalf("默认应放行 80/443：%s", body)
	}
}

func TestParseSSHPortsAndListening(t *testing.T) {
	ports := ParseSSHPorts("# Port 22\nPort 2222\nPort 2222\nListenAddress 0.0.0.0\n")
	if len(ports) != 1 || ports[0] != 2222 {
		t.Fatalf("%v", ports)
	}
	cfg := filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(cfg, []byte("Port 2222\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := setupFW(t)
	s.SSHConfigPaths = []string{cfg}
	if err := s.UseTemplate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.draftBody(), "tcp dport 2222") {
		t.Fatalf("%s", s.draftBody())
	}
	proc := filepath.Join(t.TempDir(), "tcp")
	// 22 在听，2222 不在
	if err := os.WriteFile(proc, []byte("sl local_address rem_address st\n   0: 00000000:0016 00000000:0000 0A 0 0 0 0 0 0 0 0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s.ProcNetTCP = proc
	s.ProcNetTCP6 = filepath.Join(t.TempDir(), "missing")
	if err := s.Put(context.Background(), Form{SSHPort: 2222, PanelPort: 18443, InputPolicy: "drop", PublicHTTP: true, Force: true}); err != nil {
		t.Fatal(err)
	}
	err := s.Apply(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "SSH 端口 2222 上没有服务在监听") {
		t.Fatalf("got %v", err)
	}
	if err := s.Apply(context.Background(), true); err != nil {
		t.Fatal(err)
	}
}

func TestStageMigratedRewritesLocalPorts(t *testing.T) {
	s := setupFW(t)
	s.PanelPort = 28443
	_ = s.Settings.Set("listen_port", "28443")
	sshCfg := filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(sshCfg, []byte("Port 2222\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s.SSHConfigPaths = []string{sshCfg}
	if err := os.WriteFile(s.Canonical, []byte(Placeholder+"# local-only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	in := Form{SSHPort: 22, PanelPort: 18443, InputPolicy: "drop", PublicHTTP: true, OpenPorts: []int{8080}}
	if err := s.StageMigrated(in); err != nil {
		t.Fatal(err)
	}
	can, _ := os.ReadFile(s.Canonical)
	if strings.Contains(string(can), "18443") {
		t.Fatalf("开机文件不得含 A 的面板端口：%s", can)
	}
	if strings.Contains(string(can), "tcp dport 22") && strings.Contains(string(can), "comment \"ssh\"") {
		t.Fatalf("开机文件不得被改成 A 的 SSH：%s", can)
	}
	draft := s.draftBody()
	if strings.Contains(draft, "18443") {
		t.Fatalf("草稿不得含 A 端口：%s", draft)
	}
	if !strings.Contains(draft, "28443") {
		t.Fatalf("草稿应含 B 面板端口：%s", draft)
	}
	if !strings.Contains(draft, "2222") {
		t.Fatalf("草稿应含 B 的 SSH：%s", draft)
	}
	v, err := s.Get()
	if err != nil || !v.MigratePending {
		t.Fatalf("应标记迁移待应用 %+v %v", v, err)
	}
}

func TestWithFlush(t *testing.T) {
	out := withFlush("table inet x {\n}\n")
	if !strings.HasPrefix(out, "flush ruleset\n") {
		t.Fatal(out)
	}
	already := withFlush("flush ruleset\ntable inet x {}\n")
	if strings.Count(already, "flush ruleset") != 1 {
		t.Fatal(already)
	}
}
