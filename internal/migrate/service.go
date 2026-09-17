package migrate

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lnmp-panel/internal/env"
	"lnmp-panel/internal/exec"
	"lnmp-panel/internal/firewall"
	panelFS "lnmp-panel/internal/fs"
	"lnmp-panel/internal/mysql"
	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/php"
	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/site"
	"lnmp-panel/internal/ssl"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"

	"golang.org/x/sys/unix"
)

type Service struct {
	db         *store.DB
	Sites      *site.Service
	Nginx      *nginx.Service
	PHP        *php.Service
	MySQL      *mysql.Service
	SSL        *ssl.Service
	Tpl        *template.Service
	Firewall   *firewall.Service
	Settings   *settings.Service
	OS         env.OSInfo
	TLSCert    string
	DataDir    string
	WWW        string
	Secrets    string
	AuthKeys   string
	SSHUser    string
	SSHPort    int
	HostKeyPub string
	SkipExec   bool
	Runner     func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error)
	IORunner   func(ctx context.Context, d time.Duration, stdin io.Reader, argv ...string) (exec.Result, error)
	HTTPDo     func(*http.Request) (*http.Response, error)
	DialFP     func(ctx context.Context, hostPort string) (string, error)
	TokenTTL   time.Duration
	IdleFor    time.Duration
	Now        func() time.Time

	mu        sync.Mutex
	runCancel context.CancelFunc
}

func New(db *store.DB) *Service {
	return &Service{
		db: db, WWW: "/var/www", Secrets: "/var/lib/lnmp-panel/secrets",
		AuthKeys: "/root/.ssh/authorized_keys", SSHUser: "root",
		HostKeyPub: "/etc/ssh/ssh_host_ed25519_key.pub",
		TokenTTL:   15 * time.Minute, IdleFor: 7 * 24 * time.Hour,
		Now:    func() time.Time { return time.Now().UTC() },
		Runner: exec.Run, IORunner: exec.RunIO,
	}
}

