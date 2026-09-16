package env

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

var (
	dockerSock    = "/var/run/docker.sock"
	systemctlPath = "/usr/bin/systemctl"
)

func DockerPresent() bool {
	if _, err := os.Stat(dockerSock); err == nil {
		return true
	}
	if _, err := os.Stat(systemctlPath); err != nil {
		return false
	}
	out, err := exec.Command(systemctlPath, "is-active", "docker").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "active"
}

func RefuseIfDocker() error {
	if DockerPresent() {
		return errors.New("检测到 Docker。本面板独占 nftables，不支持与 Docker 共存。请卸载 Docker 后重试")
	}
	return nil
}
