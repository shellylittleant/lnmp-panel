package httpapi

import (
	"testing"
	"time"
)

func TestIPLimiterWindow(t *testing.T) {
	l := newIPLimiter()
	for i := 0; i < 30; i++ {
		if !l.allow("1.2.3.4", 30, time.Minute) {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	if l.allow("1.2.3.4", 30, time.Minute) {
		t.Fatal("第 31 次应拒绝")
	}
	if !l.allow("9.9.9.9", 30, time.Minute) {
		t.Fatal("其他 IP 应独立计数")
	}
}
