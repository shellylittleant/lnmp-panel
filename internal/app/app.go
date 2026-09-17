package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"lnmp-panel/internal/audit"
	"lnmp-panel/internal/auth"
	"lnmp-panel/internal/cron"
	"lnmp-panel/internal/env"
	"lnmp-panel/internal/files"
	"lnmp-panel/internal/firewall"
	"lnmp-panel/internal/ha"
	"lnmp-panel/internal/httpapi"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/logview"
	"lnmp-panel/internal/migrate"
	"lnmp-panel/internal/monitor"
	"lnmp-panel/internal/mysql"
	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/php"
	"lnmp-panel/internal/plugin"
	"lnmp-panel/internal/scheduler"
	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/site"
	"lnmp-panel/internal/ssl"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

const Version = "0.1.0-stage9"

func CLI(args []string) error {
	if len(args) == 0 {
		return runCmd([]string{})
	}
	switch args[0] {
	case "run":
		return runCmd(args[1:])
	case "bootstrap":
		return bootstrapCmd(args[1:])
	case "whitelist":
		return whitelistCmd(args[1:])
	case "version":
		fmt.Println(Version)
		return nil
	default:
		if args[0] == "-h" || args[0] == "--help" {
			fmt.Print(`lnmp-panel 命令：
  bootstrap  初始化目录、证书、管理员与配置
  run        启动 HTTPS 面板
  whitelist  show|set|clear  查看/设置/清空运维白名单
  version    打印版本
`)
			return nil
		}
		return runCmd(args)
	}
}

func stripHiddenFlag(args []string, name string) ([]string, bool) {
	forms := []string{"--" + name, "-" + name}
	out := make([]string, 0, len(args))
	on := false
	for _, a := range args {
		hit := false
		for _, f := range forms {
			if a == f || a == f+"=true" {
				on = true
				hit = true
				break
			}
			if a == f+"=false" {
				hit = true
				break
			}
		}
		if !hit {
			out = append(out, a)
		}
	}
	return out, on
}

func runCmd(args []string) error {
	args, skipOS := stripHiddenFlag(args, "skip-os-check")
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "/etc/lnmp-panel/panel.json", "引导配置路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return Run(*cfgPath, skipOS)
}

