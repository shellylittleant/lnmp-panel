package jobs

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"sync"

	"lnmp-panel/internal/store"
)

type Job struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Phase      string  `json:"phase"`
	Progress   int     `json:"progress"`
	Detail     string  `json:"detail"`
	StartedAt  string  `json:"started_at"`
	FinishedAt *string `json:"finished_at"`
}

type Service struct {
	db *store.DB
	mu sync.Mutex
}

func New(db *store.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Get(id string) (Job, error) {
	var j Job
	var finished sql.NullString
	err := s.db.SQL.QueryRow(`SELECT id, kind, phase, progress, detail, started_at, finished_at FROM jobs WHERE id = ?`, id).
		Scan(&j.ID, &j.Kind, &j.Phase, &j.Progress, &j.Detail, &j.StartedAt, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, err
	}
	if finished.Valid {
		j.FinishedAt = &finished.String
	}
	return j, err
}

func (s *Service) Start(kind string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	_ = s.db.SQL.QueryRow(`SELECT COUNT(1) FROM jobs WHERE kind=? AND finished_at IS NULL`, kind).Scan(&n)
	if n > 0 {
		return "", errors.New("已有同类任务在执行")
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	_, err := s.db.SQL.Exec(`INSERT INTO jobs(id, kind, phase, progress, detail, started_at) VALUES (?, ?, 'queued', 0, '', ?)`,
		id, kind, s.db.Now())
	return id, err
}

func (s *Service) Update(id, phase, detail string, progress int) {
	_, _ = s.db.SQL.Exec(`UPDATE jobs SET phase=?, detail=?, progress=? WHERE id=?`, phase, detail, progress, id)
}

func (s *Service) AbandonUnfinished() error {
	_, err := s.db.SQL.Exec(`UPDATE jobs SET phase='failed', detail=?, progress=100, finished_at=? WHERE finished_at IS NULL`, "面板重启，结果未知", s.db.Now())
	return err
}

func (s *Service) Finish(id string, err error) {
	detail := "完成"
	phase := "done"
	if err != nil {
		detail = err.Error()
		phase = "failed"
	}
	now := s.db.Now()
	_, _ = s.db.SQL.Exec(`UPDATE jobs SET phase=?, detail=?, progress=100, finished_at=? WHERE id=?`, phase, detail, now, id)
}
