package ha

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lnmp-panel/internal/exec"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/migrate"
	"lnmp-panel/internal/mysql"
	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

func TestNextInterval(t *testing.T) {
	if got := NextInterval(3 * time.Second); got != time.Minute {
		t.Fatalf("短轮次应保持 1 分钟，got %s", got)
	}
	got := NextInterval(90 * time.Second)
	if got != 100*time.Second {
		t.Fatalf("长轮次应为耗时+余量，got %s", got)
	}
	if SyncAlert(time.Minute) != "" {
		t.Fatal("1 分钟不应告警")
	}
	if SyncAlert(6*time.Minute) == "" {
		t.Fatal("超过 5 分钟应告警")
	}
}

func TestShouldDegrade(t *testing.T) {
	if ShouldDegrade(RoleMaster, 1, false, RoleMaster, 2) {
		t.Fatal("联系不上对端不得降级")
	}
	if ShouldDegrade(RoleMaster, 1, true, RoleStandby, 1) {
		t.Fatal("对端仍是备机不得降级")
	}
	if !ShouldDegrade(RoleMaster, 1, true, RoleMaster, 2) {
		t.Fatal("对端任期更大且已是主机应降级")
	}
	if ShouldDegrade(RoleMaster, 2, true, RoleMaster, 2) {
		t.Fatal("任期相同不降级")
	}
	if ShouldDegrade(RoleStandby, 1, true, RoleMaster, 2) {
		t.Fatal("本机已是备机无需自我保护")
	}
}

