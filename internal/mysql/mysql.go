package mysql

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lnmp-panel/internal/exec"
	panelFS "lnmp-panel/internal/fs"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

var (
	ErrMariaDB      = errors.New("检测到 MariaDB，本面板只管理 MySQL 官方源安装的实例")
	ErrNotInstalled = errors.New("尚未安装 MySQL")
	ErrIdent        = errors.New("名称只允许字母、数字和下划线")
	ErrDrift        = errors.New("磁盘配置与面板上次写入不一致，请确认后带 force=true 保存")
	ErrRawMode      = errors.New("处于原文模式，请先恢复为模板渲染")
	ErrDup          = errors.New("已存在")
	ErrNotFound     = errors.New("不存在")
	ErrOfficial     = errors.New("未配置 MySQL 官方 apt 源，请先执行环境安装")
	ErrSystemDB     = errors.New("系统库不可删除")
	ErrUnmanaged    = errors.New("该库非面板创建，请手动处理")
	ErrReservedUser = errors.New("不能创建系统保留账号")
)

var systemDBs = map[string]bool{
	"mysql": true, "sys": true, "information_schema": true, "performance_schema": true,
}

var reservedUsers = map[string]bool{
	"root": true, "mysql.sys": true, "mysql.session": true, "mysql.infoschema": true,
	"lnmp_panel": true, "debian-sys-maint": true,
}

var identRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
var hostRe = regexp.MustCompile(`^(%|localhost|[A-Za-z0-9._:-]+)$`)
var cnfValRe = regexp.MustCompile(`^[A-Za-z0-9._+/-]+$`)

func ValidIdent(name string) error {
	if !identRe.MatchString(name) {
		return ErrIdent
	}
	return nil
}

func ValidHost(h string) error {
	if h == "" {
		return fmt.Errorf("host 不能为空")
	}
	if !hostRe.MatchString(h) || strings.ContainsAny(h, ";'\" \n\r") {
		return fmt.Errorf("无效 host")
	}
	return nil
}

type Runner func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error)
type IORunner func(ctx context.Context, d time.Duration, stdin io.Reader, argv ...string) (exec.Result, error)

type Service struct {
	db         *store.DB
	tpl        *template.Service
	Jobs       *jobs.Service
	Runner     Runner
	IORunner   IORunner
	CNFPath    string
	Defaults   string // --defaults-extra-file
	Socket     string
	BackupRoot string
	TmpDir     string
	ErrorLog   string
	SkipExec   bool
	mu         sync.Mutex
}

func New(db *store.DB, tpl *template.Service, j *jobs.Service) *Service {
	return &Service{
		db:         db,
		tpl:        tpl,
		Jobs:       j,
		Runner:     exec.Run,
		CNFPath:    "/etc/mysql/mysql.conf.d/lnmp-panel.cnf",
		Defaults:   "/var/lib/lnmp-panel/secrets/mysql-panel.cnf",
		Socket:     "/var/run/mysqld/mysqld.sock",
		BackupRoot: "/var/lib/lnmp-panel/backups/mysql",
		TmpDir:     "/var/lib/lnmp-panel/tmp",
		ErrorLog:   "/var/log/mysql/error.log",
	}
}

func (s *Service) run() Runner {
	if s.Runner != nil {
		return s.Runner
	}
	return exec.Run
}

func OfficialPresent() bool {
	b, err := os.ReadFile("/etc/apt/sources.list.d/mysql-official.list")
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "repo.mysql.com")
}

func (s *Service) Installed() bool {
	_, err := os.Stat("/usr/sbin/mysqld")
	if err == nil {
		return true
	}
	_, err = os.Stat("/usr/bin/mysqld")
	return err == nil
}

func (s *Service) mysqldBin() string {
	return "/usr/sbin/mysqld"
}

func (s *Service) MariaDB(ctx context.Context) bool {
	if _, err := os.Stat("/usr/bin/mariadb"); err == nil {
		return true
	}
	if !s.SkipExec {
		r, err := s.run()(ctx, 5*time.Second, "/usr/bin/dpkg-query", "-W", "-f=${Status}", "mariadb-server")
		if err == nil && strings.Contains(r.Stdout, "installed") && !strings.Contains(r.Stdout, "not-installed") {
			return true
		}
		if s.Installed() {
			r, err = s.run()(ctx, 5*time.Second, s.mysqldBin(), "--version")
			out := r.Stdout + r.Stderr
			if err == nil && strings.Contains(out, "MariaDB") {
				return true
			}
		}
	}
	return false
}

