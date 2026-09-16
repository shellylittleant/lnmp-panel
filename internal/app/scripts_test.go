package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("找不到仓库根")
	return ""
}

func TestSystemdRestartAlways(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "systemd/lnmp-panel.service"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"Restart=always",
		"StartLimitIntervalSec=60",
		"StartLimitBurst=5",
		"UMask=0077",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("单元文件缺少 %s", want)
		}
	}
	if strings.Contains(s, "Restart=on-failure") {
		t.Fatal("仍是 on-failure，干净退出不会被拉起")
	}
}

func TestInstallScriptFixes(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts/install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	wants := []string{
		"ufw --force disable",
		"systemctl disable --now ufw",
		"apt-get install -y nftables",
		"panel.json",
		"跳过初始化",
		"systemctl restart lnmp-panel.service",
		"install.info",
	}
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Fatalf("install.sh 缺少 %q", w)
		}
	}
}

func TestStripHiddenSkipOSCheck(t *testing.T) {
	args, on := stripHiddenFlag([]string{"--config", "x", "--skip-os-check"}, "skip-os-check")
	if !on {
		t.Fatal("应解析到 skip")
	}
	if strings.Join(args, " ") != "--config x" {
		t.Fatalf("过滤失败：%v", args)
	}
	help := `lnmp-panel 命令：
  bootstrap  初始化目录、证书、管理员与配置
  run        启动 HTTPS 面板
  version    打印版本
`
	if strings.Contains(help, "skip-os-check") {
		t.Fatal("主帮助不应出现 skip-os-check")
	}
}
