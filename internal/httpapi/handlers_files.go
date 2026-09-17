package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lnmp-panel/internal/files"
)

func fileErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrForbidden):
		writeErr(w, 403, "forbidden", err.Error())
	case errors.Is(err, files.ErrNotFound):
		writeErr(w, 404, "not_found", err.Error())
	case errors.Is(err, files.ErrExists):
		writeErr(w, 409, "conflict", err.Error())
	case errors.Is(err, files.ErrRoot), errors.Is(err, files.ErrTooLarge), errors.Is(err, files.ErrUploadTooLarge),
		errors.Is(err, files.ErrBinary), errors.Is(err, files.ErrZipSlip), errors.Is(err, files.ErrNotDir), errors.Is(err, files.ErrIsDir),
		errors.Is(err, files.ErrContains), errors.Is(err, files.ErrIntoSelf):
		writeErr(w, 400, "invalid", err.Error())
	default:
		writeErr(w, 400, "invalid", err.Error())
	}
}

func (s *Server) filesSvc() *files.Service {
	if s.Files != nil {
		return s.Files
	}
	return files.New()
}

func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	q := r.URL.Query().Get("q")
	items, abs, err := s.filesSvc().List(path, q)
	if err != nil {
		fileErr(w, err)
		return
	}
	writeOK(w, map[string]any{"path": abs, "items": items})
}

func (s *Server) handleFilesMkdir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.filesSvc().Mkdir(in.Path); err != nil {
		fileErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFilesCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.filesSvc().CreateFile(in.Path); err != nil {
		fileErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFilesDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path    string   `json:"path"`
		Paths   []string `json:"paths"`
		Confirm bool     `json:"confirm"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if !in.Confirm {
		writeErr(w, 400, "invalid", "删除需要确认")
		return
	}
	ps := in.Paths
	if in.Path != "" {
		ps = append(ps, in.Path)
	}
	for _, p := range ps {
		if err := s.filesSvc().Delete(p); err != nil {
			fileErr(w, err)
			return
		}
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFilesCopy(w http.ResponseWriter, r *http.Request) {
	s.filesCopyMove(w, r, true)
}

func (s *Server) handleFilesMove(w http.ResponseWriter, r *http.Request) {
	s.filesCopyMove(w, r, false)
}

func (s *Server) filesCopyMove(w http.ResponseWriter, r *http.Request, copy bool) {
	var in struct {
		Src     string `json:"src"`
		Dest    string `json:"dest"`
		Confirm bool   `json:"confirm"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	var err error
	if copy {
		err = s.filesSvc().Copy(in.Src, in.Dest, in.Confirm)
	} else {
		err = s.filesSvc().Rename(in.Src, in.Dest, in.Confirm)
	}
	if err != nil {
		fileErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFilesRename(w http.ResponseWriter, r *http.Request) {
	s.filesCopyMove(w, r, false)
}

func (s *Server) handleFilesUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, files.MaxUpload+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, 400, "invalid", "请用 multipart 上传")
		return
	}
	dir := r.FormValue("path")
	confirm := r.FormValue("confirm") == "true" || r.FormValue("confirm") == "1"
	form := r.MultipartForm
	if form == nil {
		writeErr(w, 400, "invalid", "缺少文件")
		return
	}
	var n int
	for _, fhs := range form.File {
		for _, fh := range fhs {
			dest := filepath.Join(dir, filepath.Base(fh.Filename))
			abs, err := s.filesSvc().Resolve(dest)
			if err != nil {
				fileErr(w, err)
				return
			}
			if _, err := os.Lstat(abs); err == nil && !confirm {
				writeErr(w, 409, "conflict", files.ErrExists.Error())
				return
			}
			src, err := fh.Open()
			if err != nil {
				writeErr(w, 400, "invalid", "读取上传失败")
				return
			}
			_, err = s.filesSvc().SaveUpload(dest, src)
			_ = src.Close()
			if err != nil {
				fileErr(w, err)
				return
			}
			n++
		}
	}
	if n == 0 {
		writeErr(w, 400, "invalid", "缺少文件")
		return
	}
	writeOK(w, map[string]any{"count": n})
}

func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	f, st, err := s.filesSvc().OpenFile(p)
	if err != nil {
		fileErr(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+st.Name()+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	_, _ = io.Copy(w, f)
}

func (s *Server) handleFilesZip(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	paths := r.URL.Query()["paths"]
	if p != "" {
		paths = append(paths, p)
	}
	if len(paths) == 0 {
		writeErr(w, 400, "invalid", "缺少 path")
		return
	}
	name := "download.zip"
	if len(paths) == 1 {
		name = filepath.Base(paths[0]) + ".zip"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	if err := s.filesSvc().ZipTo(w, paths); err != nil {
		return
	}
}

func (s *Server) handleFilesCompress(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paths []string `json:"paths"`
		Dest  string   `json:"dest"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.filesSvc().Compress(in.Paths, in.Dest); err != nil {
		fileErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFilesUnzip(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path    string `json:"path"`
		Dest    string `json:"dest"`
		Confirm bool   `json:"confirm"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.filesSvc().Unzip(in.Path, in.Dest, in.Confirm); err != nil {
		fileErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFilesRead(w http.ResponseWriter, r *http.Request) {
	txt, err := s.filesSvc().ReadText(r.URL.Query().Get("path"))
	if err != nil {
		fileErr(w, err)
		return
	}
	writeOK(w, map[string]any{"content": txt})
}

func (s *Server) handleFilesWrite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.filesSvc().WriteText(in.Path, in.Content); err != nil {
		fileErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFilesChmod(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path      string `json:"path"`
		Mode      string `json:"mode"`
		Owner     string `json:"owner"`
		Group     string `json:"group"`
		Recursive bool   `json:"recursive"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	var mode os.FileMode
	if strings.TrimSpace(in.Mode) != "" {
		v, err := strconv.ParseUint(strings.TrimSpace(in.Mode), 8, 32)
		if err != nil {
			writeErr(w, 400, "invalid", "权限格式无效")
			return
		}
		mode = os.FileMode(v)
	}
	if err := s.filesSvc().Chmod(in.Path, in.Owner, in.Group, mode, in.Recursive); err != nil {
		fileErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFilesUsers(w http.ResponseWriter, r *http.Request) {
	writeOK(w, map[string]any{"users": s.filesSvc().Users(), "groups": s.filesSvc().Groups()})
}

func (s *Server) handleFilesShortcuts(w http.ResponseWriter, r *http.Request) {
	var items []map[string]any
	if s.Sites != nil {
		list, err := s.Sites.List()
		if err == nil {
			for _, it := range list {
				items = append(items, map[string]any{"id": it.ID, "name": it.Name, "root": it.Root})
			}
		}
	}
	if items == nil {
		items = []map[string]any{}
	}
	writeOK(w, map[string]any{"items": items, "default": s.filesSvc().WWW})
}