func (s *Service) Version(ctx context.Context) string {
	if s.SkipExec || !s.Installed() {
		return ""
	}
	r, err := s.run()(ctx, 5*time.Second, "/usr/bin/mysql", "--version")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(r.Stdout)
}

func (s *Service) StatusLine(ctx context.Context) string {
	if s.MariaDB(ctx) {
		return "检测到 MariaDB（拒绝管理）"
	}
	if !s.Installed() {
		if OfficialPresent() {
			return "官方源已就绪，未安装"
		}
		return "未安装"
	}
	v := s.Version(ctx)
	if v == "" {
		return "已安装"
	}
	return "已安装 " + v
}

func (s *Service) Refuse(ctx context.Context) error {
	if s.MariaDB(ctx) {
		return ErrMariaDB
	}
	return nil
}

type CNFVars struct {
	MaxConnections   string `json:"max_connections"`
	InnoDBBufferPool string `json:"innodb_buffer_pool_size"`
	SlowQueryLog     string `json:"slow_query_log"`
	LongQueryTime    string `json:"long_query_time"`
}

func defaultCNF() CNFVars {
	return CNFVars{
		MaxConnections:   "151",
		InnoDBBufferPool: "128M",
		SlowQueryLog:     "ON",
		LongQueryTime:    "2",
	}
}

func (v CNFVars) fill() CNFVars {
	d := defaultCNF()
	if v.MaxConnections == "" {
		v.MaxConnections = d.MaxConnections
	}
	if v.InnoDBBufferPool == "" {
		v.InnoDBBufferPool = d.InnoDBBufferPool
	}
	if v.SlowQueryLog == "" {
		v.SlowQueryLog = d.SlowQueryLog
	}
	if v.LongQueryTime == "" {
		v.LongQueryTime = d.LongQueryTime
	}
	return v
}

func (v CNFVars) validate() error {
	for _, f := range []string{v.MaxConnections, v.InnoDBBufferPool, v.SlowQueryLog, v.LongQueryTime} {
		if f == "" || !cnfValRe.MatchString(f) {
			return fmt.Errorf("my.cnf 字段含非法字符")
		}
	}
	return nil
}

type FileView struct {
	Form    CNFVars `json:"form"`
	Content string  `json:"content"`
	Drifted bool    `json:"drifted"`
	RawMode bool    `json:"raw_mode"`
}

func (s *Service) setting(key, def string) string {
	var v string
	err := s.db.SQL.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err != nil {
		return def
	}
	return v
}

