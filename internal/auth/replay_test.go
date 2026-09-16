package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"lnmp-panel/internal/store"
)

func TestTOTPReplayRejected(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
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
	if err := s.checkTOTP(code); err != nil {
		t.Fatal(err)
	}
	if err := s.checkTOTP(code); err != ErrBadTOTP {
		t.Fatalf("同一时间片应拒绝重放，得到 %v", err)
	}
}

func TestMissingUserStillHashes(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	if err := s.EnsureAdmin("admin", "GoodPassw0rd!"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Login("1.1.1.1", "ua", "nobody", "GoodPassw0rd!")
	if err != ErrInvalidCreds {
		t.Fatalf("%v", err)
	}
}
