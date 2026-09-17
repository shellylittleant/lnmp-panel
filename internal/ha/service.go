package ha

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lnmp-panel/internal/exec"
	"lnmp-panel/internal/firewall"
	"lnmp-panel/internal/migrate"
	"lnmp-panel/internal/mysql"
	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/site"
	"lnmp-panel/internal/ssl"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

const (
	RoleStandalone = "standalone"
	RoleMaster     = "master"
	RoleStandby    = "standby"
)

type Status struct {
	Role            string `json:"role"`
	Term            int    `json:"term"`
	PeerID          int64  `json:"peer_id"`
	PeerAddress     string `json:"peer_address"`
	PeerFingerprint string `json:"peer_fingerprint"`
	LastSyncAt      string `json:"last_sync_at"`
	LastScanMs      int    `json:"last_scan_ms"`
	IntervalSec     int    `json:"interval_sec"`
	LagSec          int    `json:"lag_sec"`
	MysqlReplOK     bool   `json:"mysql_repl_ok"`
	Degraded        bool   `json:"degraded"`
	Syncing         bool   `json:"syncing"`
	Alert           string `json:"alert"`
	Detail          string `json:"detail"`
	NginxMode       string `json:"nginx_mode"`
	FirewallWarning string `json:"firewall_warning,omitempty"`
}

type RoleView struct {
	Role        string `json:"role"`
	Term        int    `json:"term"`
	Degraded    bool   `json:"degraded"`
	Fingerprint string `json:"fingerprint"`
}

type Health struct {
	Role     string `json:"role"`
	Term     int    `json:"term"`
	MySQL    string `json:"mysql"`
	Nginx    string `json:"nginx"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail"`
	Degraded bool   `json:"degraded"`
}

type SyncStatus struct {
	Syncing     bool   `json:"syncing"`
	LastSyncAt  string `json:"last_sync_at"`
	LastScanMs  int    `json:"last_scan_ms"`
	IntervalSec int    `json:"interval_sec"`
	LagSec      int    `json:"lag_sec"`
	MysqlReplOK bool   `json:"mysql_repl_ok"`
	Alert       string `json:"alert"`
	Detail      string `json:"detail"`
}

type PairIn struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}