func Run(cfgPath string, skipOS bool) error {
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("读取配置：%w", err)
	}
	var osInfo env.OSInfo
	if !skipOS {
		osInfo, err = env.MustCurrentOS()
		if err != nil {
			return err
		}
		if err := env.RefuseIfDocker(); err != nil {
			return err
		}
	} else {
		osInfo = env.OSInfo{ID: "ubuntu", VersionID: "24.04", Pretty: "测试环境"}
	}
	db, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	authSvc := auth.New(db)
	auditSvc := audit.New(db)
	setSvc := settings.New(db)
	setSvc.SetConfigPath(cfgPath)
	jobSvc := jobs.New(db)
	tpl := template.New(db)
	if err := tpl.SyncOfficial(); err != nil {
		return fmt.Errorf("同步官方模板：%w", err)
	}
	ngx := nginx.New()
	sites := site.New(db, tpl, ngx)
	sslSvc := ssl.New(db, sites)
	if skipOS {
		sslSvc.Issuer = ssl.SelfSigner{}
	} else {
		sslSvc.Issuer = ssl.ACMEIssuer{
			Account: filepath.Join(cfg.DataDir, "secrets", "acme.key"),
		}
	}
	inst := &env.NginxInstaller{Jobs: jobSvc, Nginx: ngx}
	phpSvc := php.New(db, tpl, jobSvc)
	mysqlSvc := mysql.New(db, tpl, jobSvc)
	stack := &env.StackInstaller{Jobs: jobSvc, MySQL: mysqlSvc, Codename: env.AptCodename(osInfo)}
	fw := firewall.New(db, tpl, setSvc)
	if _, port, err := net.SplitHostPort(cfg.Listen); err == nil {
		if n, err := strconv.Atoi(port); err == nil {
			fw.PanelPort = n
		}
	}
	if skipOS {
		fw.SkipNFT = true
	}
	authSvc.OnLock = func(ip string) { _ = fw.Ban(ip) }
	fw.OnRecover = func(detail string) {
		_ = auditSvc.Append("127.0.0.1", "system", "POST /firewall/rollback", "firewall", detail, "")
	}
	if err := fw.SetupOnce(); err != nil {
		log.Printf("防火墙一次性初始化：%v", err)
	}
	if err := fw.RecoverPending(context.Background()); err != nil {
		log.Printf("启动时回滚未确认防火墙规则失败：%v", err)
	}
	logs := logview.New(db, tpl)
	logs.Sites = func() []logview.SiteRef {
		items, err := sites.List()
		if err != nil {
			return nil
		}
		out := make([]logview.SiteRef, 0, len(items))
		for _, st := range items {
			out = append(out, logview.SiteRef{ID: st.ID, Key: st.SiteKey, Name: st.Name})
		}
		return out
	}
	if skipOS {
		logs.SkipExec = true
	}
	mon := monitor.New(db)
	mon.Extra = func() map[string]string {
		return map[string]string{
			"nginx": nginxStatusLine(ngx),
			"php":   phpSvc.StatusLine(),
			"mysql": mysqlSvc.StatusLine(context.Background()),
		}
	}
	cronSvc := cron.New(db)
	if skipOS {
		cronSvc.SkipExec = true
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sch := scheduler.New()
	sch.Register("ssl.renew", 24*time.Hour, sslSvc.RenewDue)
	sch.Register("log.aggregate", time.Hour, logs.AggregateHour)
	sch.Register("metrics.sample", 15*time.Second, func(ctx context.Context) error { return mon.Sample() })
	sch.Register("metrics.rollup", time.Hour, func(ctx context.Context) error {
		if err := mon.Rollup(); err != nil {
			return err
		}
		rawDays := 7
		hourDays := 90
		if v, err := setSvc.Get("metrics_raw_days"); err == nil {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				rawDays = n
			}
		}
		if v, err := setSvc.Get("metrics_hourly_days"); err == nil {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				hourDays = n
			}
		}
		if err := mon.Purge(rawDays); err != nil {
			return err
		}
		return mon.PurgeHourly(hourDays)
	})
	sch.Register("session.purge", time.Hour, func(ctx context.Context) error { return authSvc.PurgeSessions() })
	sch.Register("login_attempts.purge", 24*time.Hour, func(ctx context.Context) error { return authSvc.PurgeAttempts() })
	go sch.Run(ctx)

	mig := migrate.New(db)
	mig.Sites, mig.Nginx, mig.PHP, mig.MySQL = sites, ngx, phpSvc, mysqlSvc
	mig.SSL, mig.Tpl, mig.Firewall, mig.Settings = sslSvc, tpl, fw, setSvc
	mig.OS, mig.TLSCert, mig.DataDir = osInfo, cfg.TLSCert, cfg.DataDir
	mig.WWW, mig.Secrets = sites.WWW, filepath.Join(cfg.DataDir, "secrets")
	if skipOS {
		mig.SkipExec = true
	}
	sch.Register("migrate.expire", 24*time.Hour, func(ctx context.Context) error { return mig.ExpireStale() })

	haSvc := ha.New(db)
	haSvc.Migrate, haSvc.Sites, haSvc.Nginx, haSvc.MySQL = mig, sites, ngx, mysqlSvc
	haSvc.Firewall, haSvc.Settings, haSvc.SSL, haSvc.Tpl = fw, setSvc, sslSvc, tpl
	haSvc.TLSCert, haSvc.DataDir, haSvc.WWW = cfg.TLSCert, cfg.DataDir, sites.WWW
	haSvc.Secrets, haSvc.AuthKeys = filepath.Join(cfg.DataDir, "secrets"), "/root/.ssh/authorized_keys"
	if skipOS {
		haSvc.SkipExec = true
		haSvc.DropInRoot = filepath.Join(cfg.DataDir, "systemd")
	}
	sslSvc.AllowIssue = haSvc.AllowCertIssue
	if err := haSvc.SelfProtect(context.Background()); err != nil {
		log.Printf("自我保护：%v", err)
	}

	plugHost := &plugin.Host{
		WWWRoot:    sites.WWW,
		Data:       cfg.DataDir,
		Secrets:    filepath.Join(cfg.DataDir, "secrets"),
		GetSetting: func(k string) (string, error) { return setSvc.Get(k) },
		PutSetting: func(k, v string) error { return setSvc.Set(k, v) },
		GetConfig: func(id string) (string, error) {
			var c string
			err := db.SQL.QueryRow(`SELECT config FROM plugins WHERE id=?`, id).Scan(&c)
			return c, err
		},
		PutConfig: func(id, js string) error {
			_, err := db.SQL.Exec(`UPDATE plugins SET config=? WHERE id=?`, js, id)
			return err
		},
	}
	plugReg := plugin.New(db, plugHost)
	if err := plugReg.Sync(); err != nil {
		log.Printf("插件同步：%v", err)
	}

	fileSvc := files.New()
	fileSvc.DataDir = cfg.DataDir
	fileSvc.Secrets = filepath.Join(cfg.DataDir, "secrets")
	fileSvc.EtcDir = filepath.Dir(cfgPath)
	if fileSvc.EtcDir == "" || fileSvc.EtcDir == "." {
		fileSvc.EtcDir = "/etc/lnmp-panel"
	}
	fileSvc.WWW = sites.WWW

	srv := &httpapi.Server{
		Prefix:    cfg.PathPrefix,
		Auth:      authSvc,
		Audit:     auditSvc,
		Settings:  setSvc,
		Jobs:      jobSvc,
		Started:   time.Now(),
		Listen:    cfg.Listen,
		OS:        osInfo,
		Tpl:       tpl,
		Sites:     sites,
		Nginx:     ngx,
		SSL:       sslSvc,
		Installer: inst,
		Stack:     stack,
		PHP:       phpSvc,
		MySQL:     mysqlSvc,
		Firewall:  fw,
		Logs:      logs,
		Monitor:   mon,
		Cron:      cronSvc,
		Scheduler: sch,
		Migrate:   mig,
		HA:        haSvc,
		Plugins:   plugReg,
		Files:     fileSvc,
	}
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()
	https := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       30 * time.Minute,
		WriteTimeout:      30 * time.Minute,
		IdleTimeout:       60 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}
	exitCh := make(chan struct{}, 1)
	srv.Restart = func() {
		select {
		case exitCh <- struct{}{}:
		default:
		}
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- https.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case <-sig:
		shutdownHTTP(https, baseCancel)
		return nil
	case <-exitCh:
		shutdownHTTP(https, baseCancel)
		os.Exit(0)
		return nil
	}
}

