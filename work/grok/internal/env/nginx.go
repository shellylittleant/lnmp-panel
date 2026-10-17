package env

import (
	"context"
	"fmt"
	"os"
	"time"

	"lnmp-panel/internal/exec"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/nginx"
)

type NginxInstaller struct {
	Jobs   *jobs.Service
	Nginx  *nginx.Service
	Runner func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error)
}

func (n *NginxInstaller) Start(ctx context.Context) (string, error) {
	id, err := n.Jobs.Start("nginx-install")
	if err != nil {
		return "", err
	}
	go n.run(context.Background(), id)
	return id, nil
}

func (n *NginxInstaller) run(ctx context.Context, id string) {
	run := n.Runner
	if run == nil {
		run = exec.Run
	}
	n.Jobs.Update(id, "apt-update", "更新软件包索引", 10)
	if _, err := run(ctx, 5*time.Minute, "/usr/bin/apt-get", "update"); err != nil {
		n.Jobs.Finish(id, fmt.Errorf("apt-get update 失败：%w", err))
		return
	}
	n.Jobs.Update(id, "apt-install", "安装 nginx", 50)
	if _, err := run(ctx, 10*time.Minute, "/usr/bin/apt-get", "install", "-y", "nginx"); err != nil {
		n.Jobs.Finish(id, fmt.Errorf("安装 nginx 失败：%w", err))
		return
	}
	n.Jobs.Update(id, "configure", "写入全局片段", 80)
	if n.Nginx != nil {
		n.Nginx.DisableDefault()
		if err := n.Nginx.EnsureLayout(); err != nil {
			n.Jobs.Finish(id, err)
			return
		}
		if err := n.Nginx.TestAndReload(ctx); err != nil {
			n.Jobs.Finish(id, err)
			return
		}
	}
	if err := os.MkdirAll("/var/www", 0755); err != nil {
		n.Jobs.Finish(id, err)
		return
	}
	_ = os.Chmod("/var/www", 0755)
	n.Jobs.Finish(id, nil)
}

func NginxInstalled() bool {
	_, err := os.Stat("/usr/sbin/nginx")
	return err == nil
}
