package scheduler

import (
	"context"
	"log"
	"time"

	"lnmp-panel/internal/auth"
)

type Scheduler struct {
	auth *auth.Service
}

func New(a *auth.Service) *Scheduler {
	return &Scheduler{auth: a}
}

func (s *Scheduler) Run(ctx context.Context) {
	sessionTick := time.NewTicker(time.Hour)
	attemptTick := time.NewTicker(24 * time.Hour)
	defer sessionTick.Stop()
	defer attemptTick.Stop()
	s.purgeSessions()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sessionTick.C:
			s.purgeSessions()
		case <-attemptTick.C:
			s.purgeAttempts()
		}
	}
}

func (s *Scheduler) purgeSessions() {
	if err := s.auth.PurgeSessions(); err != nil {
		log.Printf("清理过期会话失败：%v", err)
	}
}

func (s *Scheduler) purgeAttempts() {
	if err := s.auth.PurgeAttempts(); err != nil {
		log.Printf("清理登录尝试失败：%v", err)
	}
}
