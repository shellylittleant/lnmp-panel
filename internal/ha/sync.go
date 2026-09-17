package ha

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lnmp-panel/internal/exec"
	panelFS "lnmp-panel/internal/fs"
	"lnmp-panel/internal/migrate"
	"lnmp-panel/internal/mysql"
	"lnmp-panel/internal/nginx"
)

func cloneLost(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "lost") || strings.Contains(m, "gone away") || strings.Contains(m, "connect")
}

func (s *Service) StartSync(ctx context.Context) error {
	st, err := s.loadState()
	if err != nil {
		return err
	}
	if st.Role != RoleStandby {
		return fmt.Errorf("只有备机可以开始同步")
	}
	if _, _, err := s.peerMeta(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.syncing {
		s.mu.Unlock()
		return fmt.Errorf("同步已在运行")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	s.syncCancel = cancel
	s.syncing = true
	s.mu.Unlock()
	go s.syncLoop(runCtx)
	s.setDetail("已开始从主机拉取")
	return nil
}

func (s *Service) stopSyncLocked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.syncCancel != nil {
		s.syncCancel()
		s.syncCancel = nil
	}
	s.syncing = false
}

func (s *Service) syncLoop(ctx context.Context) {
	defer func() {
		s.mu.Lock()
		s.syncing = false
		s.mu.Unlock()
	}()
	first := true
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		detail, err := s.syncRound(ctx, first)
		first = false
		elapsed := time.Since(start)
		interval := NextInterval(elapsed)
		alert := SyncAlert(elapsed)
		lag := int(elapsed.Seconds())
		replOK := false
		if s.MySQL != nil {
			if rs, e := s.MySQL.ReplicaStatus(ctx); e == nil {
				replOK = rs.OK
				if rs.LagSec > 0 {
					lag = rs.LagSec
				}
				if rs.LastError != "" && detail == "完成" {
					detail = "复制：" + rs.LastError
				}
			}
		}
		if err != nil {
			detail = err.Error()
		}
		s.saveSync(elapsed, interval, lag, replOK, alert, detail)
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

func (s *Service) syncRound(ctx context.Context, first bool) (string, error) {
	addr, fp, err := s.peerMeta()
	if err != nil {
		return "", err
	}
	s.setDetail("同步站点文件")
	if err := s.rsyncFromPeer(ctx, addr); err != nil {
		return "", fmt.Errorf("rsync：%w", err)
	}
	if err := s.applyNginxCatchall(); err != nil {
		return "", fmt.Errorf("恢复 404：%w", err)
	}
	s.setDetail("拉取配置")
	token := s.machineToken()
	var bundle migrate.Bundle
	if err := s.Migrate.PostPeer(ctx, addr, fp, "", "/ha/peer/bundle", map[string]string{"token": token}, &bundle); err != nil {
		return "", fmt.Errorf("配置拉取：%w", err)
	}
	if _, err := s.Migrate.ApplyConfigPull(ctx, bundle); err != nil {
		return "", fmt.Errorf("应用配置：%w", err)
	}
	if err := s.applyNginxCatchall(); err != nil {
		return "", fmt.Errorf("保持 404：%w", err)
	}
	if s.MySQL != nil && s.MySQL.Installed() && !s.SkipExec {
		s.setDetail("确认 MySQL 复制")
		if err := s.ensureReplica(ctx, addr, fp, token, first); err != nil {
			return "", err
		}
	}
	if err := s.pullAndAdoptTerm(ctx); err != nil {
		return "", fmt.Errorf("同步任期：%w", err)
	}
	return "完成", nil
}

func (s *Service) rsyncFromPeer(ctx context.Context, addr string) error {
	sshHost, err := migrate.SSHHostOf(addr)
	if err != nil {
		return err
	}
	user, port := s.peerSSH()
	destPrefix := user + "@" + sshHost + ":"
	pull := func(remote, local string, extra ...string) error {
		return migrate.RsyncOverSSH(ctx, s.Runner, destPrefix+remote, local, s.haKeyPath(), s.haKnownHosts(), port, s.SkipExec, extra...)
	}
	www := s.WWW
	if www == "" {
		www = "/var/www"
	}
	if err := pull(www+"/", www+"/", "--exclude=.lnmp-panel-ha"); err != nil {
		return fmt.Errorf("站点：%w", err)
	}
	if s.SSL != nil && s.SSL.Dir != "" {
		_ = pull(s.SSL.Dir+"/", s.SSL.Dir+"/")
	}
	if s.Nginx != nil {
		if s.Nginx.Available != "" {
			_ = pull(s.Nginx.Available+"/", s.Nginx.Available+"/",
				"--exclude="+nginx.CatchallFile, "--exclude="+nginx.MaintenanceFile)
		}
		if s.Nginx.ConfD != "" {
			_ = pull(s.Nginx.ConfD+"/", s.Nginx.ConfD+"/")
		}
	}
	acme := filepath.Join(s.Secrets, "acme.key")
	_ = pull(acme, acme)
	_ = pull("/etc/mysql/debian.cnf", "/etc/mysql/debian.cnf")
	return nil
}

func (s *Service) ensureReplica(ctx context.Context, addr, fp, token string, first bool) error {
	rs, err := s.MySQL.ReplicaStatus(ctx)
	if err == nil && rs.OK {
		return nil
	}
	var src ReplSource
	if err := s.Migrate.PostPeer(ctx, addr, fp, "", "/ha/peer/repl-source", map[string]string{"token": token}, &src); err != nil {
		return fmt.Errorf("复制源：%w", err)
	}
	if h, err := migrate.SSHHostOf(addr); err == nil && h != "" {
		src.Host = h
	}
	if src.Port <= 0 {
		src.Port = 3306
	}
	if src.PanelPass != "" {
		_ = s.MySQL.WritePanelCNF(src.PanelPass)
	}
	seeded, _ := s.Settings.Get("ha_mysql_seeded")
	if seeded != "1" {
		if strings.TrimSpace(s.MySQL.GTIDExecuted(ctx)) == "" {
			if err := s.firstCopy(ctx, src); err != nil {
				return err
			}
		}
		_ = s.Settings.Set("ha_mysql_seeded", "1")
	}
	if err := s.MySQL.SetupReplica(ctx, src.Host, src.Port, src.User, src.Pass); err != nil {
		return fmt.Errorf("启动复制：%w", err)
	}
	_ = s.MySQL.WriteHACNF(mysql.ServerIDFromHost(s.hostname()), false)
	_ = s.MySQL.SetReadOnly(ctx, true)
	var st mysql.ReplicaStatus
	for i := 0; i < 15; i++ {
		st, _ = s.MySQL.ReplicaStatus(ctx)
		if st.OK {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	if !st.OK {
		msg := st.LastError
		if msg == "" {
			msg = "IO/SQL 未运行"
		}
		return fmt.Errorf("复制未就绪：%s", msg)
	}
	return nil
}

func (s *Service) firstCopy(ctx context.Context, src ReplSource) error {
	id := mysql.ServerIDFromHost(s.hostname())
	if err := s.MySQL.WriteHABaseCNF(id, false); err != nil {
		return err
	}
	if !s.SkipExec {
		_ = s.MySQL.Restart(ctx)
	}
	_ = s.pullDebianCNF(ctx, src.Host)
	cloneErr := s.MySQL.CloneFrom(ctx, src.Host, src.Port, src.User, src.Pass)
	up := s.MySQL.EnsureRunning(ctx)
	if up == nil && (cloneErr == nil || cloneLost(cloneErr)) {
		if strings.EqualFold(s.MySQL.GTIDMode(ctx), "ON") {
			_ = s.MySQL.ResetServerUUID(ctx)
			return nil
		}
	}
	s.setDetail("clone 不可用，改用全库导出")
	tmp := "/var/lib/lnmp-panel/tmp"
	if s.DataDir != "" {
		tmp = filepath.Join(s.DataDir, "tmp")
	}
	_ = panelFS.Mkdir(tmp, 0700)
	path := filepath.Join(tmp, "ha-mysql.sql")
	if err := panelFS.Mkdir(tmp, 0700); err != nil {
		return err
	}
	if err := s.dumpPeerMySQL(ctx, src, path); err != nil {
		_ = os.Remove(path)
		return err
	}
	rf, err := os.Open(path)
	if err != nil {
		return err
	}
	impErr := s.MySQL.ImportAsRoot(ctx, rf)
	_ = rf.Close()
	_ = os.Remove(path)
	if impErr != nil {
		return fmt.Errorf("导入全库：%w", impErr)
	}
	return nil
}

func (s *Service) pullDebianCNF(ctx context.Context, sshHost string) error {
	if sshHost == "" {
		return nil
	}
	user, port := s.peerSSH()
	return migrate.RsyncOverSSH(ctx, s.Runner, user+"@"+sshHost+":/etc/mysql/debian.cnf", "/etc/mysql/debian.cnf",
		s.haKeyPath(), s.haKnownHosts(), port, s.SkipExec)
}

func (s *Service) dumpPeerMySQL(ctx context.Context, src ReplSource, localPath string) error {
	if s.SkipExec {
		f, err := os.Create(localPath)
		if err != nil {
			return err
		}
		defer f.Close()
		return s.MySQL.DumpAllForReplica(ctx, f)
	}
	sshHost := src.Host
	user, port := s.peerSSH()
	remote := "/var/lib/lnmp-panel/tmp/ha-mysql.sql"
	run := s.Runner
	if run == nil {
		run = exec.Run
	}
	script := "mkdir -p /var/lib/lnmp-panel/tmp && mysqldump -uroot --single-transaction --routines --events --triggers --all-databases --set-gtid-purged=ON --source-data=2 > " + remote
	r, err := run(ctx, 30*time.Minute, "/usr/bin/ssh",
		"-i", s.haKeyPath(), "-p", fmt.Sprintf("%d", port),
		"-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile="+s.haKnownHosts(),
		"-o", "GlobalKnownHostsFile=/dev/null", "-o", "BatchMode=yes",
		user+"@"+sshHost, script)
	if err != nil {
		return fmt.Errorf("对端 mysqldump：%s", strings.TrimSpace(r.Stderr+" "+r.Stdout))
	}
	return migrate.RsyncOverSSH(ctx, s.Runner, user+"@"+sshHost+":"+remote, localPath, s.haKeyPath(), s.haKnownHosts(), port, false)
}
