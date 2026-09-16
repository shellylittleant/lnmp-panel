package fs

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func umask(n int) int { return syscall.Umask(n) }

func TestWriteAtomicIgnoresUmask(t *testing.T) {
	dir := t.TempDir()
	old := umask(0077)
	defer umask(old)
	p := filepath.Join(dir, "a.conf")
	if err := WriteAtomic(p, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0644 {
		t.Fatalf("perm=%o", st.Mode().Perm())
	}
}

func TestMkdirIgnoresUmask(t *testing.T) {
	dir := t.TempDir()
	old := umask(0077)
	defer umask(old)
	p := filepath.Join(dir, "sub")
	if err := Mkdir(p, 0755); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0755 {
		t.Fatalf("perm=%o", st.Mode().Perm())
	}
}
