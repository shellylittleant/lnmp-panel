package migrate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/site"
	"lnmp-panel/internal/ssl"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

func TestParseAndHashToken(t *testing.T) {
	fp := strings.Repeat("ab", 32)
	raw, err := NewToken(fp)
	if err != nil {
		t.Fatal(err)
	}
	p, sec, err := ParseToken(raw)
	if err != nil || p != fp[:16] || len(sec) < 16 {
		t.Fatalf("%s %v", raw, err)
	}
	if _, _, err := ParseToken("short"); err == nil {
		t.Fatal("应拒绝")
	}
	if HashToken("a") == HashToken("b") {
		t.Fatal("hash")
	}
}

func TestFingerprintPrefix(t *testing.T) {
	c := &x509.Certificate{Raw: []byte("hello-cert-raw-bytes")}
	fp := Fingerprint(c)
	if len(fp) != 64 {
		t.Fatal(fp)
	}
	sum := sha256.Sum256(c.Raw)
	if fp != hex.EncodeToString(sum[:]) {
		t.Fatal("mismatch")
	}
	if FingerprintPrefix(fp) != fp[:16] {
		t.Fatal(FingerprintPrefix(fp))
	}
}

func TestAuthorizedKeysRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "authorized_keys")
	_ = os.WriteFile(p, []byte("ssh-ed25519 AAAA keep-me\n"), 0600)
	if err := upsertAuthorizedKeys(p, "ssh-ed25519 BBBB mig"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "keep-me") || !strings.Contains(string(b), "mig") {
		t.Fatal(string(b))
	}
	if err := upsertAuthorizedKeys(p, "ssh-ed25519\nINJECT"); err == nil {
		t.Fatal("换行应拒绝")
	}
	if err := removeAuthorizedKeys(p); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), "mig") || !strings.Contains(string(b), "keep-me") {
		t.Fatal(string(b))
	}
}

func writeTestCert(t *testing.T, dir string) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "t"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "t.crt")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func setupMig(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cert := writeTestCert(t, dir)
	st := settings.New(db)
	s := New(db)
	s.TLSCert = cert
	s.Settings = st
	s.Secrets = filepath.Join(dir, "sec")
	s.AuthKeys = filepath.Join(dir, "authorized_keys")
	s.SkipExec = true
	s.WWW = filepath.Join(dir, "www")
	s.DataDir = dir
	s.HostKeyPub = filepath.Join(dir, "host.pub")
	_ = os.WriteFile(s.HostKeyPub, []byte("ssh-ed25519 AAAA host"), 0644)
	return s, dir
}

