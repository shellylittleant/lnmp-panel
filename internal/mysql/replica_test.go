package mysql

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"lnmp-panel/internal/exec"
)

var errDenied = errors.New("denied")

func TestSetupReplicaSQLIncludesSSL(t *testing.T) {
	s := setupMySQL(t)
	s.SkipExec = false
	var got string
	s.IORunner = func(ctx context.Context, d time.Duration, stdin io.Reader, argv ...string) (exec.Result, error) {
		b, _ := io.ReadAll(stdin)
		got += string(b)
		return exec.Result{Stdout: "ok"}, nil
	}
	if err := s.SetupReplica(context.Background(), "10.0.0.2", 3306, "lnmp_repl", "p"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "SOURCE_SSL=1") {
		t.Fatalf("缺少 SOURCE_SSL=1：%s", got)
	}
}

func TestAdminFallsBackToRoot(t *testing.T) {
	s := setupMySQL(t)
	s.SkipExec = false
	if err := s.WritePanelCNF("sharedPassAa1"); err != nil {
		t.Fatal(err)
	}
	var last []string
	s.Runner = func(ctx context.Context, d time.Duration, args ...string) (exec.Result, error) {
		last = append([]string{}, args...)
		if len(args) >= 2 && args[1] == "-uroot" {
			return exec.Result{Stdout: "mysqld is alive"}, nil
		}
		return exec.Result{Stderr: "Access denied"}, errDenied
	}
	if err := s.admin(context.Background(), "ping"); err != nil {
		t.Fatal(err)
	}
	if len(last) < 2 || last[1] != "-uroot" {
		t.Fatalf("应回退 root：%v", last)
	}
}

func TestGTIDExecutedTreatsHeaderAsEmpty(t *testing.T) {
	s := setupMySQL(t)
	s.SkipExec = false
	s.IORunner = func(ctx context.Context, d time.Duration, stdin io.Reader, argv ...string) (exec.Result, error) {
		return exec.Result{Stdout: "@@GLOBAL.GTID_EXECUTED\n+------------------------+\n"}, nil
	}
	if g := s.GTIDExecuted(context.Background()); g != "" {
		t.Fatalf("表头不得当成 GTID：%q", g)
	}
}

func TestWritePanelCNF(t *testing.T) {
	s := setupMySQL(t)
	if err := s.WritePanelCNF("sharedPassAa1"); err != nil {
		t.Fatal(err)
	}
	if s.PanelPassword() != "sharedPassAa1" {
		t.Fatalf("pass %q", s.PanelPassword())
	}
	b, err := os.ReadFile(s.Defaults)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "user=lnmp_panel") {
		t.Fatal(string(b))
	}
	st, err := os.Stat(s.Defaults)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", st.Mode())
	}
}
