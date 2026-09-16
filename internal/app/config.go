package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	panelFS "lnmp-panel/internal/fs"
)

type FileConfig struct {
	Listen     string `json:"listen"`
	PathPrefix string `json:"path_prefix"`
	TLSCert    string `json:"tls_cert"`
	TLSKey     string `json:"tls_key"`
	DB         string `json:"db"`
	BindMode   string `json:"bind_mode"`
	DataDir    string `json:"data_dir"`
}

func DefaultConfig(dataDir string) FileConfig {
	return FileConfig{
		Listen:     "",
		PathPrefix: "",
		TLSCert:    filepath.Join(dataDir, "tls", "panel.crt"),
		TLSKey:     filepath.Join(dataDir, "tls", "panel.key"),
		DB:         filepath.Join(dataDir, "panel.db"),
		BindMode:   "public",
		DataDir:    dataDir,
	}
}

func LoadConfig(path string) (FileConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, err
	}
	var c FileConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return FileConfig{}, err
	}
	return c, nil
}

func SaveConfig(path string, c FileConfig) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return panelFS.WriteAtomic(path, b, 0600)
}

func RandomPort() (int, error) {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return 0, err
	}
	n := int(b[0])<<8 | int(b[1])
	return 20000 + n%(49151-20000+1), nil
}

func RandomPrefix() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

func EnsureTLS(certPath, keyPath string) error {
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "lnmp-panel", Organization: []string{"LNMP面板"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	if host != "" {
		tpl.DNSNames = append(tpl.DNSNames, host)
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return err
	}
	return os.WriteFile(keyPath, keyPEM, 0600)
}
