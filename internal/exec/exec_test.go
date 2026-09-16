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

func TestAllowNewBins(t *testing.T) {
	for _, b := range []string{"/usr/bin/apt-cache", "/usr/bin/dpkg-query", "/usr/sbin/mysqld", "/usr/sbin/php-fpm8.3"} {
		if !allowed(b) {
			t.Fatalf("应允许 %s", b)
		}
	}
	if allowed("/usr/bin/pecl") || allowed("/usr/bin/docker") {
		t.Fatal("不应允许 pecl/docker")
	}
}