func (s *Service) setSetting(key, val string) {
	now := s.db.Now()
	_, _ = s.db.SQL.Exec(`INSERT INTO settings(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, val)
	_ = now
}

func (s *Service) cnfState() (bool, bool) {
	hash := s.setting("mysql_cnf_hash", "")
	raw := s.setting("mysql_cnf_raw_mode", "0") == "1"
	h, err := panelFS.HashFile(s.CNFPath)
	if err != nil {
		h = ""
	}
	return hash != "" && h != hash, raw
}

func (s *Service) GetCNF() (FileView, error) {
	body := ""
	if b, err := os.ReadFile(s.CNFPath); err == nil {
		body = string(b)
	}
	form := parseCNF(body)
	d, r := s.cnfState()
	return FileView{Form: form, Content: body, Drifted: d, RawMode: r}, nil
}

func parseCNF(body string) CNFVars {
	v := defaultCNF()
	kv := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		kv[strings.TrimSpace(k)] = strings.TrimSpace(val)
	}
	if x := kv["max_connections"]; x != "" {
		v.MaxConnections = x
	}
	if x := kv["innodb_buffer_pool_size"]; x != "" {
		v.InnoDBBufferPool = x
	}
	if x := kv["slow_query_log"]; x != "" {
		v.SlowQueryLog = x
	}
	if x := kv["long_query_time"]; x != "" {
		v.LongQueryTime = x
	}
	return v
}

func (s *Service) renderCNF(v CNFVars) (string, error) {
	rec, err := s.tpl.OfficialByName("mysql_cnf", "面板默认片段")
	if err != nil {
		return "", fmt.Errorf("缺少官方 mysql_cnf 模板")
	}
	return template.Render(rec.Name, rec.Content, v)
}

func (s *Service) PutCNF(ctx context.Context, in CNFVars, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, raw := s.cnfState()
	if raw {
		return ErrRawMode
	}
	if d && !force {
		return ErrDrift
	}
	in = in.fill()
	if err := in.validate(); err != nil {
		return err
	}
	body, err := s.renderCNF(in)
	if err != nil {
		return err
	}
	return s.writeCNF(ctx, body, false)
}

func (s *Service) PutCNFRaw(ctx context.Context, content string, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, _ := s.cnfState()
	if d && !force {
		return ErrDrift
	}
	if _, err := s.tpl.EnsureUser("mysql_cnf", content, nil); err != nil {
		return err
	}
	return s.writeCNF(ctx, content, true)
}

func (s *Service) UseCNFTemplate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, err := s.renderCNF(defaultCNF())
	if err != nil {
		return err
	}
	return s.writeCNF(ctx, body, false)
}

func (s *Service) writeCNF(ctx context.Context, body string, raw bool) error {
	if err := panelFS.Mkdir(filepath.Dir(s.CNFPath), 0755); err != nil {
		return err
	}
	bak, _ := panelFS.Backup(s.CNFPath, s.BackupRoot)
	if err := panelFS.WriteAtomic(s.CNFPath, []byte(body), 0644); err != nil {
		return err
	}
	if err := s.Restart(ctx); err != nil {
		if bak != "" {
			b, rerr := os.ReadFile(bak)
			if rerr == nil {
				_ = panelFS.WriteAtomic(s.CNFPath, b, 0644)
				_ = s.Restart(ctx)
			}
		}
		return err
	}
	s.setSetting("mysql_cnf_hash", panelFS.HashBytes([]byte(body)))
	if raw {
		s.setSetting("mysql_cnf_raw_mode", "1")
	} else {
		s.setSetting("mysql_cnf_raw_mode", "0")
	}
	return nil
}

func (s *Service) Restart(ctx context.Context) error {
	if s.SkipExec {
		return nil
	}
	if !s.Installed() {
		return ErrNotInstalled
	}
	run := s.run()
	if exec.SystemdLive() {
		r, err := run(ctx, 60*time.Second, "/usr/bin/systemctl", "restart", "mysql")
		if err != nil {
			r2, err2 := run(ctx, 60*time.Second, "/usr/bin/systemctl", "start", "mysql")
			if err2 != nil {
				return fmt.Errorf("systemctl restart/start mysql 失败：%s %s", exec.Clip(r.Stderr), exec.Clip(r2.Stderr))
			}
		}
		return s.waitReady(ctx)
	}
	_ = s.admin(ctx, "shutdown")
	if err := s.startDaemon(ctx); err != nil {
		return err
	}
	return s.waitReady(ctx)
}

func (s *Service) startDaemon(ctx context.Context) error {
	_ = panelFS.Mkdir("/var/run/mysqld", 0755)
	if u, err := user.Lookup("mysql"); err == nil {
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		_ = os.Chown("/var/run/mysqld", uid, gid)
	}
	datadir := "/var/lib/mysql"
	if _, err := os.Stat(filepath.Join(datadir, "mysql")); err != nil {
		r, err := s.run()(ctx, 2*time.Minute, s.mysqldBin(), "--initialize-insecure", "--user=mysql")
		if err != nil {
			return s.wrapStartErr("mysqld --initialize-insecure 失败", r, err)
		}
	}
	r, err := s.run()(ctx, 30*time.Second, s.mysqldBin(), "--user=mysql", "--daemonize")
	if err != nil {
		return s.wrapStartErr("启动 mysqld 失败", r, err)
	}
	return nil
}

func (s *Service) wrapStartErr(prefix string, r exec.Result, err error) error {
	msg := strings.TrimSpace(r.Stderr + " " + r.Stdout)
	if err != nil && msg == "" {
		msg = err.Error()
	}
	tail := tailLines(s.ErrorLog, 20)
	if tail != "" {
		if msg != "" {
			msg += "；"
		}
		msg += "error.log：" + exec.Clip(tail)
	}
	if msg == "" {
		msg = "无输出"
	}
	return fmt.Errorf("%s：%s", prefix, msg)
}

func tailLines(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	return out
}

func (s *Service) waitReady(ctx context.Context) error {
	if s.SkipExec {
		return nil
	}
	var last error
	for i := 0; i < 40; i++ {
		if err := s.admin(ctx, "ping"); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("mysql 未就绪")
	}
	return last
}

func (s *Service) admin(ctx context.Context, extra ...string) error {
	args := s.clientArgs("/usr/bin/mysqladmin")
	args = append(args, extra...)
	r, err := s.run()(ctx, 15*time.Second, args...)
	if err != nil {
		return fmt.Errorf("mysqladmin：%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	return nil
}

func (s *Service) clientArgs(bin string) []string {
	for _, p := range []string{s.Defaults, filepath.Join(filepath.Dir(s.Defaults), "mysql-root.cnf")} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return []string{bin, "--defaults-extra-file=" + p}
		}
	}
	return []string{bin, "-uroot"}
}

func IsReservedUser(name string) bool {
	return reservedUsers[strings.ToLower(name)]
}

// DumpManagedArgs 只导面板纳管库，不含 --all-databases。
func DumpManagedArgs(defaults string, names []string) []string {
	args := []string{"/usr/bin/mysqldump"}
	if defaults != "" {
		args = append(args, "--defaults-extra-file="+defaults)
	} else {
		args = append(args, "-uroot")
	}
	args = append(args, "--single-transaction", "--routines", "--events", "--databases")
	args = append(args, names...)
	return args
}

func (s *Service) DumpDatabasesTo(ctx context.Context, names []string, w io.Writer) error {
	if len(names) == 0 {
		return nil
	}
	for _, n := range names {
		if err := ValidIdent(n); err != nil {
			return err
		}
	}
	if err := s.Refuse(ctx); err != nil {
		return err
	}
	if s.SkipExec {
		_, _ = io.WriteString(w, "-- skip-managed\n")
		return nil
	}
	def := ""
	if s.Defaults != "" {
		if _, err := os.Stat(s.Defaults); err == nil {
			def = s.Defaults
		}
	}
	args := DumpManagedArgs(def, names)
	r, err := exec.RunTo(ctx, 30*time.Minute, nil, w, args...)
	if err != nil {
		return fmt.Errorf("mysqldump：%s", exec.Clip(r.Stderr))
	}
	return nil
}

var pluginRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
var authHexRe = regexp.MustCompile(`^[0-9A-Fa-f]+$`)

func FormatCreateUserSQL(user, host, plugin, hexHash string) (string, error) {
	if err := ValidIdent(user); err != nil {
		return "", err
	}
	if err := ValidHost(host); err != nil {
		return "", err
	}
	if !pluginRe.MatchString(plugin) {
		return "", fmt.Errorf("无效认证插件")
	}
	if !authHexRe.MatchString(hexHash) {
		return "", fmt.Errorf("无效 authentication_string")
	}
	return fmt.Sprintf("CREATE USER IF NOT EXISTS %s@%s IDENTIFIED WITH %s AS 0x%s;",
		quoteSQL(user), quoteSQL(host), quoteSQL(plugin), hexHash), nil
}

func parsePluginHex(raw string) (plugin, hexHash string, err error) {
	line := strings.TrimSpace(raw)
	if i := strings.Index(line, "\n"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	plugin, hexHash, ok := strings.Cut(line, "\t")
	plugin = strings.TrimSpace(plugin)
	hexHash = strings.TrimSpace(hexHash)
	if !ok || plugin == "" || hexHash == "" {
		return "", "", fmt.Errorf("mysql.user 未返回 plugin 与哈希")
	}
	return plugin, hexHash, nil
}

func (s *Service) UserDumpSQL(ctx context.Context, user, host string) (string, error) {
	if IsReservedUser(user) {
		return "", nil
	}
	if err := ValidIdent(user); err != nil {
		return "", err
	}
	if err := ValidHost(host); err != nil {
		return "", err
	}
	raw, err := s.Exec(ctx, fmt.Sprintf(
		"SELECT plugin, HEX(authentication_string) FROM mysql.user WHERE user=%s AND host=%s",
		quoteSQL(user), quoteSQL(host)))
	if err != nil {
		return "", err
	}
	plugin, hexHash, err := parsePluginHex(raw)
	if err != nil {
		return "", fmt.Errorf("读取 %s@%s 认证信息：%w", user, host, err)
	}
	create, err := FormatCreateUserSQL(user, host, plugin, hexHash)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(create)
	b.WriteByte('\n')
	grants, err := s.Exec(ctx, fmt.Sprintf("SHOW GRANTS FOR %s@%s", quoteSQL(user), quoteSQL(host)))
	if err != nil {
		return "", err
	}
	for _, ln := range strings.Split(grants, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if i := strings.Index(ln, "GRANT "); i >= 0 {
			ln = ln[i:]
		}
		if !strings.HasSuffix(ln, ";") {
			ln += ";"
		}
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func (s *Service) importArgs() []string {
	return append(s.clientArgs("/usr/bin/mysql"), "--batch")
}

func (s *Service) ImportFrom(ctx context.Context, r io.Reader) error {
	return s.importWith(ctx, r, 2*time.Minute, s.importArgs())
}

// ImportAsRoot 用 unix socket 的 root（auth_socket）导入，避免全库 dump 改写 mysql.user 后面板账号对不上。
func (s *Service) ImportAsRoot(ctx context.Context, r io.Reader) error {
	return s.importWith(ctx, r, 15*time.Minute, []string{"/usr/bin/mysql", "-uroot", "--batch"})
}

func (s *Service) importWith(ctx context.Context, r io.Reader, d time.Duration, args []string) error {
	if s.SkipExec {
		return nil
	}
	if err := s.Refuse(ctx); err != nil {
		return err
	}
	res, err := exec.RunTo(ctx, d, r, io.Discard, args...)
	if err != nil {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("mysql 导入失败：%s", exec.Clip(msg))
	}
	return nil
}

func quoteSQL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `''`)
	return "'" + s + "'"
}

func (s *Service) runIO() IORunner {
	if s.IORunner != nil {
		return s.IORunner
	}
	return exec.RunIO
}

func (s *Service) Exec(ctx context.Context, sqlText string) (string, error) {
	args := append(s.clientArgs("/usr/bin/mysql"), "-N")
	if s.SkipExec && s.IORunner == nil {
		return "", nil
	}
	r, err := s.runIO()(ctx, 60*time.Second, strings.NewReader(sqlText), args...)
	if s.SkipExec {
		return r.Stdout, nil
	}
	if err != nil {
		return "", fmt.Errorf("mysql：%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	return r.Stdout, nil
}

func (s *Service) EnsureRoot(ctx context.Context) error {
	return s.EnsurePanelUser(ctx)
}

// EnsurePanelUser 创建 lnmp_panel@localhost，不改 root 认证。
func (s *Service) EnsurePanelUser(ctx context.Context) error {
	if s.SkipExec {
		return nil
	}
	if err := s.Refuse(ctx); err != nil {
		return err
	}
	if !s.Installed() {
		return ErrNotInstalled
	}
	if err := s.ensureRunning(ctx); err != nil {
		return err
	}
	if _, err := os.Stat(s.Defaults); err == nil {
		if err := s.admin(ctx, "ping"); err == nil {
			return nil
		}
	}
	pass := randomPass()
	stmt := "CREATE USER IF NOT EXISTS 'lnmp_panel'@'localhost' IDENTIFIED BY " + quoteSQL(pass) +
		"; ALTER USER 'lnmp_panel'@'localhost' IDENTIFIED BY " + quoteSQL(pass) +
		"; GRANT ALL ON *.* TO 'lnmp_panel'@'localhost' WITH GRANT OPTION; FLUSH PRIVILEGES;"
	r, err := s.runIO()(ctx, 20*time.Second, strings.NewReader(stmt), "/usr/bin/mysql", "-uroot")
	if err != nil {
		return fmt.Errorf("创建面板账号 lnmp_panel 失败：%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	if err := panelFS.Mkdir(filepath.Dir(s.Defaults), 0700); err != nil {
		return err
	}
	body := fmt.Sprintf("[client]\nuser=lnmp_panel\npassword=%q\nsocket=%s\n", pass, s.Socket)
	if err := panelFS.WriteAtomic(s.Defaults, []byte(body), 0600); err != nil {
		return err
	}
	if err := s.admin(ctx, "ping"); err != nil {
		return fmt.Errorf("面板账号无法连接：%w", err)
	}
	return nil
}

func (s *Service) EnsureRunning(ctx context.Context) error {
	return s.ensureRunning(ctx)
}

func (s *Service) ensureRunning(ctx context.Context) error {
	if s.admin(ctx, "ping") == nil {
		return nil
	}
	if exec.SystemdLive() {
		if err := exec.ReloadOrStart(ctx, s.run(), "mysql", []string{s.mysqldBin(), "--user=mysql", "--daemonize"}); err != nil {
			r, err2 := s.run()(ctx, 30*time.Second, "/usr/bin/systemctl", "start", "mysql")
			if err2 != nil {
				return s.wrapStartErr("启动 mysql 失败", r, err2)
			}
		}
		return s.waitReady(ctx)
	}
	if err := s.startDaemon(ctx); err != nil {
		return err
	}
	return s.waitReady(ctx)
}

func randomPass() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b) + "Aa1!"
}

func (s *Service) WriteDefaultCNF(ctx context.Context) error {
	if _, err := os.Stat(s.CNFPath); err == nil {
		return nil
	}
	body, err := s.renderCNF(defaultCNF())
	if err != nil {
		return err
	}
	if err := panelFS.Mkdir(filepath.Dir(s.CNFPath), 0755); err != nil {
		return err
	}
	if err := panelFS.WriteAtomic(s.CNFPath, []byte(body), 0644); err != nil {
		return err
	}
	s.setSetting("mysql_cnf_hash", panelFS.HashBytes([]byte(body)))
	s.setSetting("mysql_cnf_raw_mode", "0")
	return nil
}

type Database struct {
	Name      string `json:"name"`
	Charset   string `json:"charset"`
	Collate   string `json:"collate"`
	CreatedAt string `json:"created_at"`
}

type User struct {
	ID        int64    `json:"id"`
	User      string   `json:"user"`
	Host      string   `json:"host"`
	Databases []string `json:"databases"`
	CreatedAt string   `json:"created_at"`
}

func (s *Service) ListDB() ([]Database, error) {
	rows, err := s.db.SQL.Query(`SELECT name, charset, "collate", created_at FROM mysql_databases ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Database
	for rows.Next() {
		var d Database
		if err := rows.Scan(&d.Name, &d.Charset, &d.Collate, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if out == nil {
		out = []Database{}
	}
	return out, rows.Err()
}

func (s *Service) CreateDB(ctx context.Context, name, charset, collate string) (Database, error) {
	if err := ValidIdent(name); err != nil {
		return Database{}, err
	}
	if charset == "" {
		charset = "utf8mb4"
	}
	if collate == "" {
		collate = "utf8mb4_0900_ai_ci"
	}
	if err := ValidIdent(charset); err != nil {
		return Database{}, fmt.Errorf("无效字符集")
	}
	if err := ValidIdent(collate); err != nil {
		return Database{}, fmt.Errorf("无效校对规则")
	}
	if err := s.Refuse(ctx); err != nil {
		return Database{}, err
	}
	var n int
	_ = s.db.SQL.QueryRow(`SELECT COUNT(1) FROM mysql_databases WHERE name=?`, name).Scan(&n)
	if n > 0 {
		return Database{}, ErrDup
	}
	sqlText := fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET %s COLLATE %s", name, charset, collate)
	if _, err := s.Exec(ctx, sqlText); err != nil {
		return Database{}, err
	}
	now := s.db.Now()
	_, err := s.db.SQL.Exec(`INSERT INTO mysql_databases(name, charset, "collate", created_at) VALUES (?, ?, ?, ?)`, name, charset, collate, now)
	if err != nil {
		_, _ = s.Exec(ctx, fmt.Sprintf("DROP DATABASE `%s`", name))
		return Database{}, err
	}
	return Database{Name: name, Charset: charset, Collate: collate, CreatedAt: now}, nil
}

func (s *Service) DropDB(ctx context.Context, name string) error {
	if err := ValidIdent(name); err != nil {
		return err
	}
	if systemDBs[strings.ToLower(name)] {
		return ErrSystemDB
	}
	if err := s.Refuse(ctx); err != nil {
		return err
	}
	var n int
	_ = s.db.SQL.QueryRow(`SELECT COUNT(1) FROM mysql_databases WHERE name=?`, name).Scan(&n)
	if n == 0 {
		return ErrUnmanaged
	}
	if _, err := s.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name)); err != nil {
		return err
	}
	_, _ = s.db.SQL.Exec(`DELETE FROM mysql_grants WHERE db_name=?`, name)
	_, err := s.db.SQL.Exec(`DELETE FROM mysql_databases WHERE name=?`, name)
	return err
}

func (s *Service) ListUsers() ([]User, error) {
	rows, err := s.db.SQL.Query(`SELECT id, user, host, created_at FROM mysql_users ORDER BY user, host`)
	if err != nil {
		return nil, err
	}
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.User, &u.Host, &u.CreatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, u)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Databases = s.userDBs(out[i].ID)
	}
	if out == nil {
		out = []User{}
	}
	return out, nil
}

