package files

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	s := New()
	s.DataDir = filepath.Join(root, "data")
	s.Secrets = filepath.Join(s.DataDir, "secrets")
	s.EtcDir = filepath.Join(root, "etc")
	s.WWW = filepath.Join(root, "www")
	_ = os.MkdirAll(s.Secrets, 0700)
	_ = os.MkdirAll(s.EtcDir, 0700)
	_ = os.MkdirAll(filepath.Join(s.WWW, "demo"), 0755)
	_ = os.WriteFile(filepath.Join(s.WWW, "demo", "a.txt"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(s.DataDir, "panel.db"), []byte("db"), 0600)
	_ = os.WriteFile(filepath.Join(s.Secrets, "ha-key"), []byte("k"), 0600)
	_ = os.WriteFile(filepath.Join(s.EtcDir, "panel.json"), []byte("{}"), 0600)
	return s, root
}

func TestForbiddenSecretsAndDB(t *testing.T) {
	s, _ := setup(t)
	if _, err := s.Resolve(s.Secrets); err != ErrForbidden {
		t.Fatalf("secrets: %v", err)
	}
	if _, err := s.Resolve(filepath.Join(s.Secrets, "ha-key")); err != ErrForbidden {
		t.Fatalf("key: %v", err)
	}
	if _, err := s.Resolve(filepath.Join(s.DataDir, "panel.db")); err != ErrForbidden {
		t.Fatalf("db: %v", err)
	}
	if _, err := s.Resolve(filepath.Join(s.EtcDir, "panel.json")); err != ErrForbidden {
		t.Fatalf("etc: %v", err)
	}
	if _, err := s.Resolve(filepath.Join(s.WWW, "demo", "a.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestListFilterAndOps(t *testing.T) {
	s, _ := setup(t)
	items, abs, err := s.List(filepath.Join(s.WWW, "demo"), "a.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(abs, "demo") || len(items) != 1 || items[0].Name != "a.txt" {
		t.Fatalf("%s %+v", abs, items)
	}
	if err := s.Mkdir(filepath.Join(s.WWW, "demo", "sub")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFile(filepath.Join(s.WWW, "demo", "b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(filepath.Join(s.WWW, "demo", "b.txt"), filepath.Join(s.WWW, "demo", "c.txt"), false); err != nil {
		t.Fatal(err)
	}
	if err := s.Copy(filepath.Join(s.WWW, "demo", "a.txt"), filepath.Join(s.WWW, "demo", "d.txt"), false); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(filepath.Join(s.WWW, "demo", "c.txt")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("/"); err != ErrRoot {
		t.Fatalf("delete / : %v", err)
	}
}

func TestOverwriteNeedsConfirm(t *testing.T) {
	s, _ := setup(t)
	src := filepath.Join(s.WWW, "demo", "a.txt")
	dst := filepath.Join(s.WWW, "demo", "x.txt")
	_ = os.WriteFile(dst, []byte("old"), 0644)
	if err := s.Copy(src, dst, false); err != ErrExists {
		t.Fatalf("copy: %v", err)
	}
	if err := s.Copy(src, dst, true); err != nil {
		t.Fatal(err)
	}
}

func TestEditLimitAndBinary(t *testing.T) {
	s, _ := setup(t)
	p := filepath.Join(s.WWW, "demo", "a.txt")
	txt, err := s.ReadText(p)
	if err != nil || txt != "hello" {
		t.Fatalf("%q %v", txt, err)
	}
	if err := s.WriteText(p, "world"); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(s.WWW, "demo", "big.txt")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), MaxEdit+1), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadText(big); err != ErrTooLarge {
		t.Fatalf("big: %v", err)
	}
	bin := filepath.Join(s.WWW, "demo", "a.bin")
	if err := os.WriteFile(bin, []byte{0, 1, 2}, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadText(bin); err != ErrBinary {
		t.Fatalf("bin: %v", err)
	}
}

func TestZipSlipRejected(t *testing.T) {
	s, _ := setup(t)
	zpath := filepath.Join(s.WWW, "evil.zip")
	f, err := os.Create(zpath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../outside.txt")
	_, _ = w.Write([]byte("no"))
	_ = zw.Close()
	_ = f.Close()
	if err := s.Unzip(zpath, s.WWW, true); err != ErrZipSlip {
		t.Fatalf("zip-slip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.WWW), "outside.txt")); err == nil {
		t.Fatal("zip-slip 写出了根外文件")
	}
}

func TestZipAndUnzipRoundtrip(t *testing.T) {
	s, _ := setup(t)
	out := filepath.Join(s.WWW, "pack.zip")
	if err := s.Compress([]string{filepath.Join(s.WWW, "demo")}, out); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(s.WWW, "out")
	_ = os.MkdirAll(dest, 0755)
	if err := s.Unzip(out, dest, true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "demo", "a.txt"))
	if err != nil || string(b) != "hello" {
		t.Fatalf("%s %v", b, err)
	}
}

func TestUploadStream(t *testing.T) {
	s, _ := setup(t)
	n, err := s.SaveUpload(filepath.Join(s.WWW, "demo", "up.txt"), strings.NewReader("abc"))
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	b, _ := os.ReadFile(filepath.Join(s.WWW, "demo", "up.txt"))
	if string(b) != "abc" {
		t.Fatal(string(b))
	}
}
