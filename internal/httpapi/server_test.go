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
	"testing"
	"time"

	"lnmp-panel/internal/audit"
	"lnmp-panel/internal/auth"
	"lnmp-panel/internal/env"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/store"

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
	prefix = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s := &Server{
		Prefix:   prefix,
		Auth:     auth.New(db),
		Audit:    audit.New(db),
		Settings: st,
		Jobs:     jobs.New(db),
		Started:  time.Now(),
		Listen:   "127.0.0.1:0",
		OS:       env.OSInfo{ID: "ubuntu", VersionID: "24.04", Pretty: "Ubuntu 24.04 LTS"},
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
