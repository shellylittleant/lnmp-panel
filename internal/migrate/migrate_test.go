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
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lnmp-panel/internal/firewall"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/php"
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
	if err := upsertAuthorizedKeys(p, "ssh-ed25519 BBBB mig", "203.0.113.8"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "keep-me") || !strings.Contains(string(b), "mig") {
		t.Fatal(string(b))
	}
	if !strings.Contains(string(b), `from="203.0.113.8"`) || !strings.Contains(string(b), "no-pty") {
		t.Fatal(string(b))
	}
	if err := upsertAuthorizedKeys(p, "ssh-ed25519\nINJECT", ""); err == nil {
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
	if !strings.Contains(string(b), "AAAA a") || !strings.Contains(string(b), "no-pty") {
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

func TestRemoteSSHPath(t *testing.T) {
	if !remoteSSHPath("root@10.0.0.1:/var/www/") || remoteSSHPath("/var/www/") || remoteSSHPath("C:oops") {
		t.Fatal("应识别 user@host:path 为远程")
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
	_ = upsertAuthorizedKeys(s.AuthKeys, "ssh-ed25519 AAAA x", "192.0.2.1")
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
			NginxTplHash: rec.ContentHash, Enabled: true, RawMode: true,
			NginxConf: "server { listen 80; server_name demo.test; ssl_certificate /var/lib/lnmp-panel/tls/sites/99/fullchain.pem; }\n",
		}},
		Domains: []DomRow{{SiteKey: "demo", Domain: "demo.test"}},
		Certs: []CertRow{{
			SiteKey: "demo", Issuer: "self", Challenge: "http-01",
			CertPEM:   "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n",
			KeyPEM:    "-----BEGIN EC PRIVATE KEY-----\nMHc\n-----END EC PRIVATE KEY-----\n",
			ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		}},
		MySQLDBs: []DBRow{{Name: "migdb", Charset: "utf8mb4", Collate: "utf8mb4_0900_ai_ci"}},
	}
	if _, err := s.applyBundle(context.Background(), b); err != nil {
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
	if strings.Contains(conf, "tls/sites/99/") || !strings.Contains(conf, fmt.Sprintf("tls/sites/%d/", items[0].ID)) {
		t.Fatalf("原文证书路径应改写为 B 的 ID：%s", conf)
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

func TestPairingExpiresAfterIdle(t *testing.T) {
	s, _ := setupMig(t)
	s.IdleFor = time.Hour
	tok, err := s.Token()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(AcceptIn{Token: tok.Token, SSHPub: "ssh-ed25519 AAAA z", From: "192.0.2.9"}); err != nil {
		t.Fatal(err)
	}
	old := s.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	_, _ = s.db.SQL.Exec(`UPDATE peers SET last_seen_at=? WHERE role='migrate_source'`, old)
	if err := s.validPeerToken(tok.Token); err == nil || !strings.Contains(err.Error(), "过期") {
		t.Fatalf("应过期：%v", err)
	}
	b, _ := os.ReadFile(s.AuthKeys)
	if strings.Contains(string(b), "AAAA z") {
		t.Fatal("过期应拆掉授权钥")
	}
}

func TestStartRequiresConfirmWhenTargetHasSites(t *testing.T) {
	s, _ := setupMig(t)
	tok, err := s.Token()
	if err != nil {
		t.Fatal(err)
	}
	fp, _ := FingerprintPEMFile(s.TLSCert)
	s.DialFP = func(ctx context.Context, hostPort string) (string, error) { return fp, nil }
	s.HTTPDo = func(req *http.Request) (*http.Response, error) {
		body := `{"ok":true,"data":{"ssh_user":"root","ssh_port":22,"ssh_host_key":"ssh-ed25519 AAAA hostkey","peer_id":1}}`
		if strings.Contains(req.URL.Path, "preflight") {
			body = `{"ok":true,"data":{"ok":true,"site_count":2,"site_keys":["old1","old2"],"errors":[]}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}
	if _, err := s.Connect(context.Background(), ConnectIn{Address: "https://127.0.0.1:18443/abcdabcdabcdabcdabcdabcdabcdabcd", Token: tok.Token}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(context.Background(), "full", false); err == nil || !strings.Contains(err.Error(), "将被替换") {
		t.Fatalf("未确认应拒绝：%v", err)
	}
}

func TestApplyFirewallFormDoesNotWriteCanonical(t *testing.T) {
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
	st := settings.New(db)
	_ = st.Set("listen_port", "28443")
	fw := firewall.New(db, tpl, st)
	fw.SkipNFT = true
	fw.PanelPort = 28443
	fw.Canonical = filepath.Join(dir, "boot.nft")
	fw.Draft = filepath.Join(dir, "draft.nft")
	fw.BackupRoot = filepath.Join(dir, "bak")
	_ = os.WriteFile(fw.Canonical, []byte(firewall.Placeholder), 0600)
	s := New(db)
	s.Tpl, s.Firewall, s.Settings = tpl, fw, st
	s.SkipExec = true
	form := firewall.Form{SSHPort: 22, PanelPort: 18443, InputPolicy: "drop", PublicHTTP: true}
	if _, err := s.applyBundle(context.Background(), Bundle{FirewallForm: &form}); err != nil {
		t.Fatal(err)
	}
	can, _ := os.ReadFile(fw.Canonical)
	if strings.Contains(string(can), "18443") {
		t.Fatalf("开机文件含 A 端口：%s", can)
	}
	draft, _ := os.ReadFile(fw.Draft)
	if strings.Contains(string(draft), "18443") || !strings.Contains(string(draft), "28443") {
		t.Fatalf("草稿应是 B 端口：%s", draft)
	}
}

func TestApplyRecalcPHPInstalledAndMissing(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tpl := template.New(db)
	_ = tpl.SyncOfficial()
	phpSvc := php.New(db, tpl, jobs.New(db))
	phpSvc.SkipExec = true
	phpSvc.BinDir = filepath.Join(dir, "sbin")
	s := New(db)
	s.Tpl, s.PHP, s.SkipExec = tpl, phpSvc, true
	miss, err := s.applyBundle(context.Background(), Bundle{
		PHP: []PHPRow{{Version: "8.3", Installed: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var ins int
	_ = db.SQL.QueryRow(`SELECT installed FROM php_versions WHERE version='8.3'`).Scan(&ins)
	if ins != 0 {
		t.Fatalf("未装二进制不应标已安装：%d", ins)
	}
	if strings.Contains(strings.Join(miss, ","), "PHP 8.3") {
		t.Fatalf("无站点使用不应报缺 PHP 8.3：%v", miss)
	}
	php82 := "8.2"
	miss, err = s.applyBundle(context.Background(), Bundle{
		PHP: []PHPRow{{Version: "8.2", Installed: true}, {Version: "8.3", Installed: true}},
		Sites: []SiteRow{
			{SiteKey: "s1", Name: "s1", Root: filepath.Join(dir, "www", "s1"), PHPVersion: &php82, Enabled: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(miss, ",")
	if !strings.Contains(joined, "PHP 8.2") {
		t.Fatalf("站点在用的 8.2 应报缺：%v", miss)
	}
	if strings.Contains(joined, "PHP 8.3") {
		t.Fatalf("未使用的 8.3 不应报缺：%v", miss)
	}
}

func TestApplyUsesPEMExpiry(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tpl := template.New(db)
	_ = tpl.SyncOfficial()
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
	s := New(db)
	s.Tpl, s.Sites, s.Nginx, s.SSL = tpl, sites, ngx, sslSvc
	s.SkipExec = true
	certPath := writeTestCert(t, dir)
	pem, _ := os.ReadFile(certPath)
	want, err := ssl.LeafNotAfter(pem)
	if err != nil {
		t.Fatal(err)
	}
	off, err := tpl.OfficialByName("nginx_site", "静态站点")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.applyBundle(context.Background(), Bundle{
		Sites:   []SiteRow{{SiteKey: "c", Name: "c", Root: filepath.Join(sites.WWW, "c"), NginxTplHash: off.ContentHash, Enabled: true}},
		Domains: []DomRow{{SiteKey: "c", Domain: "c.test"}},
		Certs: []CertRow{{
			SiteKey: "c", Issuer: "self", Challenge: "http-01",
			CertPEM: string(pem), KeyPEM: "-----BEGIN EC PRIVATE KEY-----\nMHc\n-----END EC PRIVATE KEY-----\n",
			ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.SQL.QueryRow(`SELECT expires_at FROM certificates`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want.UTC().Format(time.RFC3339) {
		t.Fatalf("到期 %s want %s", got, want.UTC().Format(time.RFC3339))
	}
}

func TestPeerApplyIgnoresCallerDumpPath(t *testing.T) {
	s, dir := setupMig(t)
	tok, err := s.Token()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(AcceptIn{Token: tok.Token, SSHPub: "ssh-ed25519 AAAA p", From: "192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	evil := filepath.Join(dir, "evil.sql")
	if err := os.WriteFile(evil, []byte("DROP USER 'lnmp_panel'@'localhost';"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PeerApply(context.Background(), ApplyIn{Token: tok.Token, DumpPath: evil}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(evil); err != nil {
		t.Fatal("不得读取或删除 A 指定的 dump_path")
	}
}

func TestApplyRerendersNonRaw(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tpl := template.New(db)
	_ = tpl.SyncOfficial()
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
	s := New(db)
	s.Tpl, s.Sites, s.Nginx, s.SSL, s.SkipExec = tpl, sites, ngx, sslSvc, true
	off, _ := tpl.OfficialByName("nginx_site", "静态站点")
	pem := "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"
	_, err = s.applyBundle(context.Background(), Bundle{
		Sites: []SiteRow{{
			SiteKey: "blog", Name: "b", Root: filepath.Join(sites.WWW, "blog"),
			NginxTplHash: off.ContentHash, Enabled: true, RawMode: false,
			NginxConf: "server { # FROM-A tls/sites/7/fullchain.pem }",
		}},
		Domains: []DomRow{{SiteKey: "blog", Domain: "blog.test"}},
		Certs:   []CertRow{{SiteKey: "blog", CertPEM: pem, KeyPEM: "k", ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	conf, _ := ngx.Read("blog")
	if strings.Contains(conf, "FROM-A") {
		t.Fatalf("非原文应重渲染，不得用 A 的 conf：%s", conf)
	}
	if !strings.Contains(conf, "blog.test") {
		t.Fatal(conf)
	}
	items, _ := sites.List()
	if len(items) != 1 || items[0].SSLCert == "" || !strings.Contains(items[0].SSLCert, fmt.Sprintf("/%d/", items[0].ID)) {
		t.Fatalf("证书应按 B 的 ID 落盘 %+v", items)
	}
}
