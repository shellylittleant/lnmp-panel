package httpapi

import (
	"errors"
	"net/http"

	"lnmp-panel/internal/firewall"
)

func writeFWErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, firewall.ErrDrift), errors.Is(err, firewall.ErrRawMode), errors.Is(err, firewall.ErrPending):
		writeErr(w, 409, "conflict", err.Error())
	default:
		writeErr(w, 400, "invalid", err.Error())
	}
}

func (s *Server) handleFirewallGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.Firewall.Get()
	if err != nil {
		writeFWErr(w, err)
		return
	}
	writeOK(w, v)
}

func (s *Server) handleFirewallPut(w http.ResponseWriter, r *http.Request) {
	var in firewall.Form
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Firewall.Put(r.Context(), in); err != nil {
		writeFWErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFirewallRawGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.Firewall.Get()
	if err != nil {
		writeFWErr(w, err)
		return
	}
	writeOK(w, map[string]any{"content": v.Content, "drifted": v.Drifted, "raw_mode": v.RawMode, "pending": v.Pending, "confirm_deadline": v.ConfirmDeadline})
}

func (s *Server) handleFirewallRawPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content string `json:"content"`
		Force   bool   `json:"force"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Firewall.PutRaw(r.Context(), in.Content, in.Force); err != nil {
		writeFWErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFirewallApply(w http.ResponseWriter, r *http.Request) {
	if err := s.Firewall.Apply(r.Context()); err != nil {
		writeFWErr(w, err)
		return
	}
	v, _ := s.Firewall.Get()
	writeOK(w, map[string]any{"confirm_deadline": v.ConfirmDeadline, "message": "已加载新规则，请在确认窗口内点确认，否则自动回滚"})
}

func (s *Server) handleFirewallConfirm(w http.ResponseWriter, r *http.Request) {
	if err := s.Firewall.Confirm(r.Context()); err != nil {
		writeFWErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFirewallRollback(w http.ResponseWriter, r *http.Request) {
	if err := s.Firewall.Rollback(r.Context()); err != nil {
		writeFWErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFirewallBan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IP string `json:"ip"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Firewall.Ban(in.IP); err != nil {
		writeFWErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleFirewallUseTpl(w http.ResponseWriter, r *http.Request) {
	if err := s.Firewall.UseTemplate(r.Context()); err != nil {
		writeFWErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}
