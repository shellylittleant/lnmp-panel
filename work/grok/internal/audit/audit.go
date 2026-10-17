package audit

import (
	"lnmp-panel/internal/store"
)

type Log struct {
	ID        int64  `json:"id"`
	At        string `json:"at"`
	IP        string `json:"ip"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	Detail    string `json:"detail"`
	RequestID string `json:"request_id"`
}

type Service struct {
	db *store.DB
}

func New(db *store.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Append(ip, actor, action, resource, detail, requestID string) error {
	_, err := s.db.SQL.Exec(
		`INSERT INTO audit_logs(at, ip, actor, action, resource, detail, request_id) VALUES (?,?,?,?,?,?,?)`,
		s.db.Now(), ip, actor, action, resource, detail, requestID,
	)
	return err
}

func (s *Service) Query(from, to, action, ip string, limit int) ([]Log, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, at, ip, actor, action, resource, detail, request_id FROM audit_logs WHERE 1=1`
	var args []any
	if from != "" {
		q += ` AND at >= ?`
		args = append(args, from)
	}
	if to != "" {
		q += ` AND at <= ?`
		args = append(args, to)
	}
	if action != "" {
		q += ` AND action = ?`
		args = append(args, action)
	}
	if ip != "" {
		q += ` AND ip = ?`
		args = append(args, ip)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.SQL.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Log
	for rows.Next() {
		var l Log
		if err := rows.Scan(&l.ID, &l.At, &l.IP, &l.Actor, &l.Action, &l.Resource, &l.Detail, &l.RequestID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if out == nil {
		out = []Log{}
	}
	return out, rows.Err()
}
