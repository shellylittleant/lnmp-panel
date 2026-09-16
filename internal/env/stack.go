package env

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lnmp-panel/internal/exec"
	panelFS "lnmp-panel/internal/fs"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/mysql"
)

const (
	mysqlKeyURL  = "https://repo.mysql.com/RPM-GPG-KEY-mysql-2025"
	mysqlList    = "/etc/apt/sources.list.d/mysql-official.list"
	mysqlKeyPath = "/usr/share/keyrings/mysql-apt-config.asc"
	mysqlSeries  = "mysql-8.4-lts mysql-tools"
)

type StackInstaller struct {
	Jobs     *jobs.Service
	MySQL    *mysql.Service
	Runner   func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error)
	HTTPGet  func(url string) ([]byte, error)
	Codename string
	SkipExec bool
	KeyPath  string
	ListPath string
}

func (s *StackInstaller) run() func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error) {
	if s.Runner != nil {
		return s.Runner
	}
	return exec.Run
}

func (s *StackInstaller) StartPHPRepo(ctx context.Context) (string, error) {
	id, err := s.Jobs.Start("php-repo")
	if err != nil {
		return "", err
	}
	go s.runPHPRepo(context.Background(), id)
	return id, nil
}

func (s *StackInstaller) StartMySQL(ctx context.Context) (string, error) {
	id, err := s.Jobs.Start("mysql-install")
	if err != nil {
		return "", err
	}
	go s.runMySQL(context.Background(), id)
	return id, nil
}

func (s *StackInstaller) runPHPRepo(ctx context.Context, id string) {
	run := s.run()
	if !s.SkipExec {
		s.Jobs.Update(id, "apt-update", "更新软件包索引", 20)
		if r, err := run(ctx, 5*time.Minute, "/usr/bin/apt-get", "update"); err != nil {
			s.Jobs.Finish(id, fmt.Errorf("apt-get update 失败：%s", exec.Clip(r.Stderr+" "+r.Stdout)))
			return
		}
		s.Jobs.Update(id, "deps", "安装 add-apt-repository 依赖", 40)
		if r, err := run(ctx, 10*time.Minute, "/usr/bin/apt-get", "install", "-y",
			"software-properties-common", "ca-certificates", "gnupg"); err != nil {
			s.Jobs.Finish(id, fmt.Errorf("安装依赖失败：%s", exec.Clip(r.Stderr+" "+r.Stdout)))
			return
		}
		s.Jobs.Update(id, "ppa", "添加 Ondřej PHP PPA", 70)
		if r, err := run(ctx, 5*time.Minute, "/usr/bin/add-apt-repository", "-y", "ppa:ondrej/php"); err != nil {
			s.Jobs.Finish(id, fmt.Errorf("添加 PPA 失败：%s", exec.Clip(r.Stderr+" "+r.Stdout)))
			return
		}
		s.Jobs.Update(id, "apt-update-2", "更新索引（含 PPA）", 90)
		if r, err := run(ctx, 5*time.Minute, "/usr/bin/apt-get", "update"); err != nil {
			s.Jobs.Finish(id, fmt.Errorf("PPA 后 apt-get update 失败：%s", exec.Clip(r.Stderr+" "+r.Stdout)))
			return
		}
	}
	s.Jobs.Finish(id, nil)
}

func (s *StackInstaller) runMySQL(ctx context.Context, id string) {
	run := s.run()
	s.Jobs.Update(id, "mysql-repo", "写入 MySQL 官方 apt 源", 20)
	if err := s.writeMySQLRepo(); err != nil {
		s.Jobs.Finish(id, err)
		return
	}
	if !s.SkipExec {
		s.Jobs.Update(id, "apt-update", "更新索引（官方 MySQL）", 40)
		if r, err := run(ctx, 5*time.Minute, "/usr/bin/apt-get", "update"); err != nil {
			s.Jobs.Finish(id, fmt.Errorf("官方源 apt-get update 失败（未回退 Ubuntu mysql）：%s", exec.Clip(r.Stderr+" "+r.Stdout)))
			return
		}
		if s.MySQL != nil && s.MySQL.MariaDB(ctx) {
			s.Jobs.Finish(id, mysql.ErrMariaDB)
			return
		}
		s.Jobs.Update(id, "mysql-pkg", "从官方源安装 mysql-community-server", 70)
		show, err := run(ctx, 30*time.Second, "/usr/bin/apt-cache", "show", "mysql-community-server")
		if err != nil || !strings.Contains(show.Stdout, "Package: mysql-community-server") {
			s.Jobs.Finish(id, fmt.Errorf("MySQL 官方源没有 mysql-community-server（当前架构 %s）。官方 noble 仓库目前只有 amd64/i386。未回退 Ubuntu 的 mysql-server", runtime.GOARCH))
			return
		}
		r, err := run(ctx, 15*time.Minute, "/usr/bin/apt-get", "install", "-y",
			"-o", "Dpkg::Options::=--force-confdef",
			"-o", "Dpkg::Options::=--force-confold",
			"mysql-community-server", "mysql-community-client")
		if err != nil {
			if s.MySQL == nil || !s.MySQL.Installed() {
				s.Jobs.Finish(id, fmt.Errorf("安装 mysql-community-server 失败（未回退 Ubuntu 源）：%s", exec.Clip(r.Stderr+" "+r.Stdout)))
				return
			}
		}
	}
	s.Jobs.Update(id, "mysql-bootstrap", "启动 MySQL 并写入面板片段", 90)
	if s.MySQL != nil {
		if err := s.MySQL.EnsureRoot(ctx); err != nil {
			s.Jobs.Finish(id, err)
			return
		}
		if err := s.MySQL.WriteDefaultCNF(ctx); err != nil {
			s.Jobs.Finish(id, err)
			return
		}
	}
	s.Jobs.Finish(id, nil)
}

func (s *StackInstaller) writeMySQLRepo() error {
	codename := s.Codename
	if codename == "" {
		if info, err := MustCurrentOS(); err == nil {
			codename = AptCodename(info)
		}
	}
	if codename == "" {
		return fmt.Errorf("无法确定 Ubuntu 代号，无法写 MySQL 官方源")
	}
	get := s.HTTPGet
	if get == nil {
		get = httpGet
	}
	body, err := get(mysqlKeyURL)
	if err != nil {
		return fmt.Errorf("下载 MySQL 官方 GPG 密钥失败：%w", err)
	}
	keyPath := s.KeyPath
	if keyPath == "" {
		keyPath = mysqlKeyPath
	}
	listPath := s.ListPath
	if listPath == "" {
		listPath = mysqlList
	}
	if err := panelFS.Mkdir(filepath.Dir(keyPath), 0755); err != nil {
		return err
	}
	if err := panelFS.WriteAtomic(keyPath, body, 0644); err != nil {
		return err
	}
	line := fmt.Sprintf("deb [signed-by=%s] http://repo.mysql.com/apt/ubuntu %s %s\n", keyPath, codename, mysqlSeries)
	if err := panelFS.Mkdir(filepath.Dir(listPath), 0755); err != nil {
		return err
	}
	return panelFS.WriteAtomic(listPath, []byte(line), 0644)
}

func httpGet(url string) ([]byte, error) {
	c := &http.Client{Timeout: 60 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
