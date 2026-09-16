package ssl

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/acme"
)

// ACMEIssuer 使用 golang.org/x/crypto/acme 做 HTTP-01。
type ACMEIssuer struct {
	Directory string
	Account   string // 账户私钥路径
}

func (a ACMEIssuer) Issue(ctx context.Context, domains []string, putHTTP01 func(token, content string) error) ([]byte, []byte, string, time.Time, error) {
	dir := a.Directory
	if dir == "" {
		dir = acme.LetsEncryptURL
	}
	key, err := a.accountKey()
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	client := &acme.Client{Key: key, DirectoryURL: dir}
	if _, err := client.GetReg(ctx, ""); err != nil {
		_, err = client.Register(ctx, &acme.Account{Contact: []string{}}, acme.AcceptTOS)
		if err != nil {
			return nil, nil, "", time.Time{}, fmt.Errorf("ACME 注册失败：%w", err)
		}
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(domains...))
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	for _, authzURL := range order.AuthzURLs {
		az, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return nil, nil, "", time.Time{}, err
		}
		if az.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		for _, c := range az.Challenges {
			if c.Type == "http-01" {
				chal = c
				break
			}
		}
		if chal == nil {
			return nil, nil, "", time.Time{}, fmt.Errorf("无 HTTP-01 挑战")
		}
		tok := chal.Token
		content, err := client.HTTP01ChallengeResponse(tok)
		if err != nil {
			return nil, nil, "", time.Time{}, err
		}
		if err := putHTTP01(tok, content); err != nil {
			return nil, nil, "", time.Time{}, err
		}
		if _, err := client.Accept(ctx, chal); err != nil {
			return nil, nil, "", time.Time{}, err
		}
		if _, err := client.WaitAuthorization(ctx, az.URI); err != nil {
			return nil, nil, "", time.Time{}, err
		}
	}
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: domains}, certKey)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	der, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	var certPEM []byte
	for _, c := range der {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c})...)
	}
	kb, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	return certPEM, keyPEM, "letsencrypt", time.Now().Add(90 * 24 * time.Hour), nil
}

func (a ACMEIssuer) accountKey() (*ecdsa.PrivateKey, error) {
	path := a.Account
	if path == "" {
		path = "/var/lib/lnmp-panel/secrets/acme.key"
	}
	if b, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(b)
		if block != nil {
			k, err := x509.ParseECPrivateKey(block.Bytes)
			if err == nil {
				return k, nil
			}
		}
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	kb, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0600); err != nil {
		return nil, err
	}
	return k, nil
}

var _ crypto.Signer = (*ecdsa.PrivateKey)(nil)