type AcceptIn struct {
	Token       string `json:"token"`
	SSHPub      string `json:"ssh_pub"`
	From        string `json:"from"`
	Address     string `json:"address"`
	SSHHostKey  string `json:"ssh_host_key"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	Fingerprint string `json:"fingerprint"`
}

type AcceptOut struct {
	SSHUser      string `json:"ssh_user"`
	SSHPort      int    `json:"ssh_port"`
	SSHHostKey   string `json:"ssh_host_key"`
	SSHPub       string `json:"ssh_pub"`
	PeerID       int64  `json:"peer_id"`
	Term         int    `json:"term"`
	Role         string `json:"role"`
	MachineToken string `json:"machine_token"`
	Fingerprint  string `json:"fingerprint"`
}

type ReplSource struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	Pass string `json:"pass"`
}

type Service struct {
	db         *store.DB
	Migrate    *migrate.Service
	Sites      *site.Service
	Nginx      *nginx.Service
	MySQL      *mysql.Service
	Firewall   *firewall.Service
	Settings   *settings.Service
	SSL        *ssl.Service
	Tpl        *template.Service
	TLSCert    string
	DataDir    string
	WWW        string
	Secrets    string
	AuthKeys   string
	SSHUser    string
	SSHPort    int
	HostKeyPub string
	SkipExec   bool
	DropInRoot string
	Runner     func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error)
	Now        func() time.Time

	mu         sync.Mutex
	syncCancel context.CancelFunc
	syncing    bool
}

func New(db *store.DB) *Service {
	return &Service{
		db:         db,
		WWW:        "/var/www",
		Secrets:    "/var/lib/lnmp-panel/secrets",
		AuthKeys:   "/root/.ssh/authorized_keys",
		SSHUser:    "root",
		HostKeyPub: "/etc/ssh/ssh_host_ed25519_key.pub",
		Now:        func() time.Time { return time.Now().UTC() },
		Runner:     exec.Run,
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) Status() (Status, error) {
	st, err := s.loadState()
	if err != nil {
		return Status{}, err
	}
	st.Degraded = s.degraded()
	st.Syncing = s.isSyncing()
	st.Alert, _ = s.Settings.Get("ha_alert")
	st.Detail, _ = s.Settings.Get("ha_detail")
	st.FirewallWarning, _ = s.Settings.Get("ha_fw_warning")
	if s.Nginx != nil {
		st.NginxMode = s.Nginx.Mode()
	}
	addr, fp, _ := s.peerMeta()
	st.PeerAddress = addr
	st.PeerFingerprint = fp
	return st, nil
}

func (s *Service) SyncStatus() (SyncStatus, error) {
	st, err := s.Status()
	if err != nil {
		return SyncStatus{}, err
	}
	return SyncStatus{
		Syncing: st.Syncing, LastSyncAt: st.LastSyncAt, LastScanMs: st.LastScanMs,
		IntervalSec: st.IntervalSec, LagSec: st.LagSec, MysqlReplOK: st.MysqlReplOK,
		Alert: st.Alert, Detail: st.Detail,
	}, nil
}

func (s *Service) Health() (Health, error) {
	st, err := s.loadState()
	if err != nil {
		return Health{}, err
	}
	h := Health{Role: st.Role, Term: st.Term, Degraded: s.degraded(), OK: true}
	if s.MySQL != nil && s.MySQL.Installed() {
		h.MySQL = "已安装"
		if err := s.MySQL.EnsureRunning(context.Background()); err != nil {
			h.MySQL = err.Error()
			h.OK = false
		}
	} else {
		h.MySQL = "未安装"
	}
	if s.Nginx != nil && s.Nginx.Installed() {
		h.Nginx = "已安装"
	} else {
		h.Nginx = "未安装"
	}
	h.Detail, _ = s.Settings.Get("ha_detail")
	return h, nil
}

func (s *Service) PeerRole() (RoleView, error) {
	st, err := s.loadState()
	if err != nil {
		return RoleView{}, err
	}
	fp := ""
	if s.TLSCert != "" {
		fp, _ = migrate.FingerprintPEMFile(s.TLSCert)
	}
	return RoleView{Role: st.Role, Term: st.Term, Degraded: s.degraded(), Fingerprint: fp}, nil
}

func (s *Service) LocalRole() string {
	st, err := s.loadState()
	if err != nil {
		return RoleStandalone
	}
	return st.Role
}

func (s *Service) AllowCertIssue() bool {
	return s.LocalRole() != RoleStandby
}

func (s *Service) ValidMachineToken(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("缺少机器令牌")
	}
	want, _ := s.Settings.Get("ha_machine_token")
	if want == "" || raw != want {
		return fmt.Errorf("机器令牌无效")
	}
	return nil
}

type state struct {
	Role        string
	PeerID      int64
	Term        int
	LastSyncAt  string
	LastScanMs  int
	IntervalSec int
	LagSec      int
	MysqlReplOK bool
}

func (s *Service) loadState() (Status, error) {
	var st Status
	var peer sql.NullInt64
	var last sql.NullString
	var ok int
	err := s.db.SQL.QueryRow(`SELECT role, peer_id, term, last_sync_at, last_scan_ms, interval_sec, lag_sec, mysql_repl_ok FROM ha_state WHERE id=1`).
		Scan(&st.Role, &peer, &st.Term, &last, &st.LastScanMs, &st.IntervalSec, &st.LagSec, &ok)
	if err != nil {
		return st, err
	}
	if peer.Valid {
		st.PeerID = peer.Int64
	}
	if last.Valid {
		st.LastSyncAt = last.String
	}
	st.MysqlReplOK = ok == 1
	if st.Role == "" {
		st.Role = RoleStandalone
	}
	if st.IntervalSec <= 0 {
		st.IntervalSec = 60
	}
	return st, nil
}

func (s *Service) saveRole(role string, peerID int64, term int) error {
	_, err := s.db.SQL.Exec(`UPDATE ha_state SET role=?, peer_id=?, term=? WHERE id=1`, role, nullIf0(peerID), term)
	return err
}

func (s *Service) saveSync(scan time.Duration, interval time.Duration, lag int, replOK bool, alert, detail string) {
	now := s.now().Format(time.RFC3339)
	ok := 0
	if replOK {
		ok = 1
	}
	_, _ = s.db.SQL.Exec(`UPDATE ha_state SET last_sync_at=?, last_scan_ms=?, interval_sec=?, lag_sec=?, mysql_repl_ok=? WHERE id=1`,
		now, int(scan.Milliseconds()), int(interval.Seconds()), lag, ok)
	if s.Settings != nil {
		_ = s.Settings.Set("ha_alert", alert)
		_ = s.Settings.Set("ha_detail", detail)
	}
}

func (s *Service) setDetail(v string) {
	if s.Settings != nil {
		_ = s.Settings.Set("ha_detail", v)
	}
}

func (s *Service) degraded() bool {
	if s.Settings == nil {
		return false
	}
	v, _ := s.Settings.Get("ha_degraded")
	return v == "1"
}

func (s *Service) setDegraded(on bool) {
	if s.Settings == nil {
		return
	}
	if on {
		_ = s.Settings.Set("ha_degraded", "1")
	} else {
		_ = s.Settings.Set("ha_degraded", "")
	}
}

func (s *Service) isSyncing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncing
}

func (s *Service) peerMeta() (addr, fp string, err error) {
	if s.Settings == nil {
		return "", "", fmt.Errorf("尚未配对")
	}
	addr, _ = s.Settings.Get("ha_peer_address")
	fp, _ = s.Settings.Get("ha_peer_fingerprint")
	if addr == "" {
		_ = s.db.SQL.QueryRow(`SELECT address, fingerprint FROM peers WHERE role='ha_peer' ORDER BY id DESC LIMIT 1`).Scan(&addr, &fp)
	}
	if addr == "" {
		return "", "", fmt.Errorf("尚未配对")
	}
	return addr, fp, nil
}

func (s *Service) machineToken() string {
	v, _ := s.Settings.Get("ha_machine_token")
	return v
}

func (s *Service) sshListenPort() int {
	if s.SSHPort > 0 {
		return s.SSHPort
	}
	if p := firewall.DetectSSHPorts(); len(p) > 0 {
		return p[0]
	}
	return 22
}

func (s *Service) peerSSH() (user string, port int) {
	user = s.SSHUser
	port = s.sshListenPort()
	if s.Settings != nil {
		if v, _ := s.Settings.Get("ha_peer_ssh_user"); v != "" {
			user = v
		}
		if v, _ := s.Settings.Get("ha_peer_ssh_port"); v != "" {
			_, _ = fmt.Sscanf(v, "%d", &port)
		}
	}
	if user == "" {
		user = "root"
	}
	if port <= 0 {
		port = 22
	}
	return user, port
}

func (s *Service) applyNginxCatchall() error {
	if s.Nginx == nil {
		return nil
	}
	content := nginxCatchallDefault
	if s.Tpl != nil {
		if rec, err := s.Tpl.OfficialByName("nginx_catchall_404", "备机 404"); err == nil {
			if out, err := template.Render(rec.Name, rec.Content, struct{}{}); err == nil {
				content = out
			}
		}
	}
	if err := s.Nginx.EnterCatchall404(content); err != nil {
		return err
	}
	if s.SkipExec || !s.Nginx.Installed() {
		return nil
	}
	return s.Nginx.TestAndReload(context.Background())
}

func (s *Service) applyNginxMaintenance() error {
	if s.Nginx == nil {
		return nil
	}
	if err := s.Nginx.EnterMaintenance(); err != nil {
		return err
	}
	if s.SkipExec || !s.Nginx.Installed() {
		return nil
	}
	return s.Nginx.TestAndReload(context.Background())
}

func (s *Service) applyNginxSites() error {
	if s.Nginx == nil {
		return nil
	}
	var keys []string
	if s.Sites != nil {
		items, err := s.Sites.List()
		if err != nil {
			return err
		}
		for _, it := range items {
			if it.Enabled {
				keys = append(keys, it.SiteKey)
			}
		}
	}
	if err := s.Nginx.EnableListed(keys); err != nil {
		return err
	}
	if s.SkipExec || !s.Nginx.Installed() {
		return nil
	}
	return s.Nginx.TestAndReload(context.Background())
}

func (s *Service) applyHAFirewall(role, peerIP string) {
	if s.Firewall == nil {
		return
	}
	if err := s.Firewall.ApplyHA(context.Background(), role, peerIP); err != nil {
		_ = s.Settings.Set("ha_fw_warning", err.Error())
		return
	}
	_ = s.Settings.Set("ha_fw_warning", "")
}

func (s *Service) peerIP() string {
	if s.Settings == nil {
		return ""
	}
	v, _ := s.Settings.Get("ha_peer_ip")
	return v
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nullIf0(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func resolveHostIP(host string) string {
	host = strings.TrimSpace(host)
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
	}
	if len(ips) > 0 {
		return ips[0].String()
	}
	return ""
}

const nginxCatchallDefault = `server {
	listen 80 default_server;
	listen [::]:80 default_server;
	server_name _;
	return 404;
}
`

func (s *Service) haKeyPath() string {
	return filepath.Join(s.Secrets, "ha-key")
}

func (s *Service) haKnownHosts() string {
	return filepath.Join(s.Secrets, "ha-known_hosts")
}

func (s *Service) ensureHAKey(ctx context.Context) (pub string, err error) {
	key := s.haKeyPath()
	if _, err := os.Stat(key); err == nil {
		b, err := os.ReadFile(key + ".pub")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	if err := os.MkdirAll(s.Secrets, 0700); err != nil {
		return "", err
	}
	if s.SkipExec {
		_ = os.WriteFile(key, []byte("FAKE-HA"), 0600)
		_ = os.WriteFile(key+".pub", []byte("ssh-ed25519 AAAA ha"), 0600)
		return "ssh-ed25519 AAAA ha", nil
	}
	run := s.Runner
	if run == nil {
		run = exec.Run
	}
	r, err := run(ctx, 10*time.Second, "/usr/bin/ssh-keygen", "-t", "ed25519", "-f", key, "-N", "", "-q")
	if err != nil {
		return "", fmt.Errorf("生成热备 SSH 钥：%s", exec.Clip(r.Stderr))
	}
	b, err := os.ReadFile(key + ".pub")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (s *Service) hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		h = "lnmp"
	}
	return h
}

func (s *Service) advertisedURL() string {
	if s.Settings == nil {
		return ""
	}
	pfx, _ := s.Settings.Get("path_prefix")
	port, _ := s.Settings.Get("listen_port")
	if strings.Trim(pfx, "/") == "" || port == "" {
		return ""
	}
	return "https://" + net.JoinHostPort(s.hostname(), port) + "/" + strings.Trim(pfx, "/")
}
