package scheduler

import (
	"context"
	"log"
	"time"

	"lnmp-panel/internal/auth"
)

type Renewer interface {
	RenewDue(ctx context.Context) error
}

type Scheduler struct {
	auth  *auth.Service
	renew Renewer
}

func New(a *auth.Service) *Scheduler {
	return &Scheduler{auth: a}
}

func (s *Scheduler) SetRenewer(r Renewer) {
	s.renew = r
}

func (s *Scheduler) Run(ctx context.Context) {
	sessionTick := time.NewTicker(time.Hour)
	attemptTick := time.NewTicker(24 * time.Hour)
	renewTick := time.NewTicker(24 * time.Hour)
	defer sessionTick.Stop()
	defer attemptTick.Stop()
	defer renewTick.Stop()
	s.hourly()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sessionTick.C:
			s.hourly()
		case <-attemptTick.C:
			s.purgeAttempts()
			s.renewCerts(ctx)
		case <-renewTick.C:
			s.renewCerts(ctx)
		}
	}
}

func (s *Scheduler) hourly() {
	s.purgeSessions()
	s.auth.PurgePending()
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

func (s *Scheduler) renewCerts(ctx context.Context) {
	if s.renew == nil {
		return
	}
	if err := s.renew.RenewDue(ctx); err != nil {
		log.Printf("证书续期失败：%v", err)
	}
}