func shutdownHTTP(https *http.Server, cancel context.CancelFunc) {
	if cancel != nil {
		cancel()
	}
	ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_ = https.Shutdown(ctx)
	_ = https.Close()
}

type BootstrapResult struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	Prefix   string `json:"path_prefix"`
	Listen   string `json:"listen"`
	Config   string `json:"config"`
}

func bootstrapCmd(args []string) error {
	args, skipOS := stripHiddenFlag(args, "skip-os-check")
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "/var/lib/lnmp-panel", "数据目录")
	etcDir := fs.String("etc-dir", "/etc/lnmp-panel", "配置目录")
	listen := fs.String("listen", "", "监听地址，空则随机高位端口")
	prefix := fs.String("prefix", "", "路径前缀，空则随机")
	password := fs.String("password", "", "管理员密码，空则随机")
	bindMode := fs.String("bind-mode", "public", "public 或 localhost")
	ops := fs.String("ops-cidr", "", "运维 CIDR，逗号分隔")
	if err := fs.Parse(args); err != nil {
		return err
	}
	res, err := Bootstrap(BootstrapOpts{
		DataDir:  *dataDir,
		EtcDir:   *etcDir,
		Listen:   *listen,
		Prefix:   *prefix,
		Password: *password,
		BindMode: *bindMode,
		SkipOS:   skipOS,
		OpsCIDR:  *ops,
	})
	if err != nil {
		return err
	}
	fmt.Printf("面板地址：%s\n用户名：%s\n初始密码：%s\n请立即登录并修改密码，建议开启 TOTP。\n", res.URL, res.Username, res.Password)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
	return nil
}

type BootstrapOpts struct {
	DataDir, EtcDir, Listen, Prefix, Password, BindMode, OpsCIDR string
	SkipOS                                                       bool
}