func (s *Service) userDBs(id int64) []string {
	rows, err := s.db.SQL.Query(`SELECT db_name FROM mysql_grants WHERE user_id=?`, id)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out = append(out, n)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func (s *Service) CreateUser(ctx context.Context, uname, host, password string, dbs []string) (User, error) {
	if reservedUsers[strings.ToLower(uname)] {
		return User{}, ErrReservedUser
	}
	if err := ValidIdent(uname); err != nil {
		return User{}, err
	}
	if host == "" {
		host = "localhost"
	}
	if err := ValidHost(host); err != nil {
		return User{}, err
	}
	if len(password) < 8 {
		return User{}, fmt.Errorf("密码至少 8 位")
	}
	if err := s.Refuse(ctx); err != nil {
		return User{}, err
	}
	var n int
	_ = s.db.SQL.QueryRow(`SELECT COUNT(1) FROM mysql_users WHERE user=? AND host=?`, uname, host).Scan(&n)
	if n > 0 {
		return User{}, ErrDup
	}
	stmt := fmt.Sprintf("CREATE USER %s@%s IDENTIFIED BY %s", quoteSQL(uname), quoteSQL(host), quoteSQL(password))
	if _, err := s.Exec(ctx, stmt); err != nil {
		return User{}, err
	}
	now := s.db.Now()
	res, err := s.db.SQL.Exec(`INSERT INTO mysql_users(user, host, created_at) VALUES (?, ?, ?)`, uname, host, now)
	if err != nil {
		_, _ = s.Exec(ctx, fmt.Sprintf("DROP USER %s@%s", quoteSQL(uname), quoteSQL(host)))
		return User{}, err
	}
	id, _ := res.LastInsertId()
	for _, dbn := range dbs {
		if dbn == "" {
			continue
		}
		if err := s.grant(ctx, id, uname, host, dbn); err != nil {
			return User{}, err
		}
	}
	return User{ID: id, User: uname, Host: host, Databases: s.userDBs(id), CreatedAt: now}, nil
}

func (s *Service) grant(ctx context.Context, userID int64, uname, host, dbn string) error {
	if err := ValidIdent(dbn); err != nil {
		return err
	}
	var exists int
	_ = s.db.SQL.QueryRow(`SELECT COUNT(1) FROM mysql_databases WHERE name=?`, dbn).Scan(&exists)
	if exists == 0 {
		return fmt.Errorf("数据库不存在：%s", dbn)
	}
	stmt := fmt.Sprintf("GRANT ALL ON `%s`.* TO %s@%s", dbn, quoteSQL(uname), quoteSQL(host))
	if _, err := s.Exec(ctx, stmt); err != nil {
		return err
	}
	_, _ = s.Exec(ctx, "FLUSH PRIVILEGES")
	_, err := s.db.SQL.Exec(`INSERT INTO mysql_grants(user_id, db_name, priv) VALUES (?, ?, 'ALL')`, userID, dbn)
	return err
}

func (s *Service) SetPassword(ctx context.Context, id int64, password string) error {
	if len(password) < 8 {
		return fmt.Errorf("密码至少 8 位")
	}
	if err := s.Refuse(ctx); err != nil {
		return err
	}
	var uname, host string
	err := s.db.SQL.QueryRow(`SELECT user, host FROM mysql_users WHERE id=?`, id).Scan(&uname, &host)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	stmt := fmt.Sprintf("ALTER USER %s@%s IDENTIFIED BY %s", quoteSQL(uname), quoteSQL(host), quoteSQL(password))
	if _, err := s.Exec(ctx, stmt); err != nil {
		return err
	}
	_, _ = s.Exec(ctx, "FLUSH PRIVILEGES")
	return nil
}

func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	if err := s.Refuse(ctx); err != nil {
		return err
	}
	var uname, host string
	err := s.db.SQL.QueryRow(`SELECT user, host FROM mysql_users WHERE id=?`, id).Scan(&uname, &host)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := s.Exec(ctx, fmt.Sprintf("DROP USER IF EXISTS %s@%s", quoteSQL(uname), quoteSQL(host))); err != nil {
		return err
	}
	_, _ = s.db.SQL.Exec(`DELETE FROM mysql_grants WHERE user_id=?`, id)
	_, err = s.db.SQL.Exec(`DELETE FROM mysql_users WHERE id=?`, id)
	return err
}

