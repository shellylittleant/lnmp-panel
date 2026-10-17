package migrate

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func FingerprintPrefix(fp string) string {
	if len(fp) < 16 {
		return fp
	}
	return fp[:16]
}

func FingerprintPEMFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return "", fmt.Errorf("证书不是 PEM")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	return Fingerprint(c), nil
}

func DialFingerprint(ctx context.Context, hostPort string) (string, error) {
	d := &tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}
	if deadline, ok := ctx.Deadline(); ok {
		_ = deadline
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	c, err := d.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return "", fmt.Errorf("连接 TLS 失败：%w", err)
	}
	defer c.Close()
	tc, ok := c.(*tls.Conn)
	if !ok {
		return "", fmt.Errorf("非 TLS 连接")
	}
	if err := tc.HandshakeContext(ctx); err != nil {
		return "", fmt.Errorf("TLS 握手失败：%w", err)
	}
	certs := tc.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("对端未提供证书")
	}
	return Fingerprint(certs[0]), nil
}

func SplitHostPortFromAddr(address string) (hostPort, prefix string, err error) {
	u := strings.TrimSpace(address)
	u = strings.TrimSuffix(u, "/")
	if !strings.HasPrefix(u, "https://") {
		return "", "", fmt.Errorf("地址须为 https://主机:端口/路径前缀")
	}
	rest := strings.TrimPrefix(u, "https://")
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return "", "", fmt.Errorf("地址须含路径前缀")
	}
	hostPort, prefix = rest[:slash], rest[slash+1:]
	if prefix == "" {
		return "", "", fmt.Errorf("路径前缀不能为空")
	}
	if _, _, err := net.SplitHostPort(hostPort); err != nil {
		if strings.Contains(hostPort, ":") {
			return "", "", fmt.Errorf("无效主机端口：%s", hostPort)
		}
		hostPort = net.JoinHostPort(hostPort, "443")
	}
	return hostPort, prefix, nil
}
