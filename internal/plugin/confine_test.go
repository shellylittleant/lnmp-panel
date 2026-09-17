package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfineRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	deny := DefaultDeny(filepath.Join(root, "data"), filepath.Join(root, "sec"))
	for _, rel := range []string{"../etc/passwd", "a/../../x", "..", "foo/../bar/../../z"} {
		if _, err := Confine(root, rel, deny); err != ErrTraverse {
			t.Fatalf("%q 应拒绝穿越，got %v", rel, err)
		}
	}
}

func TestConfineRejectsAbsAndOutOfRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := Confine(root, "/etc/passwd", nil); err != ErrOutOfRoot {
		t.Fatalf("绝对路径：%v", err)
	}
	if _, err := Confine(root, "~/.ssh/id_rsa", nil); err != ErrOutOfRoot {
		t.Fatalf("家目录：%v", err)
	}
}

func TestConfineRejectsPanelSecretsAndDB(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	sec := filepath.Join(root, "sec")
	_ = os.MkdirAll(data, 0700)
	_ = os.MkdirAll(sec, 0700)
	_ = os.WriteFile(filepath.Join(data, "panel.db"), []byte("x"), 0600)
	deny := DefaultDeny(data, sec)
	if _, err := Confine(root, "data/panel.db", deny); err != ErrForbidden {
		t.Fatalf("数据库：%v", err)
	}
	if _, err := Confine(root, "sec/ha-key", deny); err != ErrForbidden {
		t.Fatalf("密钥：%v", err)
	}
}

func TestConfineAllowsUnderWWW(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "demo"), 0755)
	got, err := Confine(root, "demo/index.html", DefaultDeny("/var/lib/lnmp-panel", "/etc/lnmp-panel"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(got)) != "demo" || filepath.Base(got) != "index.html" {
		t.Fatalf("got %s", got)
	}
}

func TestRunHookRecoversPanic(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)
	Register(&panicPlug{})
	dir := t.TempDir()
	db, err := openTestDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reg := New(db, &Host{WWWRoot: dir})
	if err := reg.Sync(); err != nil {
		t.Fatal(err)
	}
	out := reg.RunHook("menu.register", Event{Name: "menu.register"})
	if len(out) != 1 || out[0].Err == "" {
		t.Fatalf("%+v", out)
	}
}
