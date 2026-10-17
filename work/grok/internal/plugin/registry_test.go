package plugin

import (
	"testing"
)

func TestRegistryEnableDisable(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)
	p := &stubPlug{id: "stub", page: "/files"}
	Register(p)
	dir := t.TempDir()
	db, err := openTestDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reg := New(db, &Host{WWWRoot: dir, Data: dir, Secrets: dir})
	if err := reg.Sync(); err != nil {
		t.Fatal(err)
	}
	if !reg.Enabled("stub") {
		t.Fatal("默认应启用")
	}
	if p.started < 1 {
		t.Fatal("Sync 应 OnStart")
	}
	is, en := reg.PageState("/files")
	if !is || !en {
		t.Fatalf("页面 %v %v", is, en)
	}
	if err := reg.SetEnabled("stub", false); err != nil {
		t.Fatal(err)
	}
	if reg.Enabled("stub") {
		t.Fatal("应停用")
	}
	is, en = reg.PageState("/files")
	if !is || en {
		t.Fatalf("停用后页面仍应识别但未启用 %v %v", is, en)
	}
	if p.stopped < 1 {
		t.Fatal("应 OnStop")
	}
	infos := reg.ListInfo()
	if len(infos) != 1 || infos[0].Enabled || len(infos[0].Pages) != 0 {
		t.Fatalf("%+v", infos)
	}
}

func TestConfineColonRejected(t *testing.T) {
	if _, err := Confine(t.TempDir(), "C:/windows", nil); err != ErrOutOfRoot {
		t.Fatalf("%v", err)
	}
}