func (s *Service) idleFor() time.Duration {
	if s.IdleFor > 0 {
		return s.IdleFor
	}
	return 7 * 24 * time.Hour
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

func (s *Service) dumpFile() string {
	if s.DataDir == "" {
		return "/var/lib/lnmp-panel/tmp/migrate-mysql.sql"
	}
	return filepath.Join(s.DataDir, "tmp", "migrate-mysql.sql")
}

type TokenOut struct {
	Token      string `json:"token"`
	ExpiresAt  string `json:"expires_at"`
	Prefix     string `json:"fingerprint_prefix"`
	TTLSeconds int    `json:"ttl_seconds"`
}

func (s *Service) Token() (TokenOut, error) {
	fp, err := FingerprintPEMFile(s.TLSCert)
	if err != nil {
		return TokenOut{}, fmt.Errorf("读取本机证书指纹：%w", err)
	}
	raw, err := NewToken(fp)
	if err != nil {
		return TokenOut{}, err
	}
	exp := s.Now().Add(s.TokenTTL)
	_, _ = s.db.SQL.Exec(`DELETE FROM pairing_tokens WHERE expires_at < ?`, s.Now().Format(time.RFC3339))
	_, err = s.db.SQL.Exec(`INSERT INTO pairing_tokens(token_hash, expires_at) VALUES (?,?)`, HashToken(raw), exp.Format(time.RFC3339))
	if err != nil {
		return TokenOut{}, err
	}
	return TokenOut{Token: raw, ExpiresAt: exp.Format(time.RFC3339), Prefix: FingerprintPrefix(fp), TTLSeconds: int(s.TokenTTL.Seconds())}, nil
}

type AcceptIn struct {
	Token  string `json:"token"`
	SSHPub string `json:"ssh_pub"`
	From   string `json:"from"`
}

type AcceptOut struct {
	SSHUser    string `json:"ssh_user"`
	SSHPort    int    `json:"ssh_port"`
	SSHHostKey string `json:"ssh_host_key"`
	PeerID     int64  `json:"peer_id"`
}

func (s *Service) Accept(in AcceptIn) (AcceptOut, error) {
	if _, _, err := ParseToken(in.Token); err != nil {
		return AcceptOut{}, err
	}
	h := HashToken(in.Token)
	var exp, used sql.NullString
	err := s.db.SQL.QueryRow(`SELECT expires_at, used_at FROM pairing_tokens WHERE token_hash=?`, h).Scan(&exp, &used)
	if err != nil {
		return AcceptOut{}, fmt.Errorf("令牌无效")
	}
	if exp.String < s.Now().Format(time.RFC3339) {
		return AcceptOut{}, fmt.Errorf("令牌已过期")
	}
	if used.Valid && used.String != "" {
		return AcceptOut{}, fmt.Errorf("令牌已使用")
	}
	if err := upsertAuthorizedKeys(s.AuthKeys, in.SSHPub, in.From); err != nil {
		return AcceptOut{}, fmt.Errorf("写入授权公钥：%w", err)
	}
	now := s.Now().Format(time.RFC3339)
	_, _ = s.db.SQL.Exec(`UPDATE pairing_tokens SET used_at=? WHERE token_hash=?`, now, h)
	addr := in.From
	if addr == "" {
		addr = "unknown"
	}
	res, err := s.db.SQL.Exec(`INSERT INTO peers(address, role, fingerprint, last_seen_at) VALUES (?,?,?,?)`,
		addr, "migrate_source", "", now)
	if err != nil {
		return AcceptOut{}, err
	}
	id, _ := res.LastInsertId()
	hostKey, _ := readHostKeyPub(s.HostKeyPub)
	port := s.sshListenPort()
	user := s.SSHUser
	if user == "" {
		user = "root"
	}
	if s.Settings != nil {
		_ = s.Settings.Set("migrate_active_token_hash", h)
	}
	return AcceptOut{SSHUser: user, SSHPort: port, SSHHostKey: hostKey, PeerID: id}, nil
}

func (s *Service) validPeerToken(raw string) error {
	h := HashToken(raw)
	var exp, used sql.NullString
	err := s.db.SQL.QueryRow(`SELECT expires_at, used_at FROM pairing_tokens WHERE token_hash=?`, h).Scan(&exp, &used)
	if err != nil {
		return fmt.Errorf("令牌无效")
	}
	if !used.Valid || used.String == "" {
		return fmt.Errorf("令牌尚未握手")
	}
	if s.stalePairing() {
		_ = s.ExpireStale()
		return fmt.Errorf("配对已过期")
	}
	now := s.Now().Format(time.RFC3339)
	_, _ = s.db.SQL.Exec(`UPDATE peers SET last_seen_at=? WHERE role='migrate_source'`, now)
	return nil
}

func (s *Service) stalePairing() bool {
	var last sql.NullString
	_ = s.db.SQL.QueryRow(`SELECT last_seen_at FROM peers WHERE role='migrate_source' ORDER BY id DESC LIMIT 1`).Scan(&last)
	if !last.Valid || last.String == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, last.String)
	if err != nil {
		return false
	}
	return s.Now().After(t.Add(s.idleFor()))
}

func (s *Service) ExpireStale() error {
	if !s.stalePairing() {
		return nil
	}
	_ = removeAuthorizedKeys(s.AuthKeys)
	_, _ = s.db.SQL.Exec(`DELETE FROM pairing_tokens`)
	return nil
}

type ConnectIn struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}

type ConnectOut struct {
	PeerID      int64  `json:"peer_id"`
	Fingerprint string `json:"fingerprint"`
	SSHUser     string `json:"ssh_user"`
	SSHPort     int    `json:"ssh_port"`
}