func TestTokenAndAcceptOnce(t *testing.T) {
	s, _ := setupMig(t)
	tok, err := s.Token()
	if err != nil {
		t.Fatal(err)
	}
	fp, _ := FingerprintPEMFile(s.TLSCert)
	if tok.Prefix != fp[:16] {
		t.Fatalf("%s vs %s", tok.Prefix, fp[:16])
	}
	_, err = s.Accept(AcceptIn{Token: tok.Token, SSHPub: "ssh-ed25519 AAAA a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(AcceptIn{Token: tok.Token, SSHPub: "ssh-ed25519 AAAA b"}); err == nil {
		t.Fatal("令牌应一次性")
	}
	b, _ := os.ReadFile(s.AuthKeys)
	if !strings.Contains(string(b), "AAAA a") {
		t.Fatal(string(b))
	}
}

func TestKnownHostsUsesSSHHost(t *testing.T) {
	p := filepath.Join(t.TempDir(), "kh")
	if err := writeKnownHosts(p, "lnmp-mig-b", 22, "ssh-ed25519 AAAA host"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if strings.Contains(s, "18443") {
		t.Fatalf("不得写入面板端口：%s", s)
	}
	if !strings.Contains(s, "lnmp-mig-b ssh-ed25519") || !strings.Contains(s, "[lnmp-mig-b]:22") {
		t.Fatal(s)
	}
	host, err := sshHostOf("https://lnmp-mig-b:18443/abcd")
	if err != nil || host != "lnmp-mig-b" {
		t.Fatalf("%s %v", host, err)
	}
}

func TestConnectPinsFingerprintAndKnownHosts(t *testing.T) {
	s, dir := setupMig(t)
	tok, err := s.Token()
	if err != nil {
		t.Fatal(err)
	}
	fp, err := FingerprintPEMFile(s.TLSCert)
	if err != nil {
		t.Fatal(err)
	}
	s.DialFP = func(ctx context.Context, hostPort string) (string, error) {
		if hostPort != "127.0.0.1:18443" {
			t.Fatalf("dial %s", hostPort)
		}
		return fp, nil
	}
	s.HTTPDo = func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/migrate/accept") {
			t.Fatalf("path %s", req.URL.Path)
		}
		body := `{"ok":true,"data":{"ssh_user":"root","ssh_port":22,"ssh_host_key":"ssh-ed25519 AAAA hostkey","peer_id":1}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
	out, err := s.Connect(context.Background(), ConnectIn{Address: "https://127.0.0.1:18443/abcdabcdabcdabcdabcdabcdabcdabcd", Token: tok.Token})
	if err != nil {
		t.Fatal(err)
	}
	if out.Fingerprint != fp {
		t.Fatalf("fp %s", out.Fingerprint)
	}
	kh, _ := os.ReadFile(filepath.Join(dir, "sec", "migrate-known_hosts"))
	if strings.Contains(string(kh), "18443") || !strings.Contains(string(kh), "127.0.0.1 ssh-ed25519") {
		t.Fatalf("known_hosts %s", kh)
	}
	var stored string
	_ = s.db.SQL.QueryRow(`SELECT fingerprint FROM peers WHERE role='migrate_target'`).Scan(&stored)
	if stored != fp {
		t.Fatalf("stored %s", stored)
	}
}

func TestConnectRejectsFingerprintMismatch(t *testing.T) {
	s, _ := setupMig(t)
	tok, err := s.Token()
	if err != nil {
		t.Fatal(err)
	}
	s.DialFP = func(ctx context.Context, hostPort string) (string, error) {
		return strings.Repeat("00", 32), nil
	}
	_, err = s.Connect(context.Background(), ConnectIn{Address: "https://127.0.0.1:18443/abcd", Token: tok.Token})
	if err == nil || !strings.Contains(err.Error(), "指纹") {
		t.Fatalf("应拒绝指纹：%v", err)
	}
}

func TestCancelRemovesKeys(t *testing.T) {
	s, _ := setupMig(t)
	_ = os.WriteFile(filepath.Join(s.Secrets, "migrate-key"), []byte("k"), 0600)
	_ = os.MkdirAll(s.Secrets, 0700)
	_ = os.WriteFile(filepath.Join(s.Secrets, "migrate-key"), []byte("k"), 0600)
	_ = upsertAuthorizedKeys(s.AuthKeys, "ssh-ed25519 AAAA x")
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Secrets, "migrate-key")); !os.IsNotExist(err) {
		t.Fatal("私钥应删除")
	}
	b, _ := os.ReadFile(s.AuthKeys)
	if strings.Contains(string(b), "AAAA x") {
		t.Fatal("授权钥应去掉")
	}
}

func TestApplyBundleRemapsTemplates(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tpl := template.New(db)
	if err := tpl.SyncOfficial(); err != nil {
		t.Fatal(err)
	}
	ngx := nginx.New()
	ngx.Available = filepath.Join(dir, "a")
	ngx.Enabled = filepath.Join(dir, "e")
	ngx.ConfD = filepath.Join(dir, "c")
	ngx.AcmeWWW = filepath.Join(dir, "acme")
	ngx.BackupRoot = filepath.Join(dir, "b")
	_ = ngx.EnsureLayout()
	sites := site.New(db, tpl, ngx)
	sites.WWW = filepath.Join(dir, "www")
	sites.SkipNX = true
	sslSvc := ssl.New(db, sites)
	sslSvc.Dir = filepath.Join(dir, "certs")
	st := settings.New(db)
	s := New(db)
	s.Tpl, s.Sites, s.Nginx, s.SSL, s.Settings = tpl, sites, ngx, sslSvc, st
	s.SkipExec = true
	s.WWW = sites.WWW
	userContent := "server { listen 80; # user-tpl-mig }\n"
	id, err := tpl.EnsureUser("nginx_site", userContent, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := tpl.Get(id)
	b := Bundle{
		Templates: []TplRow{{Type: "nginx_site", Name: "我的", Content: userContent, Hash: rec.ContentHash}},
		Sites: []SiteRow{{
			SiteKey: "demo", Name: "演示", Root: filepath.Join(sites.WWW, "demo"),
			NginxTplHash: rec.ContentHash, Enabled: true, NginxConf: "server { listen 80; server_name demo.test; }\n",
		}},
		Domains: []DomRow{{SiteKey: "demo", Domain: "demo.test"}},
		Certs: []CertRow{{
			SiteKey: "demo", Issuer: "self", Challenge: "http-01",
			CertPEM: "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n",
			KeyPEM:  "-----BEGIN EC PRIVATE KEY-----\nMHc\n-----END EC PRIVATE KEY-----\n",
			ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		}},
		MySQLDBs: []DBRow{{Name: "migdb", Charset: "utf8mb4", Collate: "utf8mb4_0900_ai_ci"}},
	}
	if err := s.applyBundle(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	items, err := sites.List()
	if err != nil || len(items) != 1 || items[0].SiteKey != "demo" {
		t.Fatalf("%+v %v", items, err)
	}
	conf, _ := ngx.Read("demo")
	if !strings.Contains(conf, "demo.test") {
		t.Fatal(conf)
	}
	if items[0].SSLCert == "" {
		t.Fatal("应有证书路径")
	}
	if _, err := os.Stat(items[0].SSLCert); err != nil {
		t.Fatal(err)
	}
	if items[0].BasicAuthFile != "" {
		t.Fatalf("basic_auth_file 应为空字符串：%q", items[0].BasicAuthFile)
	}
	var n int
	if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM mysql_databases WHERE name='migdb'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("mysql 元数据 %d %v", n, err)
	}
}

func TestPreflightSetsOKOnSupportedOS(t *testing.T) {
	s, _ := setupMig(t)
	s.OS.ID = "ubuntu"
	s.OS.VersionID = "24.04"
	s.OS.Pretty = "Ubuntu 24.04 LTS"
	pf := s.PreflightLocal(1024)
	if !pf.OK {
		t.Fatalf("%+v", pf)
	}
}

func TestDumpMySQLSkipsWhenNotInstalled(t *testing.T) {
	s, dir := setupMig(t)
	path := filepath.Join(dir, "tmp", "migrate-mysql.sql")
	if err := s.dumpMySQL(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("未安装 MySQL 不应写出 dump")
	}
}
