package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

func HashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

func CheckPassword(plain, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false
	}
	var mem, t, p uint32
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(plain), salt, t, mem, uint8(p), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// CheckPolicy：≥12 位，含大小写、数字、符号。
func CheckPolicy(plain string) error {
	if len([]rune(plain)) < 12 {
		return fmt.Errorf("密码至少 12 位")
	}
	var upper, lower, digit, symbol bool
	for _, r := range plain {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			symbol = true
		}
	}
	if !upper || !lower || !digit || !symbol {
		return fmt.Errorf("密码须同时包含大写字母、小写字母、数字和符号")
	}
	return nil
}

func RandomPassword() (string, error) {
	const (
		up = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		lo = "abcdefghijkmnopqrstuvwxyz"
		di = "23456789"
		sy = "!@#$%^&*-_=+"
	)
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	sets := []string{up, lo, di, sy}
	out := make([]byte, 16)
	for i := 0; i < 16; i++ {
		s := sets[i%4]
		out[i] = s[int(b[i])%len(s)]
	}
	// 打乱，避免固定类别顺序
	perm := make([]byte, 16)
	if _, err := rand.Read(perm); err != nil {
		return "", err
	}
	for i := 15; i > 0; i-- {
		j := int(perm[i]) % (i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return string(out), nil
}
