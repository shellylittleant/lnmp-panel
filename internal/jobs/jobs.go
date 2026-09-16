package jobs

import (
	"database/sql"
	"errors"

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
