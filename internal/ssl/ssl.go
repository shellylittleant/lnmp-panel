package ssl

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	panelFS "lnmp-panel/internal/fs"
	"lnmp-panel/internal/site"
	"lnmp-panel/internal/store"
)

type Cert struct {
	ID        int64  `json:"id"`
	SiteID    *int64 `json:"site_id"`
	Domains   string `json:"domains"`
	Issuer    string `json:"issuer"`
	CertPath  string `json:"cert_path"`
	KeyPath   string `json:"key_path"`
	ExpiresAt string `json:"expires_at"`
	Challenge string `json:"challenge"`
	CreatedAt string `json:"created_at"`
}

type Issuer interface {
	Issue(ctx context.Context, domains []string, putHTTP01 func(token, content string) error) (certPEM, keyPEM []byte, issuer string, notAfter time.Time, err error)
}

type Service struct {
	db      *store.DB
	sites   *site.Service
	Dir     string
	AcmeWWW string
	Issuer  Issuer
}

func New(db *store.DB, sites *site.Service) *Service {
	return &Service{
		db:      db,
		sites:   sites,
		Dir:     "/var/lib/lnmp-panel/tls/sites",
		AcmeWWW: "/var/lib/lnmp-panel/acme-www",
		Issuer:  SelfSigner{},
	}
}

func (s *Service) List() ([]Cert, error) {
	rows, err := s.db.SQL.Query(`SELECT id, site_id, domains, issuer, cert_path, key_path, expires_at, challenge, created_at FROM certificates ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cert
	for rows.Next() {
		var c Cert
		var sid *int64
		if err := rows.Scan(&c.ID, &sid, &c.Domains, &c.Issuer, &c.CertPath, &c.KeyPath, &c.ExpiresAt, &c.Challenge, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.SiteID = sid
		out = append(out, c)
	}
	if out == nil {
		out = []Cert{}
	}
	return out, rows.Err()
}

func (s *Service) Issue(ctx context.Context, siteID int64) error {
	st, err := s.sites.Get(siteID)
	if err != nil {
		return err
	}
	for _, d := range st.Domains {
		if strings.Contains(d, "*") {
			return fmt.Errorf("HTTP-01 不能签通配符证书")
		}
	}
	if s.Issuer == nil {
		return fmt.Errorf("未配置证书签发器")
	}
	if err := os.MkdirAll(s.AcmeWWW, 0755); err != nil {
		return err
	}
	certPEM, keyPEM, issuer, notAfter, err := s.Issuer.Issue(ctx, st.Domains, func(token, content string) error {
		return os.WriteFile(filepath.Join(s.AcmeWWW, token), []byte(content), 0644)
	})
	if err != nil {
		return err
	}
	dir := filepath.Join(s.Dir, fmt.Sprintf("%d", siteID))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	if err := panelFS.WriteAtomic(certPath, certPEM, 0644); err != nil {
		return err
	}
	if err := panelFS.WriteAtomic(keyPath, keyPEM, 0600); err != nil {
		return err
	}
	dom, _ := json.Marshal(st.Domains)
	_, err = s.db.SQL.Exec(`INSERT INTO certificates(site_id, domains, issuer, cert_path, key_path, expires_at, challenge, created_at)
		VALUES (?,?,?,?,?,?, 'http-01', ?)`, siteID, string(dom), issuer, certPath, keyPath, notAfter.UTC().Format(time.RFC3339), s.db.Now())
	if err != nil {
		return err
	}
	return s.sites.ApplyConf(ctx, siteID)
}

func (s *Service) RenewDue(ctx context.Context) error {
	cut := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	rows, err := s.db.SQL.Query(`SELECT DISTINCT site_id FROM certificates WHERE site_id IS NOT NULL AND expires_at <= ?`, cut)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		if err := s.Issue(ctx, id); err != nil {
			return fmt.Errorf("站点 %d 续期失败：%w", id, err)
		}
	}
	return nil
}

// SelfSigner 测试与无公网 ACME 时使用。生产环境通过 ACME 目录签发 Let's Encrypt。
type SelfSigner struct{}

func (SelfSigner) Issue(_ context.Context, domains []string, _ func(string, string) error) ([]byte, []byte, string, time.Time, error) {
	if len(domains) == 0 {
		return nil, nil, "", time.Time{}, fmt.Errorf("无域名")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	notAfter := time.Now().Add(90 * 24 * time.Hour)
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domains[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		DNSNames:     domains,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	return certPEM, keyPEM, "selfsigned", notAfter, nil
}