func (s *Service) ExportTo(ctx context.Context, name string, w io.Writer) error {
	if err := ValidIdent(name); err != nil {
		return err
	}
	if err := s.Refuse(ctx); err != nil {
		return err
	}
	if s.SkipExec {
		_, _ = io.WriteString(w, "-- skip\n")
		return nil
	}
	args := append(s.clientArgs("/usr/bin/mysqldump"), "--single-transaction", "--routines", "--default-character-set=utf8mb4", name)
	r, err := exec.RunTo(ctx, 5*time.Minute, nil, w, args...)
	if err != nil {
		return fmt.Errorf("mysqldump 失败：%s", exec.Clip(r.Stderr))
	}
	return nil
}

func (s *Service) StartImport(ctx context.Context, name string, sqlBytes []byte) (string, error) {
	if err := ValidIdent(name); err != nil {
		return "", err
	}
	if err := s.Refuse(ctx); err != nil {
		return "", err
	}
	if err := panelFS.Mkdir(s.TmpDir, 0700); err != nil {
		return "", err
	}
	id, err := s.Jobs.Start("mysql-import")
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.TmpDir, "import-"+id+".sql")
	if err := panelFS.WriteAtomic(path, sqlBytes, 0600); err != nil {
		s.Jobs.Finish(id, err)
		return "", err
	}
	go s.runImport(context.Background(), id, name, path)
	return id, nil
}

