package exec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

var allow = map[string]struct{}{
	"/usr/bin/systemctl":          {},
	"/usr/sbin/nginx":             {},
	"/usr/bin/apt-get":            {},
	"/usr/bin/add-apt-repository": {},
	"/usr/bin/mysql":              {},
	"/usr/bin/mysqldump":          {},
	"/usr/bin/mysqladmin":         {},
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
	if len(bin) > len("/usr/sbin/php-fpm8.") && bin[:len("/usr/sbin/php-fpm8.")] == "/usr/sbin/php-fpm8." {
		return true
	}
	return false
}

type Result struct {
	Stdout string
	Stderr string
}

func Run(ctx context.Context, timeout time.Duration, argv ...string) (Result, error) {
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
	err := cmd.Run()
	r.Stdout = out.String()
	r.Stderr = errb.String()
	if err != nil {
		return r, fmt.Errorf("%s：%w", argv[0], err)
	}
	return r, nil
}