func Bootstrap(o BootstrapOpts) (BootstrapResult, error) {
	var zero BootstrapResult
	if !o.SkipOS {
		if _, err := env.MustCurrentOS(); err != nil {
			return zero, err
		}
		if err := env.RefuseIfDocker(); err != nil {
			return zero, err
		}
	}
	if o.BindMode == "" {
		o.BindMode = "public"
	}
	if o.Prefix == "" {
		p, err := RandomPrefix()
		if err != nil {
			return zero, err
		}
		o.Prefix = p
	}
	if o.Listen == "" {
		port, err := RandomPort()
		if err != nil {
			return zero, err
		}
		host := "0.0.0.0"
		if o.BindMode == "localhost" {
			host = "127.0.0.1"
		}
		o.Listen = net.JoinHostPort(host, fmt.Sprintf("%d", port))
	}
	pass := o.Password
	if pass == "" {
		p, err := auth.RandomPassword()
		if err != nil {
			return zero, err
		}
		pass = p
	}
	if err := auth.CheckPolicy(pass); err != nil {
		return zero, err
	}
	cfg := DefaultConfig(o.DataDir)
	cfg.Listen = o.Listen
	cfg.PathPrefix = o.Prefix
	cfg.BindMode = o.BindMode
	if err := os.MkdirAll(o.DataDir, 0700); err != nil {
		return zero, err
	}
	if err := os.MkdirAll(filepath.Join(o.DataDir, "secrets"), 0700); err != nil {
		return zero, err
	}
	if err := os.MkdirAll(o.EtcDir, 0700); err != nil {
		return zero, err
	}
	if err := EnsureTLS(cfg.TLSCert, cfg.TLSKey); err != nil {
		return zero, err
	}
	cfgPath := filepath.Join(o.EtcDir, "panel.json")
	if err := SaveConfig(cfgPath, cfg); err != nil {
		return zero, err
	}
	db, err := store.Open(cfg.DB)
	if err != nil {
		return zero, err
	}
	defer db.Close()
	if err := auth.New(db).EnsureAdmin("admin", pass); err != nil {
		return zero, err
	}
	st := settings.New(db)
	_, port, _ := net.SplitHostPort(cfg.Listen)
	_ = st.Set("listen_port", port)
	_ = st.Set("path_prefix", cfg.PathPrefix)
	_ = st.Set("bind_mode", cfg.BindMode)
	_ = st.Set("metrics_raw_days", "7")
	_ = st.Set("metrics_hourly_days", "90")
	_ = st.Set("log_keep_days", "14")
	if o.OpsCIDR != "" {
		var arr []string
		for _, p := range splitComma(o.OpsCIDR) {
			arr = append(arr, p)
		}
		b, _ := json.Marshal(arr)
		_ = st.Set("ops_ip_cidrs", string(b))
	} else {
		_ = st.Set("ops_ip_cidrs", "[]")
	}
	if err := template.New(db).SyncOfficial(); err != nil {
		return zero, err
	}

	urlHost := "127.0.0.1"
	if h, p, err := net.SplitHostPort(cfg.Listen); err == nil {
		if h != "0.0.0.0" && h != "::" {
			urlHost = h
		}
		urlHost = net.JoinHostPort(urlHost, p)
		if h == "0.0.0.0" || h == "::" {
			urlHost = net.JoinHostPort(firstNonLoopback(), p)
		}
	}
	res := BootstrapResult{
		URL:      fmt.Sprintf("https://%s/%s/", urlHost, cfg.PathPrefix),
		Username: "admin",
		Password: pass,
		Prefix:   cfg.PathPrefix,
		Listen:   cfg.Listen,
		Config:   cfgPath,
	}
	info := fmt.Sprintf("面板地址：%s\n用户名：%s\n初始密码：%s\n请立即修改密码并开启 TOTP。\n", res.URL, res.Username, res.Password)
	_ = os.WriteFile(filepath.Join(o.EtcDir, "install.info"), []byte(info), 0600)
	return res, nil
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			p := s[start:i]
			// trim
			for len(p) > 0 && (p[0] == ' ' || p[0] == '\t') {
				p = p[1:]
			}
			for len(p) > 0 && (p[len(p)-1] == ' ' || p[len(p)-1] == '\t') {
				p = p[:len(p)-1]
			}
			if p != "" {
				out = append(out, p)
			}
			start = i + 1
		}
	}
	return out
}

func nginxStatusLine(ngx *nginx.Service) string {
	if ngx == nil || !ngx.Installed() {
		return "未安装"
	}
	return "已安装"
}

func firstNonLoopback() string {
	ifs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, a := range ifs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() {
			continue
		}
		if v4 := ipn.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return "127.0.0.1"
}