func (s *Service) runImport(ctx context.Context, id, name, path string) {
	defer os.Remove(path)
	s.Jobs.Update(id, "import", "导入 "+name, 30)
	if s.SkipExec {
		s.Jobs.Finish(id, nil)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.Jobs.Finish(id, err)
		return
	}
	defer f.Close()
	args := append(s.clientArgs("/usr/bin/mysql"), "--database="+name)
	r, err := exec.RunIO(ctx, 15*time.Minute, f, args...)
	if s.Runner != nil && !s.SkipExec {
		// 测试可替换 Runner，但 stdin 走 RunIO；生产用白名单 mysql
		_ = r
	}
	if err != nil {
		s.Jobs.Finish(id, fmt.Errorf("导入失败：%s", exec.Clip(r.Stderr+" "+r.Stdout)))
		return
	}
	s.Jobs.Finish(id, nil)
}

func (s *Service) ImportSync(ctx context.Context, name string, sqlBytes []byte) error {
	if err := ValidIdent(name); err != nil {
		return err
	}
	if s.SkipExec {
		return nil
	}
	args := append(s.clientArgs("/usr/bin/mysql"), "--database="+name)
	r, err := exec.RunIO(ctx, 15*time.Minute, bytes.NewReader(sqlBytes), args...)
	if err != nil {
		return fmt.Errorf("导入失败：%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	return nil
}
