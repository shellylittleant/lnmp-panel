package auth

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"lnmp-panel/internal/store"
)

func TestSetupTOTPRejectedWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	if err := s.EnsureAdmin("admin", "GoodPassw0rd!"); err != nil {
		t.Fatal(err)
	}
	st, err := s.SetupTOTP()
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(st.Secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnableTOTP(code); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetupTOTP(); !errors.Is(err, ErrTOTPActive) {
		t.Fatalf("已开启时应拒绝 setup，得到 %v", err)
	}
	on, err := s.TOTPEnabled()
	if err != nil || !on {
		t.Fatal("setup 不得静默关闭 TOTP")
	}
}