func (s *Service) Connect(ctx context.Context, in ConnectIn) (ConnectOut, error) {
	prefix, _, err := ParseToken(in.Token)
	if err != nil {
		return ConnectOut{}, err
	}
	hostPort, _, err := SplitHostPortFromAddr(in.Address)
	if err != nil {
		return ConnectOut{}, err
	}
	dial := s.DialFP
	if dial == nil {
		dial = DialFingerprint
	}
	fp, err := dial(ctx, hostPort)
	if err != nil {
		return ConnectOut{}, err
	}
	if !strings.HasPrefix(fp, prefix) {
		return ConnectOut{}, fmt.Errorf("TLS 指纹与令牌不符")
	}
	if err := panelFS.Mkdir(s.Secrets, 0700); err != nil {
		return ConnectOut{}, err
	}
	keyPath := filepath.Join(s.Secrets, "migrate-key")
	_ = os.Remove(keyPath)
	_ = os.Remove(keyPath + ".pub")
	if !s.SkipExec {
		run := s.Runner
		if run == nil {
			run = exec.Run
		}
		r, err := run(ctx, 10*time.Second, "/usr/bin/ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "", "-q")
		if err != nil {
			return ConnectOut{}, fmt.Errorf("生成 SSH 钥：%s", exec.Clip(r.Stderr))
		}
	} else {
		_ = os.WriteFile(keyPath, []byte("FAKE"), 0600)
		_ = os.WriteFile(keyPath+".pub", []byte("ssh-ed25519 AAAA test"), 0600)
	}
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return ConnectOut{}, err
	}
	var acc AcceptOut
	if err := s.postPeer(ctx, in.Address, fp, prefix, "/migrate/accept", AcceptIn{Token: in.Token, SSHPub: strings.TrimSpace(string(pub))}, &acc); err != nil {
		_ = os.Remove(keyPath)
		_ = os.Remove(keyPath + ".pub")
		return ConnectOut{}, err
	}
	sshHost, err := sshHostOf(in.Address)
	if err != nil {
		return ConnectOut{}, err
	}
	if acc.SSHHostKey == "" {
		return ConnectOut{}, fmt.Errorf("对端未提供 SSH 主机钥")
	}
	port := acc.SSHPort
	if port <= 0 {
		port = 22
	}
	user := acc.SSHUser
	if user == "" {
		user = "root"
	}
	kh := filepath.Join(s.Secrets, "migrate-known_hosts")
	if err := writeKnownHosts(kh, sshHost, port, acc.SSHHostKey); err != nil {
		return ConnectOut{}, fmt.Errorf("写入 known_hosts：%w", err)
	}
	now := s.Now().Format(time.RFC3339)
	res, err := s.db.SQL.Exec(`INSERT INTO peers(address, role, fingerprint, last_seen_at) VALUES (?,?,?,?)`,
		in.Address, "migrate_target", fp, now)
	if err != nil {
		return ConnectOut{}, err
	}
	id, _ := res.LastInsertId()
	if s.Settings != nil {
		_ = s.Settings.Set("migrate_peer_token", in.Token)
		_ = s.Settings.Set("migrate_peer_id", fmt.Sprintf("%d", id))
		_ = s.Settings.Set("migrate_peer_ssh_user", user)
		_ = s.Settings.Set("migrate_peer_ssh_port", fmt.Sprintf("%d", port))
	}
	return ConnectOut{PeerID: id, Fingerprint: fp, SSHUser: user, SSHPort: port}, nil
}

type Preflight struct {
	OK        bool     `json:"ok"`
	OS        string   `json:"os"`
	DiskOK    bool     `json:"disk_ok"`
	FreeB     int64    `json:"free_bytes"`
	NeedB     int64    `json:"need_bytes"`
	PHP       string   `json:"php"`
	MySQL     string   `json:"mysql"`
	Ports     string   `json:"ports"`
	Errors    []string `json:"errors"`
	SiteCount int      `json:"site_count"`
	SiteKeys  []string `json:"site_keys"`
}

type PreflightIn struct {
	Token     string `json:"token"`
	NeedBytes int64  `json:"need_bytes"`
}

