package php

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"lnmp-panel/internal/exec"
	panelFS "lnmp-panel/internal/fs"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

var (
	ErrNotFound = errors.New("该 PHP 版本未安装")
	ErrDrift    = errors.New("磁盘配置与面板上次写入不一致，请确认后带 force=true 保存")
	ErrRawMode  = errors.New("处于原文模式，请先恢复为模板渲染")
	ErrVersion  = errors.New("只支持 PHP 8.2 及以上，格式如 8.3")
	ErrPPA      = errors.New("尚未添加 Ondřej PHP 源，请先执行环境安装")
	ErrExt      = errors.New("扩展名不合法")
	ErrForbidden   = errors.New("不能通过扩展接口装卸 fpm/cli/common")
	ErrUnavailable = errors.New("该 PHP 版本当前不可安装")
)

var verRe = regexp.MustCompile(`^8\.([2-9]|[1-9][0-9]+)$`)
var extRe = regexp.MustCompile(`^[a-z0-9+]+$`)
var iniValRe = regexp.MustCompile(`^[A-Za-z0-9._+/-]+$`)

var extPkg = map[string]string{
	"mysql": "mysql", "mysqli": "mysql", "pdo_mysql": "mysql",
	"gd": "gd", "curl": "curl", "mbstring": "mbstring", "xml": "xml",
	"zip": "zip", "bcmath": "bcmath", "intl": "intl", "redis": "redis",
	"imagick": "imagick", "opcache": "opcache", "soap": "soap",
	"sqlite3": "sqlite3", "pgsql": "pgsql", "ldap": "ldap", "imap": "imap",
	"gmp": "gmp", "readline": "readline", "tidy": "tidy", "xsl": "xsl",
	"bz2": "bz2", "enchant": "enchant", "dba": "dba", "odbc": "odbc",
	"snmp": "snmp", "pspell": "pspell", "sybase": "sybase",
	"apcu": "apcu", "memcached": "memcached", "yaml": "yaml",
	"uuid": "uuid", "igbinary": "igbinary", "msgpack": "msgpack",
	"xdebug": "xdebug", "pcov": "pcov", "mailparse": "mailparse",
	"maxminddb": "maxminddb", "zstd": "zstd", "ds": "ds",
	"decimal": "decimal", "ssh2": "ssh2", "grpc": "grpc",
	"protobuf": "protobuf", "mcrypt": "mcrypt",
}

var extForbidden = map[string]bool{
	"fpm": true, "cli": true, "common": true, "dev": true, "phpdbg": true,
}

func ValidVersion(v string) error {
	if !verRe.MatchString(v) {
		return ErrVersion
	}
	return nil
}

func PPAPresent() bool {
	matches, _ := filepath.Glob("/etc/apt/sources.list.d/*ondrej*php*")
	return len(matches) > 0
}

type Runner func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error)

type Service struct {
	db         *store.DB
	tpl        *template.Service
	Jobs       *jobs.Service
	Runner     Runner
	BaseDir    string
	RunDir     string
	BinDir     string
	BackupRoot string
	SkipExec   bool
	PPACheck   func() bool
	verMu      sync.Map
}

func New(db *store.DB, tpl *template.Service, j *jobs.Service) *Service {
	return &Service{
		db:         db,
		tpl:        tpl,
		Jobs:       j,
		Runner:     exec.Run,
		BaseDir:    "/etc/php",
		RunDir:     "/run/php",
		BackupRoot: "/var/lib/lnmp-panel/backups/php",
		PPACheck:   PPAPresent,
	}
}

func (s *Service) run() Runner {
	if s.Runner != nil {
		return s.Runner
	}
	return exec.Run
}

