package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"lnmp-panel/internal/mysql"
)

func writeMySQLErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mysql.ErrMariaDB):
		writeErr(w, 400, "not_supported", err.Error())
	case errors.Is(err, mysql.ErrNotFound), errors.Is(err, mysql.ErrNotInstalled):
		writeErr(w, 404, "not_found", err.Error())
	case errors.Is(err, mysql.ErrDrift), errors.Is(err, mysql.ErrRawMode), errors.Is(err, mysql.ErrDup):
		writeErr(w, 409, "conflict", err.Error())
	default:
		writeErr(w, 400, "invalid", err.Error())
	}
}

func (s *Server) handleMySQLDBs(w http.ResponseWriter, r *http.Request) {
	items, err := s.MySQL.ListDB()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, map[string]any{"items": items})
}

func (s *Server) handleMySQLDBCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		Charset string `json:"charset"`
		Collate string `json:"collate"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	d, err := s.MySQL.CreateDB(r.Context(), in.Name, in.Charset, in.Collate)
	if err != nil {
		writeMySQLErr(w, err)
		return
	}
	writeOK(w, d)
}

func (s *Server) handleMySQLDBDrop(w http.ResponseWriter, r *http.Request) {
	if err := s.MySQL.DropDB(r.Context(), r.PathValue("name")); err != nil {
		writeMySQLErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleMySQLExport(w http.ResponseWriter, r *http.Request) {
	b, err := s.MySQL.Export(r.Context(), r.PathValue("name"))
	if err != nil {
		writeMySQLErr(w, err)
		return
	}
	writeOK(w, map[string]any{"sql": string(b)})
}

func (s *Server) handleMySQLImport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, 400, "invalid", "请用 multipart 上传 SQL 文件，字段名 file")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "invalid", "缺少 file 字段")
		return
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, 64<<20))
	if err != nil {
		writeErr(w, 400, "invalid", "读取上传失败")
		return
	}
	id, err := s.MySQL.StartImport(r.Context(), name, body)
	if err != nil {
		writeMySQLErr(w, err)
		return
	}
	writeOK(w, map[string]any{"job_id": id})
}

func (s *Server) handleMySQLUsers(w http.ResponseWriter, r *http.Request) {
	items, err := s.MySQL.ListUsers()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, map[string]any{"items": items})
}

func (s *Server) handleMySQLUserCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		User      string   `json:"user"`
		Host      string   `json:"host"`
		Password  string   `json:"password"`
		Databases []string `json:"databases"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	u, err := s.MySQL.CreateUser(r.Context(), in.User, in.Host, in.Password, in.Databases)
	if err != nil {
		writeMySQLErr(w, err)
		return
	}
	writeOK(w, u)
}

func (s *Server) handleMySQLUserPass(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.MySQL.SetPassword(r.Context(), id, in.Password); err != nil {
		writeMySQLErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleMySQLUserDel(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.MySQL.DeleteUser(r.Context(), id); err != nil {
		writeMySQLErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleMySQLCNFGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.MySQL.GetCNF()
	if err != nil {
		writeMySQLErr(w, err)
		return
	}
	writeOK(w, v)
}

func (s *Server) handleMySQLCNFPut(w http.ResponseWriter, r *http.Request) {
	var wrap struct {
		mysql.CNFVars
		Force bool `json:"force"`
	}
	if err := readJSON(w, r, &wrap); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.MySQL.PutCNF(r.Context(), wrap.CNFVars, wrap.Force); err != nil {
		writeMySQLErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleMySQLCNFRaw(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content string `json:"content"`
		Force   bool   `json:"force"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.MySQL.PutCNFRaw(r.Context(), in.Content, in.Force); err != nil {
		writeMySQLErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleMySQLCNFUseTpl(w http.ResponseWriter, r *http.Request) {
	if err := s.MySQL.UseCNFTemplate(r.Context()); err != nil {
		writeMySQLErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}