func TestHATokenSurvivesMigrateExpire(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := settings.New(db)
	mig := migrate.New(db)
	mig.Settings = st
	mig.Now = func() time.Time { return time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC) }
	mig.IdleFor = time.Hour
	_, err = db.SQL.Exec(`INSERT INTO pairing_tokens(token_hash, expires_at, purpose) VALUES ('ha-tok','2099-01-01T00:00:00Z','ha')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.SQL.Exec(`INSERT INTO pairing_tokens(token_hash, expires_at, purpose) VALUES ('mig-tok','2020-01-01T00:00:00Z','migrate')`)
	if err != nil {
		t.Fatal(err)
	}
	old := mig.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	_, err = db.SQL.Exec(`INSERT INTO peers(address, role, fingerprint, last_seen_at) VALUES ('https://b/x','migrate_source','fp',?)`, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := mig.ExpireStale(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM pairing_tokens WHERE token_hash='ha-tok'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("热备令牌不得被 7 天规则删除：%d %v", n, err)
	}
	if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM pairing_tokens WHERE token_hash='mig-tok'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("迁移令牌应被清理：%d %v", n, err)
	}
}

func setupHA(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := settings.New(db)
	s := New(db)
	s.Settings = st
	s.SkipExec = true
	s.Secrets = filepath.Join(dir, "sec")
	s.DataDir = dir
	s.DropInRoot = filepath.Join(dir, "systemd")
	s.WWW = filepath.Join(dir, "www")
	s.AuthKeys = filepath.Join(dir, "ak")
	tpl := template.New(db)
	mysqlSvc := mysql.New(db, tpl, jobs.New(db))
	mysqlSvc.SkipExec = true
	mysqlSvc.Defaults = filepath.Join(dir, "mysql-panel.cnf")
	s.MySQL = mysqlSvc
	mig := migrate.New(db)
	mig.SkipExec = true
	mig.Settings = st
	mig.Secrets = s.Secrets
	s.Migrate = mig
	return s
}

func TestRewriteURLHostUsesSourceIP(t *testing.T) {
	got := rewriteURLHost("https://VM-0-10-ubuntu:28443/abcdefabcdefabcdefabcdefabcdefab", "203.0.113.9")
	if got != "https://203.0.113.9:28443/abcdefabcdefabcdefabcdefabcdefab" {
		t.Fatalf("got %s", got)
	}
	got = rewriteURLHost("https://lnmp-ha-b:28443/pfx", "2001:db8::9")
	if got != "https://[2001:db8::9]:28443/pfx" {
		t.Fatalf("ipv6 %s", got)
	}
	if rewriteURLHost("https://keep.example:1/x", "") != "https://keep.example:1/x" {
		t.Fatal("无来源 IP 时应保留原地址")
	}
}

func TestAcceptRecordsPeerAddressAsIP(t *testing.T) {
	s := setupHA(t)
	s.MySQL = nil
	raw := "aaaaaaaaaaaaaaaa.0123456789abcdef"
	_, err := s.db.SQL.Exec(`INSERT INTO pairing_tokens(token_hash, expires_at, purpose) VALUES (?,?,?)`,
		migrate.HashToken(raw), "2099-01-01T00:00:00Z", "ha")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Accept(AcceptIn{
		Token: raw, SSHPub: "ssh-ed25519 AAAA ha", From: "203.0.113.20",
		Address: "https://VM-0-10-ubuntu:28443/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		SSHPort: 22, SSHUser: "root", Fingerprint: "fp",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://203.0.113.20:28443/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	got, _ := s.Settings.Get("ha_peer_address")
	if got != want {
		t.Fatalf("ha_peer_address=%s", got)
	}
	var addr string
	if err := s.db.SQL.QueryRow(`SELECT address FROM peers WHERE role='ha_peer' ORDER BY id DESC LIMIT 1`).Scan(&addr); err != nil {
		t.Fatal(err)
	}
	if addr != want {
		t.Fatalf("peers.address=%s", addr)
	}
	if strings.Contains(addr, "VM-0-10-ubuntu") || strings.Contains(got, "lnmp-ha-b") {
		t.Fatal("不得保留主机名")
	}
}

func TestMasterDropInWantsNginxMySQL(t *testing.T) {
	s := setupHA(t)
	if err := s.writeMasterDropIns(); err != nil {
		t.Fatal(err)
	}
	panel := filepath.Join(s.DropInRoot, "lnmp-panel.service.d", "lnmp-panel.conf")
	b, err := os.ReadFile(panel)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Wants=nginx.service mysql.service") {
		t.Fatalf("面板 drop-in：%s", b)
	}
	ngx := filepath.Join(s.DropInRoot, "nginx.service.d", "lnmp-panel.conf")
	nb, err := os.ReadFile(ngx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nb), "After=lnmp-panel.service") {
		t.Fatalf("nginx drop-in：%s", nb)
	}
	s.removeDropIns()
	if _, err := os.Stat(panel); !os.IsNotExist(err) {
		t.Fatal("退出主机角色应删除面板 drop-in")
	}
}

func TestNextTerm(t *testing.T) {
	if NextTerm(1, 1) != 2 {
		t.Fatal(NextTerm(1, 1))
	}
	if NextTerm(1, 2) != 3 {
		t.Fatal("应用已知对端任期")
	}
	if NextTerm(4, 2) != 5 {
		t.Fatal("本机更大时仍 +1")
	}
}

func TestTwoRoundSwitchDegradesOldMaster(t *testing.T) {
	// 第一轮：A term=1 主机，B 同步后 term=1，B 提升为 2。A 重启必须降级。
	if NextTerm(1, 1) != 2 {
		t.Fatal("第一轮提升")
	}
	if !ShouldDegrade(RoleMaster, 1, true, RoleMaster, 2) {
		t.Fatal("第一轮旧主机应降级")
	}
	// 第二轮：A 作为备机追上 term=2，提升为 3。B 重启必须降级。
	if NextTerm(2, 2) != 3 {
		t.Fatal("第二轮提升")
	}
	if !ShouldDegrade(RoleMaster, 2, true, RoleMaster, 3) {
		t.Fatal("第二轮旧主机应降级")
	}
	// 回归：任期未传播时两边都是 2，自我保护失效（审核实测双主）。
	if ShouldDegrade(RoleMaster, 2, true, RoleMaster, 2) {
		t.Fatal("任期相同不得降级（因此必须传播任期）")
	}
}

func TestDumpPeerMySQLRemovesRemoteDump(t *testing.T) {
	s := setupHA(t)
	s.SkipExec = false
	var cmds [][]string
	s.Runner = func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error) {
		cmds = append(cmds, append([]string(nil), argv...))
		return exec.Result{}, nil
	}
	dest := filepath.Join(s.DataDir, "got.sql")
	if err := s.dumpPeerMySQL(context.Background(), ReplSource{Host: "203.0.113.9"}, dest); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, c := range cmds {
		j := strings.Join(c, " ")
		switch {
		case strings.Contains(j, "mysqldump"):
			kinds = append(kinds, "dump")
		case len(c) > 0 && strings.Contains(c[0], "rsync"):
			kinds = append(kinds, "rsync")
		case strings.Contains(j, "rm -f /var/lib/lnmp-panel/tmp/ha-mysql.sql"):
			kinds = append(kinds, "rm")
		}
	}
	if strings.Join(kinds, ",") != "dump,rsync,rm" {
		t.Fatalf("应对端 dump→rsync→rm，got %v cmds=%v", kinds, cmds)
	}
}

func TestAdoptPeerTerm(t *testing.T) {
	s := setupHA(t)
	if err := s.saveRole(RoleStandby, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.adoptPeerTerm(2); err != nil {
		t.Fatal(err)
	}
	st, err := s.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Term != 2 || st.Role != RoleStandby {
		t.Fatalf("got %+v", st)
	}
	if s.knownPeerTerm() != 2 {
		t.Fatalf("peer term %d", s.knownPeerTerm())
	}
	if err := s.adoptPeerTerm(1); err != nil {
		t.Fatal(err)
	}
	st, _ = s.loadState()
	if st.Term != 2 {
		t.Fatal("任期不得回退")
	}
}

func TestPullAndAdoptTerm(t *testing.T) {
	s := setupHA(t)
	if err := s.saveRole(RoleStandby, 0, 1); err != nil {
		t.Fatal(err)
	}
	_ = s.Settings.Set("ha_peer_address", "https://peer.example:18443/pfx")
	_ = s.Settings.Set("ha_peer_fingerprint", "aa")
	_ = s.Settings.Set("ha_machine_token", "tok")
	s.Migrate.HTTPDo = func(req *http.Request) (*http.Response, error) {
		body := `{"ok":true,"data":{"role":"master","term":4,"degraded":false}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
	if err := s.pullAndAdoptTerm(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ := s.loadState()
	if st.Term != 4 {
		t.Fatalf("term %d", st.Term)
	}
}

func TestPromoteUsesMaxTermPlusOne(t *testing.T) {
	s := setupHA(t)
	s.MySQL = nil
	if err := s.saveRole(RoleStandby, 0, 1); err != nil {
		t.Fatal(err)
	}
	_ = s.Settings.Set("ha_peer_term", "2")
	if err := s.Promote(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ := s.loadState()
	if st.Role != RoleMaster || st.Term != 3 {
		t.Fatalf("want master term 3 got %s %d", st.Role, st.Term)
	}
}

func TestPromoteRequestsStepDown(t *testing.T) {
	s := setupHA(t)
	s.MySQL = nil
	if err := s.saveRole(RoleStandby, 0, 1); err != nil {
		t.Fatal(err)
	}
	_ = s.Settings.Set("ha_peer_address", "https://peer.example:18443/pfx")
	_ = s.Settings.Set("ha_peer_fingerprint", "aa")
	_ = s.Settings.Set("ha_machine_token", "tok")
	var paths []string
	s.Migrate.HTTPDo = func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.Method+" "+req.URL.Path)
		payload := `{"ok":true,"data":{"role":"master","term":1,"degraded":false}}`
		if strings.Contains(req.URL.Path, "step-down") {
			payload = `{"ok":true,"data":{}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
	}
	if err := s.Promote(context.Background()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range paths {
		if strings.Contains(p, "step-down") {
			found = true
		}
	}
	if !found {
		t.Fatalf("未调用 step-down: %v", paths)
	}
	st, _ := s.loadState()
	if st.Role != RoleMaster || st.Term != 2 {
		t.Fatalf("got %s %d", st.Role, st.Term)
	}
}

func TestStepDownMasterGoesReadOnly(t *testing.T) {
	s := setupHA(t)
	s.MySQL = nil
	if err := s.saveRole(RoleMaster, 0, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.StepDown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.degraded() {
		t.Fatal("应进入自我保护")
	}
	st, _ := s.loadState()
	if st.Role != RoleMaster {
		t.Fatalf("角色仍应是主机，got %s", st.Role)
	}
}

func TestStepDownStandbyNoop(t *testing.T) {
	s := setupHA(t)
	if err := s.saveRole(RoleStandby, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.StepDown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.degraded() {
		t.Fatal("备机不应被 step-down 标成 degraded")
	}
}

func TestValidMachineToken(t *testing.T) {
	s := setupHA(t)
	if err := s.ValidMachineToken(""); err == nil {
		t.Fatal("空令牌")
	}
	if err := s.ValidMachineToken("x"); err == nil {
		t.Fatal("未设置")
	}
	_ = s.Settings.Set("ha_machine_token", "secret-token-value")
	if err := s.ValidMachineToken("secret-token-value"); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidMachineToken("secret-token-valuE"); err == nil {
		t.Fatal("大小写不同应拒绝")
	}
	if err := s.ValidMachineToken("short"); err == nil {
		t.Fatal("长度不同应拒绝")
	}
}

func TestApplySharedPanelPassword(t *testing.T) {
	s := setupHA(t)
	if err := s.MySQL.WritePanelCNF("sharedPassAa1"); err != nil {
		t.Fatal(err)
	}
	if s.MySQL.PanelPassword() != "sharedPassAa1" {
		t.Fatalf("got %q", s.MySQL.PanelPassword())
	}
	b, err := os.ReadFile(s.MySQL.Defaults)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "lnmp_panel") || !strings.Contains(string(b), "sharedPassAa1") {
		t.Fatal(string(b))
	}
}

func TestPromoteRefusesWhenStepDownFails(t *testing.T) {
	s := setupHA(t)
	s.MySQL = nil
	if err := s.saveRole(RoleStandby, 0, 1); err != nil {
		t.Fatal(err)
	}
	_ = s.Settings.Set("ha_peer_address", "https://peer.example:18443/pfx")
	_ = s.Settings.Set("ha_peer_fingerprint", "aa")
	_ = s.Settings.Set("ha_machine_token", "tok")
	s.Migrate.HTTPDo = func(req *http.Request) (*http.Response, error) {
		body := `{"ok":false,"error":{"message":"MySQL 只读失败"}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
	err := s.Promote(context.Background())
	if err == nil || !strings.Contains(err.Error(), "只读失败") {
		t.Fatalf("可达但降级失败应拒绝提升：%v", err)
	}
	st, _ := s.loadState()
	if st.Role != RoleStandby {
		t.Fatalf("角色不应变：%s", st.Role)
	}
}

func TestPromoteAllowsUnreachablePeer(t *testing.T) {
	s := setupHA(t)
	s.MySQL = nil
	if err := s.saveRole(RoleStandby, 0, 1); err != nil {
		t.Fatal(err)
	}
	_ = s.Settings.Set("ha_peer_address", "https://peer.example:18443/pfx")
	_ = s.Settings.Set("ha_peer_fingerprint", "aa")
	s.Migrate.HTTPDo = func(req *http.Request) (*http.Response, error) {
		return nil, os.ErrDeadlineExceeded
	}
	if err := s.Promote(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ := s.loadState()
	if st.Role != RoleMaster {
		t.Fatalf("对端不可达应允许提升，got %s", st.Role)
	}
}
