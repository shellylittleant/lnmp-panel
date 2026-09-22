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
	_ = os.MkdirAll(filepath.Join(s.DataDir, "tls"), 0700)
	s.PanelKey = filepath.Join(s.DataDir, "tls", "panel.key")
	_ = os.WriteFile(s.PanelKey, []byte("KEY"), 0600)
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
	if _, err := s.Resolve(s.PanelKey); err != ErrForbidden {
		t.Fatalf("panel.key: %v", err)
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
	if err := s.WriteText(p, string(bytes.Repeat([]byte("x"), MaxEdit+1))); err != ErrTooLarge {
		t.Fatalf("写入超限：%v", err)
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
	if err := s.Compress([]string{filepath.Join(s.WWW, "demo")}, out, false); err != nil {
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

func TestParentDirCannotBypassProtected(t *testing.T) {
	s, _ := setup(t)
	dest := filepath.Join(s.WWW, "copy")
	if err := s.Copy(s.DataDir, dest, false); err != ErrContains {
		t.Fatalf("复制父目录：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "secrets", "ha-key")); err == nil {
		t.Fatal("复制不得带出 secrets")
	}
	if _, err := os.Stat(filepath.Join(dest, "panel.db")); err == nil {
		t.Fatal("复制不得带出 panel.db")
	}
	moved := filepath.Join(s.WWW, "moved")
	if err := s.Rename(s.DataDir, moved, false); err != ErrContains {
		t.Fatalf("移动父目录：%v", err)
	}
	if _, err := os.Stat(s.Secrets); err != nil {
		t.Fatal("移动失败后密钥目录应仍在原处")
	}
	if err := s.Delete(s.DataDir); err != ErrContains {
		t.Fatalf("删除父目录：%v", err)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, "panel.db")); err != nil {
		t.Fatal("删除失败后面板数据库应仍在")
	}
}

func TestCopyIntoSelfRejected(t *testing.T) {
	s, _ := setup(t)
	src := filepath.Join(s.WWW, "demo")
	if err := s.Copy(src, filepath.Join(src, "nested"), false); err != ErrIntoSelf {
		t.Fatalf("复制到子目录：%v", err)
	}
	if err := s.Rename(src, filepath.Join(src, "nested"), false); err != ErrIntoSelf {
		t.Fatalf("移动到子目录：%v", err)
	}
}

func TestCompressParentRejected(t *testing.T) {
	s, _ := setup(t)
	dest := filepath.Join(s.WWW, "all.zip")
	if err := s.Compress([]string{s.DataDir}, dest, false); err != ErrContains {
		t.Fatalf("压缩父目录：%v", err)
	}
}

func TestZipParentAndProtectedRejected(t *testing.T) {
	s, _ := setup(t)
	buf := &bytes.Buffer{}
	if err := s.ZipTo(buf, []string{s.DataDir}); err != ErrContains {
		t.Fatalf("打包父目录：%v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("父目录打包不得写出内容：%d", buf.Len())
	}
	buf.Reset()
	if err := s.ZipTo(buf, []string{filepath.Join(s.DataDir, "panel.db")}); err != ErrForbidden {
		t.Fatalf("打包 panel.db：%v", err)
	}
	buf.Reset()
	if err := s.ZipTo(buf, []string{s.Secrets}); err != ErrForbidden {
		t.Fatalf("打包 secrets：%v", err)
	}
	buf.Reset()
	if err := s.ZipTo(buf, []string{s.PanelKey}); err != ErrForbidden {
		t.Fatalf("打包 panel.key：%v", err)
	}
	if buf.Len() != 0 {
		t.Fatal("例外路径打包不得写出 zip")
	}
	buf.Reset()
	if err := s.ZipTo(buf, []string{filepath.Join(s.WWW, "demo")}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("普通目录应打出 zip")
	}
}

func TestListMarksProtectedLocked(t *testing.T) {
	s, _ := setup(t)
	items, _, err := s.List(s.DataDir, "")
	if err != nil {
		t.Fatal(err)
	}
	var sawDB, sawSec bool
	for _, it := range items {
		switch it.Name {
		case "panel.db":
			sawDB = true
			if !it.Locked {
				t.Fatal("panel.db 应加锁")
			}
		case "secrets":
			sawSec = true
			if !it.Locked {
				t.Fatal("secrets 应加锁")
			}
		}
	}
	if !sawDB || !sawSec {
		t.Fatalf("列目录应仍显示例外名：db=%v secrets=%v items=%v", sawDB, sawSec, items)
	}
	tlsItems, _, err := s.List(filepath.Join(s.DataDir, "tls"), "")
	if err != nil {
		t.Fatal(err)
	}
	var sawKey bool
	for _, it := range tlsItems {
		if it.Name == "panel.key" {
			sawKey = true
			if !it.Locked {
				t.Fatal("panel.key 应加锁")
			}
		}
	}
	if !sawKey {
		t.Fatal("tls 下列表应显示 panel.key")
	}
	wwwItems, _, err := s.List(filepath.Join(s.WWW, "demo"), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range wwwItems {
		if it.Locked {
			t.Fatalf("站点文件不应加锁：%+v", it)
		}
	}
}

func TestUploadDestFileVsDir(t *testing.T) {
	s, _ := setup(t)
	dir := filepath.Join(s.WWW, "demo")
	existing := filepath.Join(dir, "a.txt")
	got, err := s.ResolveUploadDest(dir, "up.bin")
	if err != nil || got != filepath.Join(dir, "up.bin") {
		t.Fatalf("目录目标：%s %v", got, err)
	}
	got, err = s.ResolveUploadDest(existing, "other.bin")
	if err != nil || got != existing {
		t.Fatalf("已存在文件：%s %v", got, err)
	}
	asFile, err := s.UploadPathIsFile(existing)
	if err != nil || !asFile {
		t.Fatalf("已存在文件应作目标文件：%v %v", asFile, err)
	}
	missingFile := filepath.Join(dir, "new.txt")
	got, err = s.ResolveUploadDest(missingFile, "x.bin")
	if err != nil || got != missingFile {
		t.Fatalf("不存在但带扩展名：%s %v", got, err)
	}
	missingDir := filepath.Join(dir, "newdir")
	got, err = s.ResolveUploadDest(missingDir, "x.bin")
	if err != nil || got != filepath.Join(missingDir, "x.bin") {
		t.Fatalf("不存在无扩展名按目录：%s %v", got, err)
	}
	if as, _ := s.UploadPathIsFile(missingDir); as {
		t.Fatal("无扩展名不应当文件")
	}
}

func TestUnzipLeavesExistingMode(t *testing.T) {
	s, _ := setup(t)
	keep := filepath.Join(s.WWW, "keep.txt")
	if err := os.WriteFile(keep, []byte("stay"), 0600); err != nil {
		t.Fatal(err)
	}
	zpath := filepath.Join(s.WWW, "one.zip")
	f, err := os.Create(zpath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("fresh.txt")
	_, _ = w.Write([]byte("new"))
	_ = zw.Close()
	_ = f.Close()
	if err := s.Unzip(zpath, s.WWW, true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(keep)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("原有文件权限被改成 %o", info.Mode().Perm())
	}
	if b, _ := os.ReadFile(filepath.Join(s.WWW, "fresh.txt")); string(b) != "new" {
		t.Fatal(string(b))
	}
}

func TestChmodProtectedParentRejected(t *testing.T) {
	s, _ := setup(t)
	tls := filepath.Join(s.DataDir, "tls")
	if err := s.Chmod(tls, "", "", 0777, false); err != ErrContains {
		t.Fatalf("非递归 chmod 父目录：%v", err)
	}
	info, err := os.Stat(tls)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() == 0777 {
		t.Fatal("目录被改成 0777")
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
