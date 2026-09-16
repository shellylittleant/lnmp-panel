package scheduler

import (
	"context"
	"log"
	"sync"
	"time"
)

type JobFn func(ctx context.Context) error

type Job struct {
	ID    string
	Every time.Duration
	Fn    JobFn
}

type Info struct {
	ID      string `json:"id"`
	Every   string `json:"every"`
	LastRun string `json:"last_run"`
	LastErr string `json:"last_error"`
}

type Scheduler struct {
	mu    sync.Mutex
	jobs  []Job
	last  map[string]time.Time
	lerr  map[string]string
	Tick  time.Duration
}

func New() *Scheduler {
	return &Scheduler{last: map[string]time.Time{}, lerr: map[string]string{}, Tick: time.Second}
}

func (s *Scheduler) Register(id string, every time.Duration, fn JobFn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, j := range s.jobs {
		if j.ID == id {
			s.jobs[i] = Job{ID: id, Every: every, Fn: fn}
			return
		}
	}
	s.jobs = append(s.jobs, Job{ID: id, Every: every, Fn: fn})
}

func (s *Scheduler) List() []Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Info
	for _, j := range s.jobs {
		info := Info{ID: j.ID, Every: j.Every.String()}
		if t, ok := s.last[j.ID]; ok {
			info.LastRun = t.UTC().Format(time.RFC3339)
		}
		info.LastErr = s.lerr[j.ID]
		out = append(out, info)
	}
	if out == nil {
		out = []Info{}
	}
	return out
}

func (s *Scheduler) Run(ctx context.Context) {
	d := s.Tick
	if d <= 0 {
		d = time.Second
	}
	tick := time.NewTicker(d)
	defer tick.Stop()
	next := map[string]time.Time{}
	now := time.Now()
	s.mu.Lock()
	for _, j := range s.jobs {
		next[j.ID] = now
	}
	jobs := append([]Job{}, s.jobs...)
	s.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-tick.C:
			for _, j := range jobs {
				if t.Before(next[j.ID]) {
					continue
				}
				next[j.ID] = t.Add(j.Every)
				s.runOne(ctx, j)
			}
		}
	}
}

func (s *Scheduler) runOne(ctx context.Context, j Job) {
	err := j.Fn(ctx)
	s.mu.Lock()
	s.last[j.ID] = time.Now()
	if err != nil {
		s.lerr[j.ID] = err.Error()
		log.Printf("调度 %s 失败：%v", j.ID, err)
	} else {
		s.lerr[j.ID] = ""
	}
	s.mu.Unlock()
}
