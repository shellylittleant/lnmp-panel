package ssl

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	panelFS "lnmp-panel/internal/fs"
)

type ImportResult struct {
	Warning string `json:"warning,omitempty"`
}

func (s *Service) ImportManual(siteID int64, certPEM, keyPEM []byte) (ImportResult, error) {
	var out ImportResult
	if s.sites == nil {
		return out, fmt.Errorf("站点模块未初始化")
	}
	st, err := s.sites.Get(siteID)
	if err != nil {
		return out, err
	}
	leaf, err := parseLeaf(certPEM)
	if err != nil {
		return out, err
	}
	if time.Now().After(leaf.NotAfter) {
		return out, fmt.Errorf("证书已过期")
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return out, fmt.Errorf("私钥与证书不匹配")
	}
	var missing []string
	for _, d := range st.Domains {
		if err := leaf.VerifyHostname(d); err != nil {
			missing = append(missing, d)
		}
	}
	if len(missing) > 0 {
		out.Warning = "证书未覆盖域名：" + strings.Join(missing, "、") + "，已保存但不阻止"
	}
	dir := filepath.Join(s.Dir, fmt.Sprintf("%d", siteID))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return out, err
	}
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	if err := panelFS.WriteAtomic(certPath, certPEM, 0644); err != nil {
		return out, err
	}
	if err := panelFS.WriteAtomic(keyPath, keyPEM, 0600); err != nil {
		return out, err
	}
	notAfter := leaf.NotAfter
	dom, _ := json.Marshal(st.Domains)
	tx, err := s.db.SQL.Begin()
	if err != nil {
		return out, err
	}
	if _, err := tx.Exec(`DELETE FROM certificates WHERE site_id=?`, siteID); err != nil {
		_ = tx.Rollback()
		return out, err
	}
	if _, err := tx.Exec(`INSERT INTO certificates(site_id, domains, issuer, cert_path, key_path, expires_at, challenge, created_at)
		VALUES (?,?,?,?,?,?, 'manual', ?)`, siteID, string(dom), "manual", certPath, keyPath, notAfter.UTC().Format(time.RFC3339), s.db.Now()); err != nil {
		_ = tx.Rollback()
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	if err := s.sites.ApplyConf(context.Background(), siteID); err != nil {
		return out, err
	}
	return out, nil
}

func parseLeaf(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("无法解析证书 PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

func (s *Service) ManualExpiring(within time.Duration) ([]Cert, error) {
	cut := time.Now().UTC().Add(within).Format(time.RFC3339)
	rows, err := s.db.SQL.Query(`SELECT id, site_id, domains, issuer, cert_path, key_path, expires_at, challenge, created_at
		FROM certificates WHERE issuer='manual' AND expires_at <= ? ORDER BY expires_at`, cut)
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
