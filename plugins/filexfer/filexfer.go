//go:build plugin_filexfer || plugin_all

package filexfer

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"lnmp-panel/internal/plugin"
)

func init() {
	plugin.Register(&FX{})
}

type FX struct {
	ctx plugin.Context
}

func (f *FX) ID() string      { return "filexfer" }
func (f *FX) Name() string    { return "文件上传下载" }
func (f *FX) Version() string { return "0.1.0" }
func (f *FX) Hooks() []plugin.Hook {
	return nil
}

func (f *FX) OnStart(ctx plugin.Context) error {
	f.ctx = ctx
	return nil
}

func (f *FX) OnStop(plugin.Context) error {
	return nil
}

func (f *FX) Pages() []plugin.Page {
	return []plugin.Page{{
		MenuParent: "",
		MenuTitle:  "文件上传下载",
		Path:       "/files",
		Schema: plugin.FormSchema{
			Title: "文件上传下载",
			Fields: []plugin.Field{
				{Name: "path", Label: "相对 /var/www 的路径", Type: "text", Required: true},
				{Name: "file", Label: "上传文件", Type: "file", Required: false},
			},
			Actions: []plugin.Action{
				{Name: "list", Label: "列出", Method: "GET", Path: "list"},
				{Name: "upload", Label: "上传", Method: "POST", Path: "upload"},
				{Name: "download", Label: "下载", Method: "GET", Path: "download"},
			},
		},
	}}
}

func (f *FX) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "list", Help: "列出允许根下一层", Handle: f.handleList},
		{Method: "POST", Path: "upload", Help: "上传覆盖", Handle: f.handleUpload},
		{Method: "GET", Path: "download", Help: "下载文件", Handle: f.handleDownload},
	}
}

func (f *FX) root() string {
	if f.ctx != nil && f.ctx.WWW() != "" {
		return f.ctx.WWW()
	}
	return "/var/www"
}

func (f *FX) deny() []string {
	data, sec := "", ""
	if f.ctx != nil {
		data, sec = f.ctx.DataDir(), f.ctx.SecretsDir()
	}
	return plugin.DefaultDeny(data, sec)
}

func (f *FX) resolve(rel string) (string, error) {
	return plugin.Confine(f.root(), rel, f.deny())
}

func writeOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]string{"code": "invalid", "message": msg}})
}

type entry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

func (f *FX) handleList(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	full, err := f.resolve(rel)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	st, err := os.Stat(full)
	if err != nil {
		writeErr(w, 404, "路径不存在")
		return
	}
	if !st.IsDir() {
		writeErr(w, 400, "不是目录")
		return
	}
	ents, err := os.ReadDir(full)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var items []entry
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, entry{Name: e.Name(), Dir: e.IsDir(), Size: info.Size()})
	}
	if items == nil {
		items = []entry{}
	}
	writeOK(w, map[string]any{"root": f.root(), "path": rel, "items": items})
}

const maxUpload = 64 << 20

func (f *FX) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeErr(w, 400, "请用 multipart，字段 file 与 path")
		return
	}
	rel := r.FormValue("path")
	fh, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "缺少 file")
		return
	}
	defer fh.Close()
	if strings.TrimSpace(rel) == "" || strings.HasSuffix(rel, "/") {
		if hdr != nil && hdr.Filename != "" {
			rel = filepath.Join(strings.Trim(rel, "/"), filepath.Base(hdr.Filename))
		}
	}
	full, err := f.resolve(rel)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".lnmp-upload-*")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	tmpName := tmp.Name()
	n, err := io.Copy(tmp, io.LimitReader(fh, maxUpload+1))
	_ = tmp.Close()
	if err != nil || n > maxUpload {
		_ = os.Remove(tmpName)
		writeErr(w, 400, "读取上传失败")
		return
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		_ = os.Remove(tmpName)
		writeErr(w, 400, err.Error())
		return
	}
	if err := os.Rename(tmpName, full); err != nil {
		_ = os.Remove(tmpName)
		writeErr(w, 400, err.Error())
		return
	}
	if err := chownWWWData(full); err != nil {
		writeErr(w, 400, "上传成功但无法改为 www-data："+err.Error())
		return
	}
	writeOK(w, map[string]any{"path": rel, "size": n})
}

func (f *FX) handleDownload(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	full, err := f.resolve(rel)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	st, err := os.Stat(full)
	if err != nil {
		writeErr(w, 404, "文件不存在")
		return
	}
	if st.IsDir() {
		writeErr(w, 400, "不能下载目录")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(full)+`"`)
	http.ServeFile(w, r, full)
}

func chownWWWData(path string) error {
	u, err := user.Lookup("www-data")
	if err != nil {
		return nil
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return nil
	}
	return os.Chown(path, uid, gid)
}
