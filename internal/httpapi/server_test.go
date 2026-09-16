package httpapi

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lnmp-panel/internal/audit"
	"lnmp-panel/internal/auth"
	"lnmp-panel/internal/env"
	"lnmp-panel/internal/firewall"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/mysql"
	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/php"
	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/site"
	"lnmp-panel/internal/ssl"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"

	"github.com/pquerna/otp/totp"
)

type envlope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func startTest(t *testing.T) (base, prefix, password string, client *http.Client, srv *http.Server) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	password = "GoodPassw0rd!"
	if err := auth.New(db).EnsureAdmin("admin", password); err != nil {
		t.Fatal(err)
	}
	st := settings.New(db)
	_ = st.Set("ops_ip_cidrs", "[]")
	_ = st.Set("bind_mode", "public")
	tpl := template.New(db)
	if err := tpl.SyncOfficial(); err != nil {
		t.Fatal(err)
	}
	ngx := nginx.New()
	ngx.Available = filepath.Join(dir, "avail")
	ngx.Enabled = filepath.Join(dir, "en")
	ngx.ConfD = filepath.Join(dir, "conf.d")
	ngx.AcmeWWW = filepath.Join(dir, "acme")
	ngx.BackupRoot = filepath.Join(dir, "bak")
	if err := ngx.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	sites := site.New(db, tpl, ngx)
	sites.WWW = filepath.Join(dir, "www")
	sites.SkipNX = true
	sslSvc := ssl.New(db, sites)
	sslSvc.Dir = filepath.Join(dir, "certs")
	sslSvc.AcmeWWW = ngx.AcmeWWW
	sslSvc.Issuer = ssl.SelfSigner{}
	jobSvc := jobs.New(db)
	phpSvc := php.New(db, tpl, jobSvc)
	phpSvc.SkipExec = true
	phpSvc.BaseDir = filepath.Join(dir, "php")
	phpSvc.RunDir = filepath.Join(dir, "runphp")
	phpSvc.BinDir = filepath.Join(dir, "sbin")
	phpSvc.BackupRoot = filepath.Join(dir, "bakphp")
	phpSvc.PPACheck = func() bool { return true }
	mysqlSvc := mysql.New(db, tpl, jobSvc)
	mysqlSvc.SkipExec = true
	mysqlSvc.CNFPath = filepath.Join(dir, "lnmp-panel.cnf")
	mysqlSvc.Defaults = filepath.Join(dir, "mysql-root.cnf")
	mysqlSvc.BackupRoot = filepath.Join(dir, "bakmysql")
	mysqlSvc.TmpDir = filepath.Join(dir, "tmp")
	stack := &env.StackInstaller{
		Jobs: jobSvc, MySQL: mysqlSvc, SkipExec: true, Codename: "noble",
		KeyPath: filepath.Join(dir, "keyrings", "mysql.asc"),
		ListPath: filepath.Join(dir, "apt", "mysql-official.list"),
		HTTPGet: func(url string) ([]byte, error) {
			return []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\ntest\n-----END PGP PUBLIC KEY BLOCK-----\n"), nil
		},
	}
	fw := firewall.New(db, tpl, st)
	fw.SkipNFT = true
	fw.PanelPort = 18443
	fw.Canonical = filepath.Join(dir, "nftables.conf")
	fw.Draft = filepath.Join(dir, "nft-draft")
	fw.SystemNFT = filepath.Join(dir, "system-nft")
	fw.BackupRoot = filepath.Join(dir, "nftbak")
	prefix = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s := &Server{
		Prefix:    prefix,
		Auth:      auth.New(db),
		Audit:     audit.New(db),
		Settings:  st,
		Jobs:      jobSvc,
		Started:   time.Now(),
		Listen:    "127.0.0.1:0",
		OS:        env.OSInfo{ID: "ubuntu", VersionID: "24.04", Pretty: "Ubuntu 24.04 LTS", Codename: "noble"},
		Tpl:       tpl,
		Sites:     sites,
		Nginx:     ngx,
		SSL:       sslSvc,
		Installer: &env.NginxInstaller{Jobs: jobSvc, Nginx: ngx},
		Stack:     stack,
		PHP:       phpSvc,
		MySQL:     mysqlSvc,
		Firewall:  fw,
	}
	https := &http.Server{Handler: s.Handler()}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: mustCert(t, dir),
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	go https.Serve(ln)
	t.Cleanup(func() { _ = https.Close() })
	jar, _ := cookiejar.New(nil)
	client = &http.Client{
		Jar: jar,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	base = "https://" + ln.Addr().String()
	return base, prefix, password, client, https
}

