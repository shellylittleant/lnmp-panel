package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegisterListAndRun(t *testing.T) {
	s := New()
	s.Tick = 20 * time.Millisecond
	var n atomic.Int32
	s.Register("x", 30*time.Millisecond, func(ctx context.Context) error {
		n.Add(1)
		return nil
	})
	items := s.List()
	if len(items) != 1 || items[0].ID != "x" {
		t.Fatalf("%+v", items)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	s.Run(ctx)
	if n.Load() == 0 {
		t.Fatal("应至少跑一次")
	}
}

func TestJobsRunInParallel(t *testing.T) {
	s := New()
	s.Tick = 15 * time.Millisecond
	var fast atomic.Int32
	s.Register("slow", 20*time.Millisecond, func(ctx context.Context) error {
		time.Sleep(80 * time.Millisecond)
		return nil
	})
	s.Register("fast", 20*time.Millisecond, func(ctx context.Context) error {
		fast.Add(1)
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	s.Run(ctx)
	if fast.Load() < 2 {
		t.Fatalf("fast=%d，应不被 slow 堵住", fast.Load())
	}
}

func TestNoReentryAndRunning(t *testing.T) {
	s := New()
	s.Tick = 10 * time.Millisecond
	var in atomic.Int32
	var maxc atomic.Int32
	started := make(chan struct{}, 1)
	s.Register("x", 10*time.Millisecond, func(ctx context.Context) error {
		n := in.Add(1)
		for {
			old := maxc.Load()
			if n <= old || maxc.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case started <- struct{}{}:
		default:
		}
		time.Sleep(50 * time.Millisecond)
		in.Add(-1)
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	go s.Run(ctx)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("未启动")
	}
	var saw bool
	for i := 0; i < 20; i++ {
		for _, it := range s.List() {
			if it.ID == "x" && it.Running {
				saw = true
			}
		}
		if saw {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !saw {
		t.Fatal("List 应标记运行中")
	}
	<-ctx.Done()
	if maxc.Load() > 1 {
		t.Fatalf("重入 max=%d", maxc.Load())
	}
}
