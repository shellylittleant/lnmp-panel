package exec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

var allow = map[string]struct{}{
	"/usr/bin/systemctl":          {},
	"/usr/sbin/nginx":             {},
	"/usr/bin/apt-get":            {},
	"/usr/bin/apt-cache":          {},
	"/usr/bin/dpkg-query":         {},
	"/usr/bin/add-apt-repository": {},
	"/usr/bin/mysql":              {},
	"/usr/bin/mysqldump":          {},
	"/usr/bin/mysqladmin":         {},
	"/usr/sbin/mysqld":            {},
	"/usr/sbin/nft":               {},
	"/usr/bin/rsync":              {},
	"/usr/bin/ssh":                {},
	"/usr/bin/ssh-keygen":         {},
	"/usr/sbin/logrotate":         {},
	"/usr/bin/crontab":            {},
}

func allowed(bin string) bool {
	if _, ok := allow[bin]; ok {
		return true
	}
	// php-fpm8.x，x>=2
	const pfx = "/usr/sbin/php-fpm8."
	if strings.HasPrefix(bin, pfx) && len(bin) > len(pfx) {
		return true
	}
	return false
}

type Result struct {
	Stdout string
	Stderr string
}

func Run(ctx context.Context, timeout time.Duration, argv ...string) (Result, error) {
	return RunIO(ctx, timeout, nil, argv...)
}

func RunIO(ctx context.Context, timeout time.Duration, stdin io.Reader, argv ...string) (Result, error) {
	var r Result
	if len(argv) == 0 {
		return r, fmt.Errorf("空命令")
	}
	if !allowed(argv[0]) {
		return r, fmt.Errorf("命令不在白名单：%s", argv[0])
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.Env = append(os.Environ(),
		"DEBIAN_FRONTEND=noninteractive",
		"NEEDRESTART_MODE=a",
		"APT_LISTCHANGES_FRONTEND=none",
	)
	err := cmd.Run()
	r.Stdout = out.String()
	r.Stderr = errb.String()
	if err != nil {
		return r, fmt.Errorf("%s：%w", argv[0], err)
	}
	return r, nil
}

func Clip(s string) string {
	if len(s) > 2000 {
		return s[:2000]
	}
	return s
}

func SystemdLive() bool {
	st, err := os.Stat("/run/systemd/system")
	return err == nil && st.IsDir()
}

// ReloadOrStart 有 systemd 时 reload，失败再 start；无 systemd 时执行 fallback（可空）。
func ReloadOrStart(ctx context.Context, run func(context.Context, time.Duration, ...string) (Result, error), unit string, fallback []string) error {
	if run == nil {
		run = Run
	}
	if SystemdLive() {
		r, err := run(ctx, 15*time.Second, "/usr/bin/systemctl", "reload", unit)
		if err == nil {
			return nil
		}
		r2, err2 := run(ctx, 30*time.Second, "/usr/bin/systemctl", "start", unit)
		if err2 != nil {
			return fmt.Errorf("systemctl reload/start %s 失败：%s %s", unit, Clip(r.Stderr), Clip(r2.Stderr))
		}
		return nil
	}
	if len(fallback) == 0 {
		return fmt.Errorf("无 systemd，无法启动 %s", unit)
	}
	r, err := run(ctx, 30*time.Second, fallback...)
	if err != nil {
		return fmt.Errorf("启动 %s 失败：%s", unit, Clip(r.Stderr+" "+r.Stdout))
	}
	return nil
}