func (s *Service) lock(ver string) func() {
	v, _ := s.verMu.LoadOrStore(ver, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

func (s *Service) bin(ver string) string {
	dir := s.BinDir
	if dir == "" {
		dir = "/usr/sbin"
	}
	return filepath.Join(dir, "php-fpm"+ver)
}

func (s *Service) Socket(ver string) string {
	return filepath.Join(s.RunDir, "php"+ver+"-fpm.sock")
}

func (s *Service) iniPath(ver string) string {
	return filepath.Join(s.BaseDir, ver, "fpm", "conf.d", "99-lnmp-panel.ini")
}

func (s *Service) poolPath(ver string) string {
	return filepath.Join(s.BaseDir, ver, "fpm", "pool.d", "lnmp-panel.conf")
}

func (s *Service) wwwPool(ver string) string {
	return filepath.Join(s.BaseDir, ver, "fpm", "pool.d", "www.conf")
}

func (s *Service) BinaryInstalled(ver string) bool {
	_, err := os.Stat(s.bin(ver))
	return err == nil
}

type Version struct {
	Version     string   `json:"version"`
	Installed   bool     `json:"installed"`
	Available   bool     `json:"available"`
	Extensions  []string `json:"extensions"`
	RawINIMode  bool     `json:"raw_ini_mode"`
	RawPoolMode bool     `json:"raw_pool_mode"`
}

func (s *Service) List(ctx context.Context) ([]Version, error) {
	avail := map[string]bool{}
	if !s.SkipExec {
		r, err := s.run()(ctx, 20*time.Second, "/usr/bin/apt-cache", "search", "--names-only", "^php8\\.[0-9]+-fpm$")
		if err == nil {
			re := regexp.MustCompile(`php(8\.[0-9]+)-fpm`)
			for _, line := range strings.Split(r.Stdout, "\n") {
				if m := re.FindStringSubmatch(line); len(m) == 2 {
					if ValidVersion(m[1]) == nil {
						avail[m[1]] = true
					}
				}
			}
		}
	}
	ppa := true
	if s.PPACheck != nil {
		ppa = s.PPACheck()
	}
	if ppa && len(avail) == 0 {
		for _, v := range []string{"8.2", "8.3", "8.4", "8.5"} {
			avail[v] = true
		}
	}
	if !ppa {
		avail = map[string]bool{}
	}
	seen := map[string]bool{}
	var vers []string
	for v := range avail {
		if !seen[v] {
			seen[v] = true
			vers = append(vers, v)
		}
	}
	if ents, err := os.ReadDir(s.BaseDir); err == nil {
		for _, e := range ents {
			if e.IsDir() && ValidVersion(e.Name()) == nil && !seen[e.Name()] {
				seen[e.Name()] = true
				vers = append(vers, e.Name())
			}
		}
	}
	rows, err := s.db.SQL.Query(`SELECT version FROM php_versions`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err == nil && ValidVersion(v) == nil && !seen[v] {
				seen[v] = true
				vers = append(vers, v)
			}
		}
	}
	sortVersions(vers)
	out := make([]Version, 0, len(vers))
	for _, v := range vers {
		item, err := s.snapshot(v, avail[v])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func sortVersions(vs []string) {
	for i := 0; i < len(vs); i++ {
		for j := i + 1; j < len(vs); j++ {
			if versionLess(vs[j], vs[i]) {
				vs[i], vs[j] = vs[j], vs[i]
			}
		}
	}
}

func versionLess(a, b string) bool {
	ai, aj := splitVer(a)
	bi, bj := splitVer(b)
	if ai != bi {
		return ai < bi
	}
	return aj < bj
}

func splitVer(v string) (int, int) {
	p := strings.SplitN(v, ".", 2)
	if len(p) != 2 {
		return 0, 0
	}
	x, _ := strconv.Atoi(p[0])
	y, _ := strconv.Atoi(p[1])
	return x, y
}

func (s *Service) snapshot(ver string, available bool) (Version, error) {
	item := Version{Version: ver, Available: available, Extensions: []string{}}
	item.Installed = s.BinaryInstalled(ver)
	ext, _ := s.listEnabledExt(ver)
	item.Extensions = ext
	var rawI, rawP int
	err := s.db.SQL.QueryRow(`SELECT raw_ini_mode, raw_pool_mode FROM php_versions WHERE version=?`, ver).Scan(&rawI, &rawP)
	if err == nil {
		item.RawINIMode = rawI == 1
		item.RawPoolMode = rawP == 1
	}
	if item.Installed {
		_ = s.ensureRow(ver, true, ext)
	}
	return item, nil
}

func (s *Service) ensureRow(ver string, installed bool, ext []string) error {
	if ext == nil {
		ext = []string{}
	}
	b, _ := json.Marshal(ext)
	ins := 0
	if installed {
		ins = 1
	}
	var n int
	_ = s.db.SQL.QueryRow(`SELECT COUNT(1) FROM php_versions WHERE version=?`, ver).Scan(&n)
	if n == 0 {
		if ext == nil {
			ext = []string{}
			b, _ = json.Marshal(ext)
		}
		iniID, poolID := s.officialIDs()
		_, err := s.db.SQL.Exec(`INSERT INTO php_versions(version, installed, extensions, ini_template_id, pool_template_id)
			VALUES (?, ?, ?, ?, ?)`, ver, ins, string(b), iniID, poolID)
		return err
	}
	if ext == nil {
		_, err := s.db.SQL.Exec(`UPDATE php_versions SET installed=? WHERE version=?`, ins, ver)
		return err
	}
	_, err := s.db.SQL.Exec(`UPDATE php_versions SET installed=?, extensions=? WHERE version=?`, ins, string(b), ver)
	return err
}

func (s *Service) officialIDs() (*int64, *int64) {
	var ini, pool int64
	var ip, pp *int64
	if rec, err := s.tpl.OfficialByName("php_ini", "生产默认"); err == nil {
		ini = rec.ID
		ip = &ini
	}
	if rec, err := s.tpl.OfficialByName("php_fpm_pool", "版本默认 pool"); err == nil {
		pool = rec.ID
		pp = &pool
	}
	return ip, pp
}

func (s *Service) listEnabledExt(ver string) ([]string, error) {
	confd := filepath.Join(s.BaseDir, ver, "fpm", "conf.d")
	ents, err := os.ReadDir(confd)
	if err != nil {
		return []string{}, nil
	}
	seen := map[string]bool{}
	var out []string
	re := regexp.MustCompile(`^[0-9]+-(.+)\.ini$`)
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if m := re.FindStringSubmatch(name); len(m) == 2 {
			n := m[1]
			if !seen[n] && n != "lnmp-panel" {
				seen[n] = true
				out = append(out, n)
			}
			continue
		}
		if strings.HasSuffix(name, ".ini") && !strings.Contains(name, "lnmp-panel") {
			n := strings.TrimSuffix(name, ".ini")
			n = strings.TrimLeft(n, "0123456789-")
			if n != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func (s *Service) StartInstall(ctx context.Context, ver string) (string, error) {
	if err := ValidVersion(ver); err != nil {
		return "", err
	}
	if s.PPACheck != nil && !s.PPACheck() {
		return "", ErrPPA
	}
	items, err := s.List(ctx)
	if err != nil {
		return "", err
	}
	ok := false
	for _, it := range items {
		if it.Version == ver && (it.Available || it.Installed) {
			ok = true
			break
		}
	}
	if !ok {
		return "", ErrUnavailable
	}
	id, err := s.Jobs.Start("php-install-" + ver)
	if err != nil {
		return "", err
	}
	go s.runInstall(context.Background(), id, ver)
	return id, nil
}

func (s *Service) runInstall(ctx context.Context, id, ver string) {
	s.Jobs.Update(id, "apt-install", "安装 php"+ver+"-fpm/cli", 20)
	if !s.SkipExec {
		r, err := s.run()(ctx, 15*time.Minute, "/usr/bin/apt-get", "install", "-y",
			"-o", "Dpkg::Options::=--force-confdef",
			"-o", "Dpkg::Options::=--force-confold",
			"php"+ver+"-fpm", "php"+ver+"-cli")
		if err != nil && !s.BinaryInstalled(ver) {
			s.Jobs.Finish(id, fmt.Errorf("安装 PHP %s 失败：%s", ver, exec.Clip(r.Stderr+" "+r.Stdout)))
			return
		}
	}
	s.Jobs.Update(id, "configure", "写入 pool 与 php.ini 片段", 70)
	if err := s.afterInstall(ctx, ver); err != nil {
		s.Jobs.Finish(id, err)
		return
	}
	s.Jobs.Finish(id, nil)
}

func (s *Service) afterInstall(ctx context.Context, ver string) error {
	unlock := s.lock(ver)
	defer unlock()
	_ = os.Rename(s.wwwPool(ver), s.wwwPool(ver)+".disabled")
	_ = panelFS.Mkdir(s.RunDir, 0755)
	if err := panelFS.Mkdir(filepath.Dir(s.poolPath(ver)), 0755); err != nil {
		return err
	}
	if err := panelFS.Mkdir(filepath.Dir(s.iniPath(ver)), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(s.poolPath(ver)); err != nil {
		body, err := s.renderPool(ver, defaultPool(ver, s.Socket(ver)))
		if err != nil {
			return err
		}
		if err := panelFS.WriteAtomic(s.poolPath(ver), []byte(body), 0644); err != nil {
			return err
		}
	}
	if _, err := os.Stat(s.iniPath(ver)); err != nil {
		body, err := s.renderINI(prodINI())
		if err != nil {
			return err
		}
		if err := panelFS.WriteAtomic(s.iniPath(ver), []byte(body), 0644); err != nil {
			return err
		}
	}
	ext, _ := s.listEnabledExt(ver)
	if err := s.ensureRow(ver, true, ext); err != nil {
		return err
	}
	_ = s.storeHashes(ver)
	return s.TestAndReload(ctx, ver)
}

func (s *Service) storeHashes(ver string) error {
	ih, _ := panelFS.HashFile(s.iniPath(ver))
	ph, _ := panelFS.HashFile(s.poolPath(ver))
	_, err := s.db.SQL.Exec(`UPDATE php_versions SET last_written_ini_hash=?, last_written_pool_hash=? WHERE version=?`, ih, ph, ver)
	return err
}

func (s *Service) pkgForExt(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if extForbidden[name] {
		return "", ErrForbidden
	}
	if pkg, ok := extPkg[name]; ok {
		return pkg, nil
	}
	if !extRe.MatchString(name) {
		return "", ErrExt
	}
	return name, nil
}

func (s *Service) InstallExt(ctx context.Context, ver, name string) error {
	if err := ValidVersion(ver); err != nil {
		return err
	}
	if !s.BinaryInstalled(ver) {
		return ErrNotFound
	}
	pkg, err := s.pkgForExt(name)
	if err != nil {
		return err
	}
	if !s.SkipExec {
		r, err := s.run()(ctx, 10*time.Minute, "/usr/bin/apt-get", "install", "-y",
			"-o", "Dpkg::Options::=--force-confdef",
			"-o", "Dpkg::Options::=--force-confold",
			"php"+ver+"-"+pkg)
		if err != nil {
			return fmt.Errorf("安装扩展失败：%s", exec.Clip(r.Stderr+" "+r.Stdout))
		}
	}
	ext, _ := s.listEnabledExt(ver)
	_ = s.ensureRow(ver, true, ext)
	return s.TestAndReload(ctx, ver)
}

func (s *Service) RemoveExt(ctx context.Context, ver, name string) error {
	if err := ValidVersion(ver); err != nil {
		return err
	}
	if !s.BinaryInstalled(ver) {
		return ErrNotFound
	}
	pkg, err := s.pkgForExt(name)
	if err != nil {
		return err
	}
	if !s.SkipExec {
		r, err := s.run()(ctx, 10*time.Minute, "/usr/bin/apt-get", "remove", "-y", "php"+ver+"-"+pkg)
		if err != nil {
			return fmt.Errorf("卸载扩展失败：%s", exec.Clip(r.Stderr+" "+r.Stdout))
		}
	}
	ext, _ := s.listEnabledExt(ver)
	_ = s.ensureRow(ver, true, ext)
	return s.TestAndReload(ctx, ver)
}

type INIVars struct {
	UploadMaxFilesize string `json:"upload_max_filesize"`
	PostMaxSize       string `json:"post_max_size"`
	MemoryLimit       string `json:"memory_limit"`
	MaxExecutionTime  string `json:"max_execution_time"`
	DisplayErrors     string `json:"display_errors"`
	OpcacheEnable     string `json:"opcache.enable"`
	OpcacheMemory     string `json:"opcache.memory_consumption"`
	OpcacheMaxFiles   string `json:"opcache.max_accelerated_files"`
	OpcacheValidate   string `json:"opcache.validate_timestamps"`
}

func prodINI() INIVars {
	return INIVars{
		UploadMaxFilesize: "32M",
		PostMaxSize:       "32M",
		MemoryLimit:       "256M",
		MaxExecutionTime:  "60",
		DisplayErrors:     "Off",
		OpcacheEnable:     "1",
		OpcacheMemory:     "128",
		OpcacheMaxFiles:   "10000",
		OpcacheValidate:   "0",
	}
}

func (v INIVars) validate() error {
	fields := []string{v.UploadMaxFilesize, v.PostMaxSize, v.MemoryLimit, v.MaxExecutionTime,
		v.DisplayErrors, v.OpcacheEnable, v.OpcacheMemory, v.OpcacheMaxFiles, v.OpcacheValidate}
	for _, f := range fields {
		if f == "" || !iniValRe.MatchString(f) {
			return fmt.Errorf("php.ini 字段含非法字符")
		}
	}
	return nil
}

func (v INIVars) fill() INIVars {
	d := prodINI()
	if v.UploadMaxFilesize == "" {
		v.UploadMaxFilesize = d.UploadMaxFilesize
	}
	if v.PostMaxSize == "" {
		v.PostMaxSize = d.PostMaxSize
	}
	if v.MemoryLimit == "" {
		v.MemoryLimit = d.MemoryLimit
	}
	if v.MaxExecutionTime == "" {
		v.MaxExecutionTime = d.MaxExecutionTime
	}
	if v.DisplayErrors == "" {
		v.DisplayErrors = d.DisplayErrors
	}
	if v.OpcacheEnable == "" {
		v.OpcacheEnable = d.OpcacheEnable
	}
	if v.OpcacheMemory == "" {
		v.OpcacheMemory = d.OpcacheMemory
	}
	if v.OpcacheMaxFiles == "" {
		v.OpcacheMaxFiles = d.OpcacheMaxFiles
	}
	if v.OpcacheValidate == "" {
		v.OpcacheValidate = d.OpcacheValidate
	}
	return v
}

type FileView struct {
	Form    any    `json:"form"`
	Content string `json:"content"`
	Drifted bool   `json:"drifted"`
	RawMode bool   `json:"raw_mode"`
}

func (s *Service) GetINI(ver string) (FileView, error) {
	if err := ValidVersion(ver); err != nil {
		return FileView{}, err
	}
	if !s.BinaryInstalled(ver) && !fileExists(s.iniPath(ver)) {
		return FileView{}, ErrNotFound
	}
	body := ""
	if b, err := os.ReadFile(s.iniPath(ver)); err == nil {
		body = string(b)
	}
	form := parseINI(body)
	drifted, raw := s.iniState(ver)
	return FileView{Form: form, Content: body, Drifted: drifted, RawMode: raw}, nil
}

func (s *Service) iniState(ver string) (bool, bool) {
	var hash string
	var raw int
	_ = s.db.SQL.QueryRow(`SELECT last_written_ini_hash, raw_ini_mode FROM php_versions WHERE version=?`, ver).Scan(&hash, &raw)
	h, err := panelFS.HashFile(s.iniPath(ver))
	if err != nil {
		h = ""
	}
	return hash != "" && h != hash, raw == 1
}

func (s *Service) requireINI(ver string, force bool) error {
	if !s.BinaryInstalled(ver) && !fileExists(s.iniPath(ver)) {
		return ErrNotFound
	}
	drifted, raw := s.iniState(ver)
	if raw {
		return ErrRawMode
	}
	if drifted && !force {
		return ErrDrift
	}
	return nil
}

func (s *Service) PutINI(ctx context.Context, ver string, in INIVars, force bool) error {
	if err := ValidVersion(ver); err != nil {
		return err
	}
	unlock := s.lock(ver)
	defer unlock()
	if err := s.requireINI(ver, force); err != nil {
		return err
	}
	in = in.fill()
	if err := in.validate(); err != nil {
		return err
	}
	body, err := s.renderINI(in)
	if err != nil {
		return err
	}
	return s.writeINI(ctx, ver, body, false)
}

func (s *Service) PutINIRaw(ctx context.Context, ver, content string, force bool) error {
	if err := ValidVersion(ver); err != nil {
		return err
	}
	unlock := s.lock(ver)
	defer unlock()
	if !s.BinaryInstalled(ver) && !fileExists(s.iniPath(ver)) {
		return ErrNotFound
	}
	drifted, _ := s.iniState(ver)
	if drifted && !force {
		return ErrDrift
	}
	if _, err := s.tpl.EnsureUser("php_ini", content, nil); err != nil {
		return err
	}
	return s.writeINI(ctx, ver, content, true)
}

func (s *Service) UseINITemplate(ctx context.Context, ver string) error {
	if err := ValidVersion(ver); err != nil {
		return err
	}
	unlock := s.lock(ver)
	defer unlock()
	body, err := s.renderINI(prodINI())
	if err != nil {
		return err
	}
	return s.writeINI(ctx, ver, body, false)
}

func (s *Service) renderINI(v INIVars) (string, error) {
	rec, err := s.tpl.OfficialByName("php_ini", "生产默认")
	if err != nil {
		return "", fmt.Errorf("缺少官方 php_ini 模板")
	}
	return template.Render(rec.Name, rec.Content, v)
}

func (s *Service) writeINI(ctx context.Context, ver, body string, raw bool) error {
	path := s.iniPath(ver)
	if err := panelFS.Mkdir(filepath.Dir(path), 0755); err != nil {
		return err
	}
	bak, _ := panelFS.Backup(path, filepath.Join(s.BackupRoot, ver, "ini"))
	if err := panelFS.WriteAtomic(path, []byte(body), 0644); err != nil {
		return err
	}
	if err := s.TestAndReload(ctx, ver); err != nil {
		if bak != "" {
			b, rerr := os.ReadFile(bak)
			if rerr == nil {
				_ = panelFS.WriteAtomic(path, b, 0644)
			}
		}
		return err
	}
	h := panelFS.HashBytes([]byte(body))
	rawN := 0
	if raw {
		rawN = 1
	}
	_ = s.ensureRow(ver, s.BinaryInstalled(ver), nil)
	_, err := s.db.SQL.Exec(`UPDATE php_versions SET last_written_ini_hash=?, raw_ini_mode=? WHERE version=?`, h, rawN, ver)
	return err
}

type PoolVars struct {
	PoolName     string `json:"pool_name"`
	User         string `json:"user"`
	Group        string `json:"group"`
	Listen       string `json:"listen"`
	PM           string `json:"pm"`
	MaxChildren  string `json:"pm.max_children"`
	StartServers string `json:"pm.start_servers"`
	MinSpare     string `json:"pm.min_spare_servers"`
	MaxSpare     string `json:"pm.max_spare_servers"`
}

func defaultPool(ver, listen string) PoolVars {
	return PoolVars{
		PoolName:     "lnmp-panel",
		User:         "www-data",
		Group:        "www-data",
		Listen:       listen,
		PM:           "dynamic",
		MaxChildren:  "20",
		StartServers: "4",
		MinSpare:     "2",
		MaxSpare:     "8",
	}
}

func (v PoolVars) fill(ver, listen string) PoolVars {
	d := defaultPool(ver, listen)
	if v.PoolName == "" {
		v.PoolName = d.PoolName
	}
	if v.User == "" {
		v.User = d.User
	}
	if v.Group == "" {
		v.Group = d.Group
	}
	if v.Listen == "" {
		v.Listen = d.Listen
	}
	if v.PM == "" {
		v.PM = d.PM
	}
	if v.MaxChildren == "" {
		v.MaxChildren = d.MaxChildren
	}
	if v.StartServers == "" {
		v.StartServers = d.StartServers
	}
	if v.MinSpare == "" {
		v.MinSpare = d.MinSpare
	}
	if v.MaxSpare == "" {
		v.MaxSpare = d.MaxSpare
	}
	return v
}

func (v PoolVars) validate() error {
	for _, f := range []string{v.PoolName, v.User, v.Group, v.PM, v.MaxChildren, v.StartServers, v.MinSpare, v.MaxSpare} {
		if f == "" || !iniValRe.MatchString(f) {
			return fmt.Errorf("pool 字段含非法字符")
		}
	}
	if !strings.HasPrefix(v.Listen, "/") || strings.ContainsAny(v.Listen, ";{} \n\r") {
		return fmt.Errorf("listen 必须是不含空白的绝对路径")
	}
	return nil
}

func (s *Service) GetPool(ver string) (FileView, error) {
	if err := ValidVersion(ver); err != nil {
		return FileView{}, err
	}
	if !s.BinaryInstalled(ver) && !fileExists(s.poolPath(ver)) {
		return FileView{}, ErrNotFound
	}
	body := ""
	if b, err := os.ReadFile(s.poolPath(ver)); err == nil {
		body = string(b)
	}
	form := parsePool(body, s.Socket(ver))
	drifted, raw := s.poolState(ver)
	return FileView{Form: form, Content: body, Drifted: drifted, RawMode: raw}, nil
}

func (s *Service) poolState(ver string) (bool, bool) {
	var hash string
	var raw int
	_ = s.db.SQL.QueryRow(`SELECT last_written_pool_hash, raw_pool_mode FROM php_versions WHERE version=?`, ver).Scan(&hash, &raw)
	h, err := panelFS.HashFile(s.poolPath(ver))
	if err != nil {
		h = ""
	}
	return hash != "" && h != hash, raw == 1
}

func (s *Service) PutPool(ctx context.Context, ver string, in PoolVars, force bool) error {
	if err := ValidVersion(ver); err != nil {
		return err
	}
	unlock := s.lock(ver)
	defer unlock()
	if !s.BinaryInstalled(ver) && !fileExists(s.poolPath(ver)) {
		return ErrNotFound
	}
	drifted, raw := s.poolState(ver)
	if raw {
		return ErrRawMode
	}
	if drifted && !force {
		return ErrDrift
	}
	in = in.fill(ver, s.Socket(ver))
	if err := in.validate(); err != nil {
		return err
	}
	body, err := s.renderPool(ver, in)
	if err != nil {
		return err
	}
	return s.writePool(ctx, ver, body, false)
}

func (s *Service) PutPoolRaw(ctx context.Context, ver, content string, force bool) error {
	if err := ValidVersion(ver); err != nil {
		return err
	}
	unlock := s.lock(ver)
	defer unlock()
	if !s.BinaryInstalled(ver) && !fileExists(s.poolPath(ver)) {
		return ErrNotFound
	}
	drifted, _ := s.poolState(ver)
	if drifted && !force {
		return ErrDrift
	}
	if _, err := s.tpl.EnsureUser("php_fpm_pool", content, nil); err != nil {
		return err
	}
	return s.writePool(ctx, ver, content, true)
}

func (s *Service) UsePoolTemplate(ctx context.Context, ver string) error {
	if err := ValidVersion(ver); err != nil {
		return err
	}
	unlock := s.lock(ver)
	defer unlock()
	body, err := s.renderPool(ver, defaultPool(ver, s.Socket(ver)))
	if err != nil {
		return err
	}
	return s.writePool(ctx, ver, body, false)
}

func (s *Service) renderPool(ver string, v PoolVars) (string, error) {
	rec, err := s.tpl.OfficialByName("php_fpm_pool", "版本默认 pool")
	if err != nil {
		return "", fmt.Errorf("缺少官方 php_fpm_pool 模板")
	}
	return template.Render(rec.Name, rec.Content, v)
}

func (s *Service) writePool(ctx context.Context, ver, body string, raw bool) error {
	path := s.poolPath(ver)
	if err := panelFS.Mkdir(filepath.Dir(path), 0755); err != nil {
		return err
	}
	bak, _ := panelFS.Backup(path, filepath.Join(s.BackupRoot, ver, "pool"))
	if err := panelFS.WriteAtomic(path, []byte(body), 0644); err != nil {
		return err
	}
	if err := s.TestAndReload(ctx, ver); err != nil {
		if bak != "" {
			b, rerr := os.ReadFile(bak)
			if rerr == nil {
				_ = panelFS.WriteAtomic(path, b, 0644)
			}
		}
		return err
	}
	h := panelFS.HashBytes([]byte(body))
	rawN := 0
	if raw {
		rawN = 1
	}
	_ = s.ensureRow(ver, s.BinaryInstalled(ver), nil)
	_, err := s.db.SQL.Exec(`UPDATE php_versions SET last_written_pool_hash=?, raw_pool_mode=? WHERE version=?`, h, rawN, ver)
	return err
}

func (s *Service) TestAndReload(ctx context.Context, ver string) error {
	if s.SkipExec {
		return nil
	}
	if !s.BinaryInstalled(ver) {
		return fmt.Errorf("未安装 php-fpm %s", ver)
	}
	run := s.run()
	r, err := run(ctx, 15*time.Second, s.bin(ver), "-t")
	if err != nil {
		msg := r.Stderr
		if msg == "" {
			msg = r.Stdout
		}
		return fmt.Errorf("php-fpm %s -t 失败：%s", ver, exec.Clip(msg))
	}
	unit := "php" + ver + "-fpm"
	if exec.SystemdLive() {
		return exec.ReloadOrStart(ctx, run, unit, []string{s.bin(ver)})
	}
	pidFile := filepath.Join(s.RunDir, "php"+ver+"-fpm.pid")
	if b, err := os.ReadFile(pidFile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
			if err := syscall.Kill(pid, syscall.SIGUSR2); err == nil {
				return nil
			}
		}
	}
	if _, err := os.Stat(s.Socket(ver)); err == nil {
		return nil
	}
	r2, err2 := run(ctx, 15*time.Second, s.bin(ver))
	if err2 != nil {
		if _, err := os.Stat(s.Socket(ver)); err == nil {
			return nil
		}
		return fmt.Errorf("启动 php-fpm %s 失败：%s", ver, exec.Clip(r2.Stderr+" "+r2.Stdout))
	}
	return nil
}

func (s *Service) StatusLine() string {
	list, err := s.List(context.Background())
	if err != nil {
		return "检测失败"
	}
	var inst []string
	for _, v := range list {
		if v.Installed {
			inst = append(inst, v.Version)
		}
	}
	if len(inst) == 0 {
		if s.PPACheck != nil && s.PPACheck() {
			return "源已就绪，未安装版本"
		}
		return "未安装"
	}
	return "已安装 " + strings.Join(inst, ", ")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func parseINI(body string) INIVars {
	v := prodINI()
	kv := parseKV(body)
	set := func(dst *string, keys ...string) {
		for _, k := range keys {
			if x, ok := kv[k]; ok && x != "" {
				*dst = x
				return
			}
		}
	}
	set(&v.UploadMaxFilesize, "upload_max_filesize")
	set(&v.PostMaxSize, "post_max_size")
	set(&v.MemoryLimit, "memory_limit")
	set(&v.MaxExecutionTime, "max_execution_time")
	set(&v.DisplayErrors, "display_errors")
	set(&v.OpcacheEnable, "opcache.enable")
	set(&v.OpcacheMemory, "opcache.memory_consumption")
	set(&v.OpcacheMaxFiles, "opcache.max_accelerated_files")
	set(&v.OpcacheValidate, "opcache.validate_timestamps")
	return v
}

func parsePool(body, listen string) PoolVars {
	v := defaultPool("", listen)
	kv := parseKV(body)
	if x := kv["user"]; x != "" {
		v.User = x
	}
	if x := kv["group"]; x != "" {
		v.Group = x
	}
	if x := kv["listen"]; x != "" {
		v.Listen = x
	}
	if x := kv["pm"]; x != "" {
		v.PM = x
	}
	if x := kv["pm.max_children"]; x != "" {
		v.MaxChildren = x
	}
	if x := kv["pm.start_servers"]; x != "" {
		v.StartServers = x
	}
	if x := kv["pm.min_spare_servers"]; x != "" {
		v.MinSpare = x
	}
	if x := kv["pm.max_spare_servers"]; x != "" {
		v.MaxSpare = x
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if name != "" {
				v.PoolName = name
			}
			break
		}
	}
	return v
}

func parseKV(body string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(val)
	}
	return out
}
