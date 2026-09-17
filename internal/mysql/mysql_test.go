package mysql

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"lnmp-panel/internal/exec"
	"lnmp-panel/internal/jobs"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

func setupMySQL(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tpl := template.New(db)
	if err := tpl.SyncOfficial(); err != nil {
		t.Fatal(err)
	}
	s := New(db, tpl, jobs.New(db))
	s.SkipExec = true
	s.CNFPath = filepath.Join(dir, "lnmp-panel.cnf")
	s.Defaults = filepath.Join(dir, "root.cnf")
	s.BackupRoot = filepath.Join(dir, "bak")
	s.TmpDir = filepath.Join(dir, "tmp")
	return s
}

func TestIdentRejectsInject(t *testing.T) {
	for _, n := range []string{"a;drop", "a b", "a`b", "a'b", "", "foo-bar", "foo.bar"} {
		if ValidIdent(n) == nil {
			t.Fatalf("应拒绝 %q", n)
		}
	}
	if err := ValidIdent("blog_1"); err != nil {
		t.Fatal(err)
	}
	if ValidHost("localhost") != nil || ValidHost("%") != nil {
		t.Fatal("host")
	}
	if ValidHost("a;b") == nil || ValidHost("a b") == nil {
		t.Fatal("应拒绝坏 host")
	}
}

func TestDropSystemAndUnmanaged(t *testing.T) {
	s := setupMySQL(t)
	ctx := context.Background()
	if err := s.DropDB(ctx, "sys"); err != ErrSystemDB {
		t.Fatalf("sys: %v", err)
	}
	if err := s.DropDB(ctx, "mysql"); err != ErrSystemDB {
		t.Fatalf("mysql: %v", err)
	}
	if err := s.DropDB(ctx, "performance_schema"); err != ErrSystemDB {
		t.Fatalf("ps: %v", err)
	}
	if err := s.DropDB(ctx, "manual_db"); err != ErrUnmanaged {
		t.Fatalf("unmanaged: %v", err)
	}
	if _, err := s.CreateDB(ctx, "blog", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DropDB(ctx, "blog"); err != nil {
		t.Fatal(err)
	}
}

func TestReservedUser(t *testing.T) {
	s := setupMySQL(t)
	for _, u := range []string{"root", "lnmp_panel", "mysql.sys"} {
		if _, err := s.CreateUser(context.Background(), u, "localhost", "GoodPass1", nil); err != ErrReservedUser {
			t.Fatalf("%s: %v", u, err)
		}
	}
}

func TestPasswordNotInArgv(t *testing.T) {
	s := setupMySQL(t)
	var seen [][]string
	s.IORunner = func(ctx context.Context, d time.Duration, stdin io.Reader, argv ...string) (exec.Result, error) {
		seen = append(seen, append([]string{}, argv...))
		return exec.Result{}, nil
	}
	ctx := context.Background()
	if _, err := s.CreateDB(ctx, "blog", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "blogu", "localhost", "SuperSecret1", []string{"blog"}); err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("应调用 IORunner")
	}
	for _, argv := range seen {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, "IDENTIFIED BY") || strings.Contains(joined, "SuperSecret1") {
			t.Fatalf("argv 含密码：%v", argv)
		}
	}
}

func TestStartFailIncludesErrorLog(t *testing.T) {
	s := setupMySQL(t)
	p := filepath.Join(t.TempDir(), "error.log")
	body := ""
	for i := 0; i < 30; i++ {
		body += "line\n"
	}
	body += "InnoDB: fatal\n"
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	s.ErrorLog = p
	err := s.wrapStartErr("启动 mysqld 失败", exec.Result{}, fmt.Errorf("x"))
	if err == nil || !strings.Contains(err.Error(), "InnoDB: fatal") {
		t.Fatalf("%v", err)
	}
}

