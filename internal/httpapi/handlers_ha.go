package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"lnmp-panel/internal/ha"
)

func (s *Server) handleHAStatus(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	st, err := s.HA.Status()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, st)
}

func (s *Server) handleHAToken(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	out, err := s.HA.Token()
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, out)
}

func (s *Server) handleHAPair(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	var in ha.PairIn
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.HA.Pair(r.Context(), in); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleHAPromote(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	if err := s.HA.Promote(r.Context()); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleHADemote(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	var in struct {
		Peer string `json:"peer"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in)
	if err := s.HA.Demote(r.Context(), in.Peer); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleHASyncStart(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	if err := s.HA.StartSync(r.Context()); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleHASyncStatus(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	st, err := s.HA.SyncStatus()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, st)
}

func (s *Server) handleHAHealth(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	if err := s.haPeerAuth(r); err != nil {
		writeErr(w, 401, "unauthorized", err.Error())
		return
	}
	h, err := s.HA.Health()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, h)
}

func (s *Server) handleHAPeerRole(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	if err := s.haPeerAuth(r); err != nil {
		writeErr(w, 401, "unauthorized", err.Error())
		return
	}
	v, err := s.HA.PeerRole()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, v)
}

func (s *Server) handleHAAccept(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	var in ha.AcceptIn
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	in.From = clientIP(r)
	out, err := s.HA.Accept(in)
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, out)
}

func (s *Server) handleHAPeerBundle(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	if err := s.haPeerAuth(r); err != nil {
		writeErr(w, 401, "unauthorized", err.Error())
		return
	}
	b, err := s.HA.PeerBundle(r.Context())
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, b)
}

func (s *Server) handleHAPeerRepl(w http.ResponseWriter, r *http.Request) {
	if s.HA == nil {
		writeErr(w, 500, "internal", "热备模块未初始化")
		return
	}
	if err := s.haPeerAuth(r); err != nil {
		writeErr(w, 401, "unauthorized", err.Error())
		return
	}
	src, err := s.HA.PeerReplSource()
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, src)
}

func (s *Server) haPeerAuth(r *http.Request) error {
	if s.HA == nil {
		return errHAUninit
	}
	tok := r.Header.Get("X-LNMP-Peer-Token")
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	if tok == "" && r.Body != nil && r.Method != http.MethodGet {
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
		tok = in.Token
	}
	return s.HA.ValidMachineToken(tok)
}

var errHAUninit = errString("热备模块未初始化")

type errString string

func (e errString) Error() string { return string(e) }
