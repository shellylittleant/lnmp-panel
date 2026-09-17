package ha

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"lnmp-panel/internal/exec"
	panelFS "lnmp-panel/internal/fs"
)

const dropInAfterPanel = `[Unit]
After=lnmp-panel.service
`

const dropInWantsStack = `[Unit]
Wants=nginx.service mysql.service
`

func (s *Service) writeMasterDropIns() error {
	root := s.systemdRoot()
	for _, unit := range []string{"nginx.service", "mysql.service", "mysqld.service"} {
		dir := filepath.Join(root, unit+".d")
		if err := panelFS.Mkdir(dir, 0755); err != nil {
			return err
		}
		if err := panelFS.WriteAtomic(filepath.Join(dir, "lnmp-panel.conf"), []byte(dropInAfterPanel), 0644); err != nil {
			return err
		}
	}
	panelDir := filepath.Join(root, "lnmp-panel.service.d")
	if err := panelFS.Mkdir(panelDir, 0755); err != nil {
		return err
	}
	if err := panelFS.WriteAtomic(filepath.Join(panelDir, "lnmp-panel.conf"), []byte(dropInWantsStack), 0644); err != nil {
		return err
	}
	s.daemonReload()
	return nil
}

func (s *Service) removeDropIns() {
	root := s.systemdRoot()
	for _, unit := range []string{"nginx.service", "mysql.service", "mysqld.service", "lnmp-panel.service"} {
		_ = os.Remove(filepath.Join(root, unit+".d", "lnmp-panel.conf"))
	}
	s.daemonReload()
}

func (s *Service) systemdRoot() string {
	if s.DropInRoot != "" {
		return s.DropInRoot
	}
	return "/etc/systemd/system"
}

func (s *Service) daemonReload() {
	if s.SkipExec || !exec.SystemdLive() {
		return
	}
	run := s.Runner
	if run == nil {
		run = exec.Run
	}
	_, _ = run(context.Background(), 10*time.Second, "/usr/bin/systemctl", "daemon-reload")
}