func TestCreateDropDBAndUser(t *testing.T) {
	s := setupMySQL(t)
	ctx := context.Background()
	d, err := s.CreateDB(ctx, "blog", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Charset != "utf8mb4" {
		t.Fatal(d)
	}
	if _, err := s.CreateDB(ctx, "blog", "", ""); err != ErrDup {
		t.Fatalf("got %v", err)
	}
	if _, err := s.CreateDB(ctx, "x;drop", "", ""); err == nil {
		t.Fatal("注入")
	}
	u, err := s.CreateUser(ctx, "blogu", "localhost", "GoodPass1", []string{"blog"})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Databases) != 1 || u.Databases[0] != "blog" {
		t.Fatalf("%+v", u)
	}
	if _, err := s.CreateUser(ctx, "bad;u", "localhost", "GoodPass1", nil); err == nil {
		t.Fatal("用户名注入")
	}
	if err := s.SetPassword(ctx, u.ID, "NewPassw0"); err != nil {
		t.Fatal(err)
	}
	if err := s.DropDB(ctx, "blog"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListDB()
	if len(list) != 0 {
		t.Fatalf("%v", list)
	}
}

func TestListUsersNoNestedQueryDeadlock(t *testing.T) {
	dir := t.TempDir()
	db, err := store.OpenConns(filepath.Join(dir, "p.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tpl := template.New(db)
	if err := tpl.SyncOfficial(); err != nil {
		t.Fatal(err)
	}
	s := New(db, tpl, jobs.New(db))
	s.SkipExec = true
	s.CNFPath = filepath.Join(dir, "lnmp-panel.cnf")
	s.Defaults = filepath.Join(dir, "root.cnf")
	s.BackupRoot = filepath.Join(dir, "bak")
	s.TmpDir = filepath.Join(dir, "tmp")
	ctx := context.Background()
	if _, err := s.CreateDB(ctx, "blog", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "u1", "localhost", "GoodPass1", []string{"blog"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "u2", "localhost", "GoodPass1", []string{"blog"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		items, err := s.ListUsers()
		if err == nil && len(items) != 2 {
			err = fmt.Errorf("want 2 got %d", len(items))
		}
		if err == nil && (len(items[0].Databases) != 1 || len(items[1].Databases) != 1) {
			err = fmt.Errorf("grants %+v", items)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListUsers 在单连接下超过 2 秒，疑似嵌套查询死锁")
	}
}

func TestCNFFormVsRaw(t *testing.T) {
	s := setupMySQL(t)
	ctx := context.Background()
	old := syscall.Umask(0077)
	defer syscall.Umask(old)
	if err := s.WriteDefaultCNF(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(s.CNFPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0644 {
		t.Fatalf("perm=%o", st.Mode().Perm())
	}
	before, _ := s.tpl.List("mysql_cnf")
	in := defaultCNF()
	in.MaxConnections = "200"
	if err := s.PutCNF(ctx, in, false); err != nil {
		t.Fatal(err)
	}
	after, _ := s.tpl.List("mysql_cnf")
	if len(after) != len(before) {
		t.Fatal("表单不应产生用户模板")
	}
	body, _ := os.ReadFile(s.CNFPath)
	if !strings.Contains(string(body), "200") {
		t.Fatalf("%s", body)
	}
	if err := s.PutCNFRaw(ctx, "[mysqld]\nmax_connections = 50\n", false); err != nil {
		t.Fatal(err)
	}
	after2, _ := s.tpl.List("mysql_cnf")
	if len(after2) != len(before)+1 {
		t.Fatalf("原文应产生用户模板 %d -> %d", len(before), len(after2))
	}
	if err := s.PutCNF(ctx, defaultCNF(), false); err != ErrRawMode {
		t.Fatalf("got %v", err)
	}
	if err := os.WriteFile(s.CNFPath, []byte("[mysqld]\n#drift\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCNFRaw(ctx, "[mysqld]\nx=1\n", false); err != ErrDrift {
		t.Fatalf("got %v", err)
	}
	if err := s.UseCNFTemplate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCNF(ctx, defaultCNF(), false); err != nil {
		t.Fatal(err)
	}
}

func TestDumpManagedArgsNoAllDatabases(t *testing.T) {
	args := DumpManagedArgs("/tmp/panel.cnf", []string{"blog", "shop"})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--all-databases") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "--databases") || !strings.Contains(joined, "blog") {
		t.Fatal(joined)
	}
	if args[0] != "/usr/bin/mysqldump" {
		t.Fatal(args)
	}
}

func TestReservedUserDumpEmpty(t *testing.T) {
	s := setupMySQL(t)
	for _, u := range []string{"root", "lnmp_panel", "debian-sys-maint", "mysql.sys"} {
		if !IsReservedUser(u) {
			t.Fatalf("%s 应排除", u)
		}
		sqlu, err := s.UserDumpSQL(context.Background(), u, "localhost")
		if err != nil || sqlu != "" {
			t.Fatalf("%s dump %q %v", u, sqlu, err)
		}
	}
}

func TestFormatCreateUserFromPluginHex(t *testing.T) {
	got, err := FormatCreateUserSQL("migu", "localhost", "caching_sha2_password", "AA01FF")
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE USER IF NOT EXISTS 'migu'@'localhost' IDENTIFIED WITH 'caching_sha2_password' AS 0xAA01FF;"
	if got != want {
		t.Fatalf("got %s", got)
	}
	if _, err := FormatCreateUserSQL("migu", "localhost", "bad';drop", "AA"); err == nil {
		t.Fatal("插件名应拒绝")
	}
	if _, err := FormatCreateUserSQL("migu", "localhost", "caching_sha2_password", "zz"); err == nil {
		t.Fatal("哈希应拒绝")
	}
	plugin, hexHash, err := parsePluginHex("caching_sha2_password\tDEADBEEF\n")
	if err != nil || plugin != "caching_sha2_password" || hexHash != "DEADBEEF" {
		t.Fatalf("%s %s %v", plugin, hexHash, err)
	}
}

func TestUserDumpSQLUsesMysqlUserNotShowCreate(t *testing.T) {
	s := setupMySQL(t)
	var seen []string
	s.IORunner = func(ctx context.Context, d time.Duration, stdin io.Reader, argv ...string) (exec.Result, error) {
		b, _ := io.ReadAll(stdin)
		q := string(b)
		seen = append(seen, q)
		if strings.Contains(q, "SHOW CREATE USER") {
			t.Fatal("不得解析 SHOW CREATE USER")
		}
		if strings.Contains(q, "HEX(authentication_string)") {
			return exec.Result{Stdout: "caching_sha2_password\tAABBCC\n"}, nil
		}
		if strings.Contains(q, "SHOW GRANTS") {
			return exec.Result{Stdout: "GRANT ALL ON `migdb`.* TO 'migu'@'localhost'\n"}, nil
		}
		return exec.Result{}, nil
	}
	s.SkipExec = false
	sqlu, err := s.UserDumpSQL(context.Background(), "migu", "localhost")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sqlu, "IDENTIFIED WITH 'caching_sha2_password' AS 0xAABBCC") {
		t.Fatal(sqlu)
	}
	if strings.Contains(sqlu, "IDENTIFIED BY") {
		t.Fatal(sqlu)
	}
	if !strings.Contains(sqlu, "GRANT ALL ON `migdb`.*") {
		t.Fatal(sqlu)
	}
	if !strings.Contains(strings.Join(seen, "\n"), "FROM mysql.user") {
		t.Fatalf("应查 mysql.user：%v", seen)
	}
}

func TestImportArgsNoForce(t *testing.T) {
	s := setupMySQL(t)
	s.Defaults = filepath.Join(t.TempDir(), "missing.cnf")
	for _, a := range s.importArgs() {
		if a == "--force" {
			t.Fatal(s.importArgs())
		}
	}
	foundBatch := false
	for _, a := range s.importArgs() {
		if a == "--batch" {
			foundBatch = true
		}
	}
	if !foundBatch {
		t.Fatal(s.importArgs())
	}
}

func TestMariaDBRefuse(t *testing.T) {
	s := setupMySQL(t)
	if err := s.Refuse(context.Background()); err != nil {
		t.Fatal(err)
	}
}