func (s *Service) PreflightLocal(need int64) Preflight {
	p := Preflight{Errors: []string{}}
	if s.OS.ID != "" {
		if err := env.CheckSupported(s.OS); err != nil {
			p.Errors = append(p.Errors, err.Error())
		} else {
			p.OS = s.OS.Pretty
		}
	} else if info, err := env.MustCurrentOS(); err != nil {
		p.Errors = append(p.Errors, err.Error())
	} else {
		p.OS = info.Pretty
	}
	var st unix.Statfs_t
	root := s.WWW
	if root == "" {
		root = "/"
	}
	if err := unix.Statfs(root, &st); err == nil {
		p.FreeB = int64(st.Bavail) * int64(st.Bsize)
		p.NeedB = need
		p.DiskOK = p.FreeB > need+1<<30
		if !p.DiskOK {
			p.Errors = append(p.Errors, "磁盘空间不足")
		}
	}
	p.PHP = "可按面板流程安装或已安装"
	p.MySQL = "可按面板流程安装或已安装"
	if s.MySQL != nil && s.MySQL.MariaDB(context.Background()) {
		p.MySQL = "检测到 MariaDB"
		p.Errors = append(p.Errors, mysql.ErrMariaDB.Error())
	}
	if procHas("apache2") || procHas("httpd") {
		p.Ports = "80/443 被非 Nginx 占用"
		p.Errors = append(p.Errors, "80/443 被 Apache 占用")
	} else {
		p.Ports = "80/443 将由 Nginx 使用"
	}
	_ = panelFS.Mkdir(s.WWW, 0755)
	if s.DataDir != "" {
		_ = panelFS.Mkdir(filepath.Join(s.DataDir, "tmp"), 0700)
	}
	if s.Nginx != nil {
		_ = s.Nginx.EnsureLayout()
	}
	if s.Sites != nil {
		if items, err := s.Sites.List(); err == nil {
			p.SiteCount = len(items)
			p.SiteKeys = make([]string, 0, len(items))
			for _, it := range items {
				p.SiteKeys = append(p.SiteKeys, it.SiteKey)
			}
		}
	}
	p.OK = len(p.Errors) == 0
	return p
}

func procHas(name string) bool {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range ents {
		b, err := os.ReadFile("/proc/" + e.Name() + "/comm")
		if err == nil && strings.TrimSpace(string(b)) == name {
			return true
		}
	}
	return false
}

func (s *Service) PreflightPeer(ctx context.Context, need int64) (Preflight, error) {
	addr, token, fp, err := s.activePeer()
	if err != nil {
		return Preflight{}, err
	}
	var out Preflight
	err = s.postPeer(ctx, addr, fp, "", "/migrate/peer/preflight", PreflightIn{Token: token, NeedBytes: need}, &out)
	return out, err
}

func (s *Service) PeerPreflight(in PreflightIn) (Preflight, error) {
	if err := s.validPeerToken(in.Token); err != nil {
		return Preflight{}, err
	}
	return s.PreflightLocal(in.NeedBytes), nil
}

type Status struct {
	PeerID      int64  `json:"peer_id"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	Phase       string `json:"phase"`
	Detail      string `json:"detail"`
	Progress    int    `json:"progress"`
	Mode        string `json:"mode"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at"`
}

func (s *Service) Status() (Status, error) {
	var st Status
	err := s.db.SQL.QueryRow(`SELECT id, address, fingerprint FROM peers WHERE role='migrate_target' ORDER BY id DESC LIMIT 1`).
		Scan(&st.PeerID, &st.Address, &st.Fingerprint)
	if err != nil && err != sql.ErrNoRows {
		return st, err
	}
	var fin sql.NullString
	_ = s.db.SQL.QueryRow(`SELECT mode, phase, detail, progress, started_at, finished_at FROM migrate_jobs ORDER BY id DESC LIMIT 1`).
		Scan(&st.Mode, &st.Phase, &st.Detail, &st.Progress, &st.StartedAt, &fin)
	if fin.Valid {
		st.FinishedAt = fin.String
	}
	return st, nil
}

