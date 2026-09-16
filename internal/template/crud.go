package template

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Record struct {
	ID          int64  `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Origin      string `json:"origin"`
	Content     string `json:"content"`
	ContentHash string `json:"content_hash"`
	ParentID    *int64 `json:"parent_id"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (s *Service) Get(id int64) (Record, error) {
	var r Record
	var parent sql.NullInt64
	err := s.db.SQL.QueryRow(`SELECT id, type, name, origin, content, content_hash, parent_id, created_at, updated_at FROM templates WHERE id = ?`, id).
		Scan(&r.ID, &r.Type, &r.Name, &r.Origin, &r.Content, &r.ContentHash, &parent, &r.CreatedAt, &r.UpdatedAt)
	if parent.Valid {
		v := parent.Int64
		r.ParentID = &v
	}
	return r, err
}

func (s *Service) OfficialByName(typ, name string) (Record, error) {
	var id int64
	err := s.db.SQL.QueryRow(`SELECT id FROM templates WHERE origin='official' AND type=? AND name=?`, typ, name).Scan(&id)
	if err != nil {
		return Record{}, err
	}
	return s.Get(id)
}

func (s *Service) List(typ string) ([]Record, error) {
	q := `SELECT id, type, name, origin, content, content_hash, parent_id, created_at, updated_at FROM templates`
	var args []any
	if typ != "" {
		q += ` WHERE type = ?`
		args = append(args, typ)
	}
	q += ` ORDER BY origin, name`
	rows, err := s.db.SQL.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		var parent sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Type, &r.Name, &r.Origin, &r.Content, &r.ContentHash, &parent, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if parent.Valid {
			v := parent.Int64
			r.ParentID = &v
		}
		out = append(out, r)
	}
	if out == nil {
		out = []Record{}
	}
	return out, rows.Err()
}

func (s *Service) EnsureUser(typ, content string, parent *int64) (int64, error) {
	h := Hash(content)
	var id int64
	err := s.db.SQL.QueryRow(`SELECT id FROM templates WHERE origin='user' AND type=? AND content_hash=?`, typ, h).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	now := s.db.Now()
	name := "用户-" + time.Now().UTC().Format("20060102-150405")
	res, err := s.db.SQL.Exec(`INSERT INTO templates(type, name, origin, content, content_hash, parent_id, created_at, updated_at)
		VALUES (?, ?, 'user', ?, ?, ?, ?, ?)`, typ, name, content, h, parent, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Service) CreateUser(typ, name, content string, parent *int64) (int64, error) {
	if name == "" {
		name = "用户-" + time.Now().UTC().Format("20060102-150405")
	}
	h := Hash(content)
	now := s.db.Now()
	res, err := s.db.SQL.Exec(`INSERT INTO templates(type, name, origin, content, content_hash, parent_id, created_at, updated_at)
		VALUES (?, ?, 'user', ?, ?, ?, ?, ?)`, typ, name, content, h, parent, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Service) UpdateUser(id int64, name, content string) error {
	r, err := s.Get(id)
	if err != nil {
		return err
	}
	if r.Origin != "user" {
		return fmt.Errorf("官方模板不可改内容")
	}
	if name == "" {
		name = r.Name
	}
	h := Hash(content)
	_, err = s.db.SQL.Exec(`UPDATE templates SET name=?, content=?, content_hash=?, updated_at=? WHERE id=?`, name, content, h, s.db.Now(), id)
	return err
}

func (s *Service) Delete(id int64) error {
	r, err := s.Get(id)
	if err != nil {
		return err
	}
	if r.Origin != "user" {
		return fmt.Errorf("官方模板不可删除")
	}
	var n int
	if err := s.db.SQL.QueryRow(`SELECT COUNT(1) FROM sites WHERE nginx_template_id=? OR rewrite_template_id=? OR ssl_template_id=?`, id, id, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("模板正被站点引用")
	}
	_, err = s.db.SQL.Exec(`DELETE FROM templates WHERE id=?`, id)
	return err
}

func (s *Service) Export(id int64) ([]byte, error) {
	r, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(map[string]string{"type": r.Type, "name": r.Name, "content": r.Content}, "", "  ")
}

func (s *Service) Import(typ, name, content string) (int64, error) {
	return s.EnsureUser(typ, content, nil)
}
