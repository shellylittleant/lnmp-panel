package nginx

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAndReloadRequiresInstall(t *testing.T) {
	s := New()
	s.Bin = "/no/such/nginx-bin"
	err := s.TestAndReload(context.Background())
	if err == nil || !strings.Contains(err.Error(), "未安装") {
		t.Fatalf("未安装应报错：%v", err)
	}
	_ = time.Second
}