func (s *Service) Start(ctx context.Context, mode string, confirmReplace bool) (int64, error) {
	if mode != "full" && mode != "incr" {
		return 0, fmt.Errorf("mode 只能是 full 或 incr")
	}
	addr, token, fp, err := s.activePeer()
	if err != nil {
		return 0, err
	}
	pf, err := s.PreflightPeer(ctx, 1<<30)
	if err != nil {
		return 0, err
	}
	if !pf.OK {
		return 0, fmt.Errorf("预检失败：%s", strings.Join(pf.Errors, "；"))
	}
	if pf.SiteCount > 0 && !confirmReplace {
		return 0, fmt.Errorf("目标机已有 %d 个站点将被替换，请确认", pf.SiteCount)
	}
	var peerID int64
	_ = s.db.SQL.QueryRow(`SELECT id FROM peers WHERE role='migrate_target' ORDER BY id DESC LIMIT 1`).Scan(&peerID)
	if peerID == 0 {
		return 0, fmt.Errorf("尚未配对")
	}
	var n int
	_ = s.db.SQL.QueryRow(`SELECT COUNT(1) FROM migrate_jobs WHERE finished_at IS NULL`).Scan(&n)
	if n > 0 {
		return 0, fmt.Errorf("已有迁移任务在执行")
	}
	now := s.Now().Format(time.RFC3339)
	res, err := s.db.SQL.Exec(`INSERT INTO migrate_jobs(peer_id, mode, phase, detail, progress, started_at) VALUES (?,?,?,?,0,?)`,
		peerID, mode, "preflight", "开始", now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.runCancel != nil {
		s.runCancel()
	}
	s.runCancel = cancel
	s.mu.Unlock()
	go s.runSync(runCtx, id, mode, addr, token, fp)
	return id, nil
}

func (s *Service) setJob(id int64, phase, detail string, progress int) {
	_, _ = s.db.SQL.Exec(`UPDATE migrate_jobs SET phase=?, detail=?, progress=? WHERE id=?`, phase, detail, progress, id)
}

func (s *Service) finishJob(id int64, err error, extra string) {
	phase, detail := "done", "完成"
	if err != nil {
		phase, detail = "failed", err.Error()
	} else if extra != "" {
		detail = extra
	}
	now := s.Now().Format(time.RFC3339)
	_, _ = s.db.SQL.Exec(`UPDATE migrate_jobs SET phase=?, detail=?, progress=100, finished_at=? WHERE id=?`, phase, detail, now, id)
}

func (s *Service) runSync(ctx context.Context, jobID int64, mode, addr, token, fp string) {
	extra, err := s.syncOnce(ctx, jobID, mode, addr, token, fp)
	s.finishJob(jobID, err, extra)
}

func (s *Service) syncOnce(ctx context.Context, jobID int64, mode, addr, token, fp string) (string, error) {
	s.setJob(jobID, "preflight", "目标机预检", 5)
	need := int64(1 << 30)
	pf, err := s.PreflightPeer(ctx, need)
	if err != nil {
		return "", err
	}
	if !pf.OK {
		return "", fmt.Errorf("预检失败：%s", strings.Join(pf.Errors, "；"))
	}
	sshHost, err := sshHostOf(addr)
	if err != nil {
		return "", err
	}
	user, _ := s.peerSSH()
	dest := func(p string) string { return user + "@" + sshHost + ":" + p }
	s.setJob(jobID, mode, "同步站点文件", 20)
	if err := s.rsyncPath(ctx, s.WWW+"/", dest(s.WWW+"/")); err != nil {
		return "", fmt.Errorf("rsync 站点：%w", err)
	}
	s.setJob(jobID, mode, "同步证书与密钥", 40)
	acme := filepath.Join(s.Secrets, "acme.key")
	_ = s.rsyncPath(ctx, acme, dest(acme))
	if s.Nginx != nil && s.Nginx.ConfD != "" {
		_ = s.rsyncPath(ctx, s.Nginx.ConfD+"/", dest(s.Nginx.ConfD+"/"))
	}
	if s.MySQL != nil && s.MySQL.CNFPath != "" {
		_ = s.rsyncPath(ctx, s.MySQL.CNFPath, dest(s.MySQL.CNFPath))
	}
	s.setJob(jobID, mode, "导出数据库", 55)
	dump := s.dumpFile()
	if err := s.dumpMySQL(ctx, dump); err != nil {
		return "", err
	}
	if _, err := os.Stat(dump); err == nil {
		bDump := "/var/lib/lnmp-panel/tmp/migrate-mysql.sql"
		if err := s.rsyncPath(ctx, dump, dest(bDump)); err != nil {
			return "", fmt.Errorf("rsync 数据库导出：%w", err)
		}
	}
	s.setJob(jobID, mode, "提交元数据", 75)
	bundle, err := s.exportBundle(ctx)
	if err != nil {
		return "", err
	}
	var applied ApplyOut
	if err := s.postPeer(ctx, addr, fp, "", "/migrate/peer/apply", map[string]any{"token": token, "bundle": bundle}, &applied); err != nil {
		return "", err
	}
	_ = os.Remove(dump)
	extra := "完成"
	if len(applied.Missing) > 0 {
		extra = "文件与配置已同步，目标机还需安装：" + strings.Join(applied.Missing, "、")
	}
	s.setJob(jobID, mode, extra, 95)
	return extra, nil
}

func (s *Service) dumpMySQL(ctx context.Context, path string) error {
	if s.MySQL == nil || s.SkipExec {
		return nil
	}
	if !s.MySQL.Installed() {
		return nil
	}
	if err := s.MySQL.Refuse(ctx); err != nil {
		return err
	}
	dbs, err := s.MySQL.ListDB()
	if err != nil {
		return err
	}
	if len(dbs) == 0 {
		return nil
	}
	names := make([]string, 0, len(dbs))
	for _, d := range dbs {
		names = append(names, d.Name)
	}
	if err := panelFS.Mkdir(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := s.MySQL.DumpDatabasesTo(ctx, names, f); err != nil {
		return err
	}
	users, _ := s.MySQL.ListUsers()
	for _, u := range users {
		if mysql.IsReservedUser(u.User) {
			continue
		}
		sqlu, err := s.MySQL.UserDumpSQL(ctx, u.User, u.Host)
		if err != nil {
			return err
		}
		if _, err := f.WriteString(sqlu); err != nil {
			return err
		}
	}
	if _, err := f.WriteString("FLUSH PRIVILEGES;\n"); err != nil {
		return err
	}
	_ = f.Chmod(0600)
	return nil
}

func (s *Service) peerSSH() (user string, port int) {
	user = s.SSHUser
	port = s.SSHPort
	if s.Settings != nil {
		if v, _ := s.Settings.Get("migrate_peer_ssh_user"); v != "" {
			user = v
		}
		if v, _ := s.Settings.Get("migrate_peer_ssh_port"); v != "" {
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

func (s *Service) rsyncPath(ctx context.Context, src, dest string) error {
	srcStat := strings.TrimSuffix(src, "/")
	if _, err := os.Stat(srcStat); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if s.SkipExec {
		if strings.Contains(dest, ":") {
			return nil
		}
		return copyRecursive(src, dest)
	}
	key := filepath.Join(s.Secrets, "migrate-key")
	kh := filepath.Join(s.Secrets, "migrate-known_hosts")
	_, port := s.peerSSH()
	ssh := fmt.Sprintf("/usr/bin/ssh -i %s -p %d -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=%s -o GlobalKnownHostsFile=/dev/null -o BatchMode=yes",
		key, port, kh)
	run := s.Runner
	if run == nil {
		run = exec.Run
	}
	r, err := run(ctx, 30*time.Minute, "/usr/bin/rsync", "-a", "--delete", "--mkpath", "-e", ssh, src, dest)
	if err != nil {
		return fmt.Errorf("%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	return nil
}

func copyRecursive(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return panelFS.WriteAtomic(dst, b, st.Mode())
	}
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return panelFS.Mkdir(target, 0755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return panelFS.WriteAtomic(target, b, info.Mode())
	})
}

type ApplyIn struct {
	Token    string `json:"token"`
	Bundle   Bundle `json:"bundle"`
	DumpPath string `json:"dump_path"`
}

type ApplyOut struct {
	Missing []string `json:"missing"`
}

func (s *Service) PeerApply(ctx context.Context, in ApplyIn) (ApplyOut, error) {
	var out ApplyOut
	if err := s.validPeerToken(in.Token); err != nil {
		return out, err
	}
	missing, err := s.applyBundle(ctx, in.Bundle)
	if err != nil {
		return out, err
	}
	out.Missing = missing
	if err := s.importLocalDump(ctx); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Service) importLocalDump(ctx context.Context) error {
	path := s.dumpFile()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	if s.SkipExec {
		_ = os.Remove(path)
		return nil
	}
	if s.MySQL == nil || !s.MySQL.Installed() {
		return fmt.Errorf("目标机未安装 MySQL，无法导入")
	}
	if err := s.MySQL.ImportFrom(ctx, f); err != nil {
		return err
	}
	_ = f.Close()
	_ = os.Remove(path)
	return nil
}

func (s *Service) Cancel(ctx context.Context) error {
	s.mu.Lock()
	if s.runCancel != nil {
		s.runCancel()
		s.runCancel = nil
	}
	s.mu.Unlock()
	_ = os.Remove(filepath.Join(s.Secrets, "migrate-key"))
	_ = os.Remove(filepath.Join(s.Secrets, "migrate-key.pub"))
	_ = os.Remove(filepath.Join(s.Secrets, "migrate-known_hosts"))
	_ = removeAuthorizedKeys(s.AuthKeys)
	addr, token, fp, err := s.activePeer()
	if err == nil && token != "" {
		_ = s.postPeer(ctx, addr, fp, "", "/migrate/peer/cancel", map[string]string{"token": token}, nil)
	}
	_, _ = s.db.SQL.Exec(`UPDATE migrate_jobs SET phase='failed', detail='已取消', finished_at=? WHERE finished_at IS NULL`, s.Now().Format(time.RFC3339))
	if s.Settings != nil {
		_ = s.Settings.Set("migrate_peer_token", "")
		_ = s.Settings.Set("migrate_peer_ssh_user", "")
		_ = s.Settings.Set("migrate_peer_ssh_port", "")
	}
	return nil
}

func (s *Service) PeerCancel(token string) error {
	if err := s.validPeerToken(token); err != nil {
		return err
	}
	_ = removeAuthorizedKeys(s.AuthKeys)
	_, _ = s.db.SQL.Exec(`DELETE FROM pairing_tokens WHERE token_hash=?`, HashToken(token))
	return nil
}

func (s *Service) activePeer() (addr, token, fp string, err error) {
	if s.Settings == nil {
		return "", "", "", fmt.Errorf("尚未配对")
	}
	token, _ = s.Settings.Get("migrate_peer_token")
	if token == "" {
		return "", "", "", fmt.Errorf("尚未配对")
	}
	_ = s.db.SQL.QueryRow(`SELECT address, fingerprint FROM peers WHERE role='migrate_target' ORDER BY id DESC LIMIT 1`).Scan(&addr, &fp)
	if addr == "" {
		return "", "", "", fmt.Errorf("没有目标机记录")
	}
	return addr, token, fp, nil
}

func (s *Service) postPeer(ctx context.Context, address, fpFull, fpPrefix, path string, body any, out any) error {
	hostPort, prefix, err := SplitHostPortFromAddr(address)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(body)
	url := "https://" + hostPort + "/" + strings.Trim(prefix, "/") + "/api/v1" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	do := s.HTTPDo
	if do == nil {
		cli := s.peerHTTP(fpFull, fpPrefix)
		do = cli.Do
	}
	resp, err := do(req)
	if err != nil {
		return fmt.Errorf("请求对端失败：%w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	var envl struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &envl); err != nil {
		return fmt.Errorf("对端响应无效：%s", string(b))
	}
	if !envl.OK {
		msg := "对端拒绝"
		if envl.Error != nil {
			msg = envl.Error.Message
		}
		return fmt.Errorf("%s", msg)
	}
	if out != nil && len(envl.Data) > 0 {
		return json.Unmarshal(envl.Data, out)
	}
	return nil
}

func (s *Service) peerHTTP(fpFull, fpPrefix string) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig(fpFull, fpPrefix),
		},
	}
}

func tlsConfig(fpFull, fpPrefix string) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return fmt.Errorf("无证书")
			}
			c, err := x509.ParseCertificate(raw[0])
			if err != nil {
				return err
			}
			fp := Fingerprint(c)
			if fpFull != "" && fp != fpFull {
				return fmt.Errorf("TLS 指纹与配对记录不符")
			}
			if fpPrefix != "" && !strings.HasPrefix(fp, fpPrefix) {
				return fmt.Errorf("TLS 指纹与令牌不符")
			}
			return nil
		},
	}
}