func mustCert(t *testing.T, dir string) []tls.Certificate {
	t.Helper()
	cert := filepath.Join(dir, "t.crt")
	key := filepath.Join(dir, "t.key")
	if err := genTestCert(cert, key); err != nil {
		t.Fatal(err)
	}
	c, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	return []tls.Certificate{c}
}

func genTestCert(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		return err
	}
	return os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0600)
}

func doJSON(t *testing.T, client *http.Client, method, url, csrf string, body any) (int, envlope) {
	t.Helper()
	return doJSONH(t, client, method, url, csrf, body, nil)
}

func doJSONH(t *testing.T, client *http.Client, method, url, csrf string, body any, extra http.Header) (int, envlope) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for k, vs := range extra {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var e envlope
	if len(b) > 0 && b[0] == '{' {
		_ = json.Unmarshal(b, &e)
	}
	return res.StatusCode, e
}

func loginCSRF(t *testing.T, client *http.Client, api, user, pass string) string {
	t.Helper()
	code, e := doJSON(t, client, "POST", api+"/auth/login", "", map[string]string{"username": user, "password": pass})
	if code != 200 || !e.OK {
		t.Fatalf("登录失败 %d %+v", code, e)
	}
	_, sess := doJSON(t, client, "GET", api+"/auth/session", "", nil)
	var data map[string]any
	_ = json.Unmarshal(sess.Data, &data)
	csrf, _ := data["csrf_token"].(string)
	if csrf == "" {
		t.Fatal("缺少 csrf")
	}
	return csrf
}

func TestRootIs404(t *testing.T) {
	base, prefix, _, client, _ := startTest(t)
	code, _ := doJSON(t, client, "GET", base+"/", "", nil)
	if code != 404 {
		t.Fatalf("根路径应为 404，得到 %d", code)
	}
	code, _ = doJSON(t, client, "GET", base+"/"+prefix+"/", "", nil)
	if code != 200 {
		t.Fatalf("前缀路径应为 200，得到 %d", code)
	}
}

func TestLoginAndAudit(t *testing.T) {
	base, prefix, pass, client, _ := startTest(t)
	api := base + "/" + prefix + "/api/v1"
	code, e := doJSON(t, client, "POST", api+"/auth/login", "", map[string]string{
		"username": "admin", "password": "wrong-password!!",
	})
	if code == 200 && e.OK {
		t.Fatal("错误密码不应成功")
	}
	code, e = doJSON(t, client, "POST", api+"/auth/login", "", map[string]string{
		"username": "admin", "password": pass,
	})
	if code != 200 || !e.OK {
		t.Fatalf("登录失败 %d %+v", code, e)
	}
	code, sess := doJSON(t, client, "GET", api+"/auth/session", "", nil)
	if code != 200 || !sess.OK {
		t.Fatalf("session %d %+v", code, sess)
	}
	var data map[string]any
	_ = json.Unmarshal(sess.Data, &data)
	csrf, _ := data["csrf_token"].(string)
	if csrf == "" {
		t.Fatal("缺少 csrf")
	}
	code, st := doJSON(t, client, "GET", api+"/status", csrf, nil)
	if code != 200 || !st.OK {
		t.Fatalf("status %d", code)
	}
	code, _ = doJSON(t, client, "PUT", api+"/settings", "", map[string]any{"bind_mode": "localhost"})
	if code == 200 {
		t.Fatal("无 CSRF 不应改设置")
	}
	code, au := doJSON(t, client, "GET", api+"/audit", csrf, nil)
	if code != 200 || !au.OK {
		t.Fatalf("audit %d", code)
	}
	code, job := doJSON(t, client, "GET", api+"/jobs/nope", csrf, nil)
	if code != 404 {
		t.Fatalf("未知 job 应为 404，得到 %d %+v", code, job)
	}
}

func TestLockout(t *testing.T) {
	base, prefix, _, client, _ := startTest(t)
	api := base + "/" + prefix + "/api/v1"
	for i := 0; i < 5; i++ {
		doJSON(t, client, "POST", api+"/auth/login", "", map[string]string{
			"username": "admin", "password": "BadPassw0rd!",
		})
	}
	code, e := doJSON(t, client, "POST", api+"/auth/login", "", map[string]string{
		"username": "admin", "password": "BadPassw0rd!",
	})
	if code != 423 && (e.Error == nil || e.Error.Code != "locked") {
		t.Fatalf("应锁定，得到 %d %+v", code, e)
	}
}

