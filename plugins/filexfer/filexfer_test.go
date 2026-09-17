//go:build plugin_filexfer || plugin_all

package filexfer

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lnmp-panel/internal/plugin"
)

type testHost struct {
	www, data, sec string
}

func (h testHost) WWW() string                     { return h.www }
func (h testHost) DataDir() string                 { return h.data }
func (h testHost) SecretsDir() string              { return h.sec }
func (h testHost) Setting(string) string           { return "" }
func (h testHost) SetSetting(string, string) error { return nil }
func (h testHost) Config(string) string            { return "{}" }
func (h testHost) SetConfig(string, string) error  { return nil }

func setupFX(t *testing.T) (*FX, string) {
	t.Helper()
	root := t.TempDir()
	www := filepath.Join(root, "www")
	_ = os.MkdirAll(filepath.Join(www, "demo"), 0755)
	_ = os.WriteFile(filepath.Join(www, "demo", "a.txt"), []byte("hello"), 0644)
	fx := &FX{}
	if err := fx.OnStart(testHost{www: www, data: filepath.Join(root, "data"), sec: filepath.Join(root, "sec")}); err != nil {
		t.Fatal(err)
	}
	return fx, www
}

func TestFilexferListAndDownload(t *testing.T) {
	fx, _ := setupFX(t)
	req := httptest.NewRequest("GET", "/?path=demo", nil)
	rr := httptest.NewRecorder()
	fx.handleList(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "a.txt") {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest("GET", "/?path=demo/a.txt", nil)
	rr = httptest.NewRecorder()
	fx.handleDownload(rr, req)
	if rr.Code != 200 || rr.Body.String() != "hello" {
		t.Fatalf("download %d %s", rr.Code, rr.Body.String())
	}
}

func TestFilexferUploadAndTraversal(t *testing.T) {
	fx, www := setupFX(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("path", "demo/b.txt")
	fw, _ := mw.CreateFormFile("file", "b.txt")
	_, _ = fw.Write([]byte("world"))
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	fx.handleUpload(rr, req)
	if rr.Code != 200 {
		t.Fatalf("upload %d %s", rr.Code, rr.Body.String())
	}
	b, err := os.ReadFile(filepath.Join(www, "demo", "b.txt"))
	if err != nil || string(b) != "world" {
		t.Fatalf("file %s %v", b, err)
	}

	buf.Reset()
	mw = multipart.NewWriter(&buf)
	_ = mw.WriteField("path", "../etc/passwd")
	fw, _ = mw.CreateFormFile("file", "x")
	_, _ = fw.Write([]byte("no"))
	_ = mw.Close()
	req = httptest.NewRequest("POST", "/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr = httptest.NewRecorder()
	fx.handleUpload(rr, req)
	if rr.Code != 400 || !strings.Contains(rr.Body.String(), "穿越") {
		t.Fatalf("穿越 %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/?path=/etc/passwd", nil)
	rr = httptest.NewRecorder()
	fx.handleDownload(rr, req)
	if rr.Code != 400 {
		t.Fatalf("越界下载 %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/?path=../data/panel.db", nil)
	rr = httptest.NewRecorder()
	fx.handleDownload(rr, req)
	if rr.Code != 400 {
		t.Fatalf("数据库 %d %s", rr.Code, rr.Body.String())
	}
}

func TestFilexferID(t *testing.T) {
	fx := &FX{}
	if fx.ID() != "filexfer" || fx.Pages()[0].Path != "/files" {
		t.Fatal("元数据")
	}
	_ = http.StatusOK
	_ = plugin.ErrTraverse
}
