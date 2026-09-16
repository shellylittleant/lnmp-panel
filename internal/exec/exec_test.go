package exec

import (
	"context"
	"testing"
	"time"
)

func TestRejectUnknown(t *testing.T) {
	_, err := Run(context.Background(), time.Second, "/bin/sh", "-c", "echo hi")
	if err == nil {
		t.Fatal("应拒绝 /bin/sh")
	}
}
