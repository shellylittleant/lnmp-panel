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