func TestTOTPLogin(t *testing.T) {
	base, prefix, pass, client, _ := startTest(t)
	api := base + "/" + prefix + "/api/v1"
	_, e := doJSON(t, client, "POST", api+"/auth/login", "", map[string]string{"username": "admin", "password": pass})
	if !e.OK {
		t.Fatal(e)
	}
	_, sess := doJSON(t, client, "GET", api+"/auth/session", "", nil)
	var data map[string]any
	_ = json.Unmarshal(sess.Data, &data)
	csrf, _ := data["csrf_token"].(string)
	code, setup := doJSON(t, client, "POST", api+"/auth/totp/setup", csrf, map[string]any{})
	if code != 200 || !setup.OK {
		t.Fatalf("setup %d %+v", code, setup)
	}
	var st auth.TOTPSetup
	_ = json.Unmarshal(setup.Data, &st)
	codeNow, err := totp.GenerateCode(st.Secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	code, en := doJSON(t, client, "POST", api+"/auth/totp/enable", csrf, map[string]string{"code": codeNow})
	if code != 200 || !en.OK {
		t.Fatalf("enable %d %+v", code, en)
	}
	// 新客户端重新登录
	jar, _ := cookiejar.New(nil)
	c2 := &http.Client{Jar: jar, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	_, login := doJSON(t, c2, "POST", api+"/auth/login", "", map[string]string{"username": "admin", "password": pass})
	var lr map[string]any
	_ = json.Unmarshal(login.Data, &lr)
	if lr["need_totp"] != true {
		t.Fatalf("应要求 TOTP：%s", login.Data)
	}
	ticket, _ := lr["ticket"].(string)
	codeNow, _ = totp.GenerateCode(st.Secret, time.Now().UTC())
	code, ver := doJSON(t, c2, "POST", api+"/auth/totp/verify", "", map[string]string{"ticket": ticket, "code": codeNow})
	if code != 200 || !ver.OK {
		t.Fatalf("totp verify %d %+v", code, ver)
	}
}

func TestWhitelistRejectsSelfLock(t *testing.T) {
	base, prefix, pass, client, _ := startTest(t)
	api := base + "/" + prefix + "/api/v1"
	csrf := loginCSRF(t, client, api, "admin", pass)
	code, e := doJSON(t, client, "PUT", api+"/settings", csrf, map[string]any{
		"ops_ip_cidrs": []string{"10.0.0.0/8"},
	})
	if code != 400 || e.OK {
		t.Fatalf("自锁应 400，得到 %d %+v", code, e)
	}
	if e.Error == nil || e.Error.Message != "当前 IP 不在新白名单内，拒绝保存" {
		t.Fatalf("错误文案不符：%+v", e.Error)
	}
	code, sess := doJSON(t, client, "GET", api+"/auth/session", "", nil)
	if code != 200 || !sess.OK {
		t.Fatalf("拒绝后会话应仍可用，得到 %d", code)
	}
	code, ok := doJSON(t, client, "PUT", api+"/settings", csrf, map[string]any{
		"ops_ip_cidrs": []string{"127.0.0.1"},
	})
	if code != 200 || !ok.OK {
		t.Fatalf("包含自己的白名单应成功 %d %+v", code, ok)
	}
}

func TestTOTPSetupConflictWhenEnabled(t *testing.T) {
	base, prefix, pass, client, _ := startTest(t)
	api := base + "/" + prefix + "/api/v1"
	csrf := loginCSRF(t, client, api, "admin", pass)
	_, setup := doJSON(t, client, "POST", api+"/auth/totp/setup", csrf, map[string]any{})
	var st auth.TOTPSetup
	_ = json.Unmarshal(setup.Data, &st)
	codeNow, err := totp.GenerateCode(st.Secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	code, en := doJSON(t, client, "POST", api+"/auth/totp/enable", csrf, map[string]string{"code": codeNow})
	if code != 200 || !en.OK {
		t.Fatalf("enable %d %+v", code, en)
	}
	code, again := doJSON(t, client, "POST", api+"/auth/totp/setup", csrf, map[string]any{})
	if code != 409 || again.OK {
		t.Fatalf("已开启时 setup 应 409，得到 %d %+v", code, again)
	}
	_, sess := doJSON(t, client, "GET", api+"/auth/session", "", nil)
	var data map[string]any
	_ = json.Unmarshal(sess.Data, &data)
	if data["totp_enabled"] != true {
		t.Fatalf("TOTP 不得被关闭：%s", sess.Data)
	}
}

func TestRequestIDIgnoresClient(t *testing.T) {
	base, prefix, pass, client, _ := startTest(t)
	api := base + "/" + prefix + "/api/v1"
	csrf := loginCSRF(t, client, api, "admin", pass)
	hdr := http.Header{}
	hdr.Set("X-Request-Id", "client-injected")
	code, e := doJSONH(t, client, "PUT", api+"/settings", csrf, map[string]any{"ops_ip_cidrs": []string{}}, hdr)
	if code != 200 || !e.OK {
		t.Fatalf("保存失败 %d %+v", code, e)
	}
	_, au := doJSON(t, client, "GET", api+"/audit", csrf, nil)
	raw, _ := json.Marshal(au.Data)
	if strings.Contains(string(raw), "client-injected") {
		t.Fatalf("审计采信了客户端 request_id：%s", raw)
	}
}

func TestSiteCRUDAndSSL(t *testing.T) {
	base, prefix, pass, client, _ := startTest(t)
	api := base + "/" + prefix + "/api/v1"
	csrf := loginCSRF(t, client, api, "admin", pass)
	code, e := doJSON(t, client, "POST", api+"/sites", csrf, map[string]any{
		"site_key": "demo", "name": "演示", "domains": []string{"demo.test"},
	})
	if code != 200 || !e.OK {
		t.Fatalf("建站失败 %d %+v", code, e)
	}
	var st map[string]any
	_ = json.Unmarshal(e.Data, &st)
	id := int(st["id"].(float64))
	code, raw := doJSON(t, client, "GET", api+"/sites/"+itoa(id)+"/nginx", csrf, nil)
	if code != 200 || !raw.OK {
		t.Fatalf("读 conf %d %+v", code, raw)
	}
	code, iss := doJSON(t, client, "POST", api+"/sites/"+itoa(id)+"/ssl/issue", csrf, map[string]any{})
	if code != 200 || !iss.OK {
		t.Fatalf("签发 %d %+v", code, iss)
	}
	code, got := doJSON(t, client, "GET", api+"/sites/"+itoa(id), csrf, nil)
	if code != 200 {
		t.Fatal(got)
	}
	var det map[string]any
	_ = json.Unmarshal(got.Data, &det)
	if det["ssl_cert"] == "" || det["ssl_cert"] == nil {
		t.Fatalf("应有证书路径：%s", got.Data)
	}
}

func TestStage3PHPMySQLAPI(t *testing.T) {
	base, prefix, pass, client, _ := startTest(t)
	api := base + "/" + prefix + "/api/v1"
	csrf := loginCSRF(t, client, api, "admin", pass)
	code, e := doJSON(t, client, "GET", api+"/php/versions", csrf, nil)
	if code != 200 || !e.OK {
		t.Fatalf("php versions %d %+v", code, e)
	}
	code, e = doJSON(t, client, "POST", api+"/mysql/databases", csrf, map[string]string{"name": "blog"})
	if code != 200 || !e.OK {
		t.Fatalf("create db %d %+v", code, e)
	}
	code, e = doJSON(t, client, "POST", api+"/mysql/databases", csrf, map[string]string{"name": "a;drop"})
	if code == 200 && e.OK {
		t.Fatal("注入库名应拒绝")
	}
	code, e = doJSON(t, client, "POST", api+"/mysql/users", csrf, map[string]any{
		"user": "blogu", "host": "localhost", "password": "GoodPass1", "databases": []string{"blog"},
	})
	if code != 200 || !e.OK {
		t.Fatalf("create user %d %+v", code, e)
	}
	code, e = doJSON(t, client, "POST", api+"/env/php/repo", csrf, map[string]any{})
	if code != 200 || !e.OK {
		t.Fatalf("env install %d %+v", code, e)
	}
	var job map[string]any
	_ = json.Unmarshal(e.Data, &job)
	jid, _ := job["job_id"].(string)
	if jid == "" {
		t.Fatal("缺 job_id")
	}
	ok := false
	for i := 0; i < 50; i++ {
		_, st := doJSON(t, client, "GET", api+"/jobs/"+jid, csrf, nil)
		var js map[string]any
		_ = json.Unmarshal(st.Data, &js)
		if js["phase"] == "done" {
			ok = true
			break
		}
		if js["phase"] == "failed" {
			t.Fatalf("job failed %+v", js)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ok {
		t.Fatal("env-install 任务未完成")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [16]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
