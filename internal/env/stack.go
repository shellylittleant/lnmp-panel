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
	Arch     string // 空则用 runtime.GOARCH；测试可注入
}

func (s *StackInstaller) arch() string {
	if s.Arch != "" {
		return s.Arch
	}
	return runtime.GOARCH
}

func (s *StackInstaller) useOfficialMySQL() bool {
	a := s.arch()
	return a == "amd64" || a == "386"
}

func (s *StackInstaller) MySQLSource() string {
	if s.useOfficialMySQL() {
		return "official"
	}
	return "ubuntu"
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
	official := s.useOfficialMySQL()
	if official {
		s.Jobs.Update(id, "mysql-repo", "写入 MySQL 官方 apt 源（x86_64）", 20)
		if err := s.writeMySQLRepo(); err != nil {
			s.Jobs.Finish(id, err)
			return
		}
	} else {
		s.Jobs.Update(id, "mysql-ubuntu", "arm64 使用 Ubuntu 源 mysql-server 8.0（docs/02 分源）", 20)
	}
	if !s.SkipExec {
		s.Jobs.Update(id, "apt-update", "更新软件包索引", 40)
		if r, err := run(ctx, 5*time.Minute, "/usr/bin/apt-get", "update"); err != nil {
			s.Jobs.Finish(id, fmt.Errorf("apt-get update 失败：%s", exec.Clip(r.Stderr+" "+r.Stdout)))
			return
		}
		if s.MySQL != nil && s.MySQL.MariaDB(ctx) {
			s.Jobs.Finish(id, mysql.ErrMariaDB)
			return
		}
		pkgs := []string{"mysql-server", "mysql-client"}
		label := "从 Ubuntu 源安装 mysql-server"
		if official {
			pkgs = []string{"mysql-community-server", "mysql-community-client"}
			label = "从官方源安装 mysql-community-server"
			show, err := run(ctx, 30*time.Second, "/usr/bin/apt-cache", "show", "mysql-community-server")
			if err != nil || !strings.Contains(show.Stdout, "Package: mysql-community-server") {
				s.Jobs.Finish(id, fmt.Errorf("MySQL 官方源没有 mysql-community-server（架构 %s），未回退 Ubuntu 源", s.arch()))
				return
			}
		}
		s.Jobs.Update(id, "mysql-pkg", label, 70)
		args := append([]string{"/usr/bin/apt-get", "install", "-y",
			"-o", "Dpkg::Options::=--force-confdef",
			"-o", "Dpkg::Options::=--force-confold"}, pkgs...)
		r, err := run(ctx, 15*time.Minute, args...)
		if err != nil {
			if s.MySQL == nil || !s.MySQL.Installed() {
				s.Jobs.Finish(id, fmt.Errorf("安装 MySQL 失败：%s", exec.Clip(r.Stderr+" "+r.Stdout)))
				return
			}
		}
	}
	s.Jobs.Update(id, "mysql-bootstrap", "创建面板账号并写入片段", 90)
	if s.MySQL != nil {
		if err := s.MySQL.EnsurePanelUser(ctx); err != nil {
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
