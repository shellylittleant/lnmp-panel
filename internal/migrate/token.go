package migrate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func ParseToken(raw string) (prefix, secret string, err error) {
	prefix, secret, ok := strings.Cut(strings.TrimSpace(raw), ".")
	if !ok || len(prefix) != 16 || len(secret) < 16 {
		return "", "", fmt.Errorf("令牌格式无效")
	}
	if _, err := hex.DecodeString(prefix); err != nil {
		return "", "", fmt.Errorf("令牌指纹段无效")
	}
	return prefix, secret, nil
}

func NewToken(fp string) (raw string, err error) {
	if len(fp) < 16 {
		return "", fmt.Errorf("本地证书指纹过短")
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return FingerprintPrefix(fp) + "." + hex.EncodeToString(b), nil
}
