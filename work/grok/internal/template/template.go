package template

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"path"
	"strings"

	"lnmp-panel/internal/store"
)

//go:embed official/*
var officialFS embed.FS

type Service struct {
	db *store.DB
}

func New(db *store.DB) *Service {
	return &Service{db: db}
}

func Hash(content string) string {
	n := strings.ReplaceAll(content, "\r\n", "\n")
	sum := sha256.Sum256([]byte(n))
	return hex.EncodeToString(sum[:])
}

// SyncOfficial 按 (type, name) 原地更新官方模板。
func (s *Service) SyncOfficial() error {
	return fs.WalkDir(officialFS, "official", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".") || strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		// 文件名：<type>__<name>.tmpl  例如 nginx_site__PHP站点.tmpl
		base := strings.TrimSuffix(d.Name(), path.Ext(d.Name()))
		typ, name, ok := strings.Cut(base, "__")
		if !ok {
			return nil
		}
		b, err := officialFS.ReadFile(p)
		if err != nil {
			return err
		}
		content := string(b)
		h := Hash(content)
		now := s.db.Now()
		var id int64
		err = s.db.SQL.QueryRow(`SELECT id FROM templates WHERE origin = 'official' AND type = ? AND name = ?`, typ, name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = s.db.SQL.Exec(`INSERT INTO templates(type, name, origin, content, content_hash, created_at, updated_at)
				VALUES (?, ?, 'official', ?, ?, ?, ?)`, typ, name, content, h, now, now)
			return err
		}
		if err != nil {
			return err
		}
		_, err = s.db.SQL.Exec(`UPDATE templates SET content = ?, content_hash = ?, updated_at = ? WHERE id = ?`, content, h, now, id)
		return err
	})
}
