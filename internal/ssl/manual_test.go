package ssl

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/site"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

func setupSSL(t *testing.T) (*Service, int64) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
	sites.CertDir = filepath.Join(dir, "certs")
	sites.SkipNX = true
	st, err := sites.Create(context.Background(), site.In{SiteKey: "m1", Domains: []string{"m1.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, sites)
	svc.Dir = sites.CertDir
	svc.AcmeWWW = ngx.AcmeWWW
	svc.Issuer = SelfSigner{}
	return svc, st.ID
}

func TestImportManualMatchAndWarning(t *testing.T) {
	svc, id := setupSSL(t)
	cert, key, _, _, err := SelfSigner{}.Issue(context.Background(), []string{"m1.example.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ImportManual(id, cert, key)
	if err != nil {
		t.Fatal(err)
	}
	if res.Warning != "" {
		t.Fatalf("覆盖域名不应警告：%s", res.Warning)
	}
	var issuer string
	if err := svc.db.SQL.QueryRow(`SELECT issuer FROM certificates WHERE site_id=?`, id).Scan(&issuer); err != nil || issuer != "manual" {
		t.Fatalf("issuer %s %v", issuer, err)
	}
	other, okey, _, _, err := SelfSigner{}.Issue(context.Background(), []string{"other.example.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err = svc.ImportManual(id, other, okey)
	if err != nil {
		t.Fatal(err)
	}
	if res.Warning == "" || !strings.Contains(res.Warning, "m1.example.test") {
		t.Fatalf("应警告域名：%s", res.Warning)
	}
}

func TestImportManualRejectsExpiredAndBadKey(t *testing.T) {
	svc, id := setupSSL(t)
	cert, key := expiredPair(t, "m1.example.test")
	if _, err := svc.ImportManual(id, cert, key); err == nil || !strings.Contains(err.Error(), "过期") {
		t.Fatalf("过期：%v", err)
	}
	good, _, _, _, err := SelfSigner{}.Issue(context.Background(), []string{"m1.example.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, otherKey, _, _, err := SelfSigner{}.Issue(context.Background(), []string{"m1.example.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportManual(id, good, otherKey); err == nil || !strings.Contains(err.Error(), "不匹配") {
		t.Fatalf("密钥：%v", err)
	}
}

func TestRenewDueSkipsManual(t *testing.T) {
	svc, id := setupSSL(t)
	cert, key, _, _, err := SelfSigner{}.Issue(context.Background(), []string{"m1.example.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportManual(id, cert, key); err != nil {
		t.Fatal(err)
	}
	_, _ = svc.db.SQL.Exec(`UPDATE certificates SET expires_at=? WHERE site_id=?`, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), id)
	if err := svc.RenewDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	var issuer string
	if err := svc.db.SQL.QueryRow(`SELECT issuer FROM certificates WHERE site_id=?`, id).Scan(&issuer); err != nil || issuer != "manual" {
		t.Fatalf("手动证书不得被自动续期改写：%s %v", issuer, err)
	}
}

func expiredPair(t *testing.T, domain string) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	return certPEM, keyPEM
}
