package mysql

import (
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lnmp-panel/internal/exec"
	panelFS "lnmp-panel/internal/fs"
)

const HACNF = "/etc/mysql/mysql.conf.d/z-lnmp-panel-ha.cnf"

type ReplicaStatus struct {
	IORunning  bool   `json:"io_running"`
	SQLRunning bool   `json:"sql_running"`
	SourceHost string `json:"source_host"`
	LagSec     int    `json:"lag_sec"`
	LastError  string `json:"last_error"`
	OK         bool   `json:"ok"`
}

func ServerIDFromHost(host string) int {
	if host == "" {
		host = "lnmp"
	}
	id := int(crc32.ChecksumIEEE([]byte(host))%900000) + 1000
	if id <= 0 {
		id = 1001
	}
	return id
}

func (s *Service) WriteHACNF(serverID int, asMaster bool) error {
	return s.writeHACNF(serverID, asMaster, !asMaster)
}

// WriteHABaseCNF 写 GTID/server-id，不落只读。首次导入全库前必须可写。
func (s *Service) WriteHABaseCNF(serverID int, asMaster bool) error {
	return s.writeHACNF(serverID, asMaster, false)
}

func (s *Service) writeHACNF(serverID int, asMaster, persistRO bool) error {
	if serverID <= 0 {
		serverID = 1001
	}
	bind := "127.0.0.1"
	if asMaster {
		bind = "0.0.0.0"
	}
	body := fmt.Sprintf(`[mysqld]
server-id = %d
gtid_mode = ON
enforce_gtid_consistency = ON
log_bin = mysql-bin
binlog_format = ROW
bind-address = %s
`, serverID, bind)
	if persistRO {
		body += "read_only = ON\nsuper_read_only = ON\n"
	}
	if err := panelFS.Mkdir("/etc/mysql/mysql.conf.d", 0755); err != nil {
		return err
	}
	return panelFS.WriteAtomic(HACNF, []byte(body), 0644)
}

func (s *Service) RemoveHACNF() error {
	return os.Remove(HACNF)
}

func (s *Service) SetReadOnly(ctx context.Context, on bool) error {
	if s.SkipExec {
		return nil
	}
	if !s.Installed() {
		return nil
	}
	var sqlText string
	if on {
		sqlText = "SET GLOBAL read_only=ON; SET GLOBAL super_read_only=ON;"
	} else {
		sqlText = "SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF;"
	}
	if _, err := s.Exec(ctx, sqlText); err != nil {
		_, err = s.execRoot(ctx, sqlText)
		return err
	}
	return nil
}

func (s *Service) EnsureReplUser(ctx context.Context, pass string) error {
	if s.SkipExec {
		return nil
	}
	if err := ValidIdent("lnmp_repl"); err != nil {
		return err
	}
	stmt := "CREATE USER IF NOT EXISTS 'lnmp_repl'@'%' IDENTIFIED BY " + quoteSQL(pass) +
		"; ALTER USER 'lnmp_repl'@'%' IDENTIFIED BY " + quoteSQL(pass) +
		"; GRANT REPLICATION SLAVE, REPLICATION CLIENT, BACKUP_ADMIN ON *.* TO 'lnmp_repl'@'%'; FLUSH PRIVILEGES;"
	if _, err := s.Exec(ctx, stmt); err != nil {
		_, err = s.execRoot(ctx, stmt)
		return err
	}
	return nil
}

func (s *Service) StopReplica(ctx context.Context) error {
	if s.SkipExec {
		return nil
	}
	if !s.Installed() {
		return nil
	}
	_, err := s.Exec(ctx, "STOP REPLICA; RESET REPLICA ALL;")
	if err != nil {
		_, err = s.Exec(ctx, "STOP SLAVE; RESET SLAVE ALL;")
	}
	return err
}

func (s *Service) ReplicaStatus(ctx context.Context) (ReplicaStatus, error) {
	var st ReplicaStatus
	if s.SkipExec || !s.Installed() {
		return st, nil
	}
	out, err := s.execRaw(ctx, "SHOW REPLICA STATUS\\G")
	if err != nil {
		out, err = s.execRaw(ctx, "SHOW SLAVE STATUS\\G")
		if err != nil {
			return st, err
		}
	}
	kv := parseShowG(out)
	st.IORunning = isYes(kv["Replica_IO_Running"]) || isYes(kv["Slave_IO_Running"])
	st.SQLRunning = isYes(kv["Replica_SQL_Running"]) || isYes(kv["Slave_SQL_Running"])
	st.SourceHost = firstNonEmpty(kv["Source_Host"], kv["Master_Host"])
	st.LastError = firstNonEmpty(kv["Last_Error"], kv["Last_SQL_Error"], kv["Last_IO_Error"])
	if v := firstNonEmpty(kv["Seconds_Behind_Source"], kv["Seconds_Behind_Master"]); v != "" && v != "NULL" {
		fmt.Sscanf(v, "%d", &st.LagSec)
	}
	st.OK = st.IORunning && st.SQLRunning && st.LastError == ""
	return st, nil
}

func isYes(v string) bool {
	return strings.EqualFold(strings.TrimSpace(v), "Yes")
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func parseShowG(raw string) map[string]string {
	out := map[string]string{}
	for _, ln := range strings.Split(raw, "\n") {
		ln = strings.TrimSpace(ln)
		k, v, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

func (s *Service) GTIDMode(ctx context.Context) string {
	out, err := s.Exec(ctx, "SELECT @@GLOBAL.GTID_MODE")
	if err != nil {
		out, _ = s.execRoot(ctx, "SELECT @@GLOBAL.GTID_MODE")
	}
	v := strings.TrimSpace(out)
	if i := strings.LastIndex(v, "\n"); i >= 0 {
		v = strings.TrimSpace(v[i+1:])
	}
	return v
}

func (s *Service) GTIDExecuted(ctx context.Context) string {
	out, err := s.Exec(ctx, "SELECT @@GLOBAL.GTID_EXECUTED")
	if err != nil {
		out, _ = s.execRoot(ctx, "SELECT @@GLOBAL.GTID_EXECUTED")
	}
	v := strings.TrimSpace(out)
	if i := strings.LastIndex(v, "\n"); i >= 0 {
		v = strings.TrimSpace(v[i+1:])
	}
	if v == "" || strings.Contains(v, "GTID_EXECUTED") || strings.HasPrefix(v, "+") {
		return ""
	}
	return v
}

func (s *Service) SetupReplica(ctx context.Context, host string, port int, user, pass string) error {
	if s.SkipExec {
		return nil
	}
	if port <= 0 {
		port = 3306
	}
	if err := ValidHost(host); err != nil {
		return err
	}
	if err := ValidIdent(user); err != nil {
		return err
	}
	stmt := fmt.Sprintf(
		"STOP REPLICA; RESET REPLICA ALL; CHANGE REPLICATION SOURCE TO SOURCE_HOST=%s, SOURCE_PORT=%d, SOURCE_USER=%s, SOURCE_PASSWORD=%s, SOURCE_AUTO_POSITION=1, GET_SOURCE_PUBLIC_KEY=1, SOURCE_SSL=1; START REPLICA;",
		quoteSQL(host), port, quoteSQL(user), quoteSQL(pass),
	)
	if err := s.execOrRoot(ctx, stmt); err != nil {
		stmt = fmt.Sprintf(
			"STOP SLAVE; RESET SLAVE ALL; CHANGE MASTER TO MASTER_HOST=%s, MASTER_PORT=%d, MASTER_USER=%s, MASTER_PASSWORD=%s, MASTER_AUTO_POSITION=1, GET_MASTER_PUBLIC_KEY=1, MASTER_SSL=1; START SLAVE;",
			quoteSQL(host), port, quoteSQL(user), quoteSQL(pass),
		)
		return s.execOrRoot(ctx, stmt)
	}
	return nil
}

func (s *Service) execOrRoot(ctx context.Context, sqlText string) error {
	if _, err := s.Exec(ctx, sqlText); err == nil {
		return nil
	}
	_, err := s.execRoot(ctx, sqlText)
	return err
}

func (s *Service) CloneFrom(ctx context.Context, host string, port int, user, pass string) error {
	if s.SkipExec {
		return nil
	}
	if port <= 0 {
		port = 3306
	}
	if err := ValidHost(host); err != nil {
		return err
	}
	if err := ValidIdent(user); err != nil {
		return err
	}
	_ = s.execOrRoot(ctx, "INSTALL PLUGIN clone SONAME 'mysql_clone.so';")
	donor := fmt.Sprintf("%s:%d", host, port)
	if err := s.execOrRoot(ctx, "SET GLOBAL clone_valid_donor_list="+quoteSQL(donor)); err != nil {
		return err
	}
	stmt := fmt.Sprintf("CLONE INSTANCE FROM %s@%s:%d IDENTIFIED BY %s;", quoteSQL(user), host, port, quoteSQL(pass))
	if _, err := s.execLong(ctx, 30*time.Minute, stmt); err != nil {
		_, err = s.execRoot(ctx, stmt)
		return err
	}
	return nil
}

func (s *Service) DumpAllForReplica(ctx context.Context, w io.Writer) error {
	if s.SkipExec {
		_, _ = io.WriteString(w, "-- skip-replica-dump\n")
		return nil
	}
	if err := s.Refuse(ctx); err != nil {
		return err
	}
	args := s.clientArgs("/usr/bin/mysqldump")
	args = append(args, "--single-transaction", "--routines", "--events", "--triggers",
		"--all-databases", "--set-gtid-purged=ON", "--source-data=2")
	r, err := exec.RunTo(ctx, 30*time.Minute, nil, w, args...)
	if err != nil {
		return fmt.Errorf("mysqldump 全库：%s", exec.Clip(r.Stderr))
	}
	return nil
}

func (s *Service) execRaw(ctx context.Context, sqlText string) (string, error) {
	args := s.clientArgs("/usr/bin/mysql")
	if s.SkipExec && s.IORunner == nil {
		return "", nil
	}
	r, err := s.runIO()(ctx, 30*time.Second, strings.NewReader(sqlText), args...)
	if s.SkipExec {
		return r.Stdout, nil
	}
	if err != nil {
		return "", fmt.Errorf("mysql：%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	return r.Stdout, nil
}

func (s *Service) execLong(ctx context.Context, d time.Duration, sqlText string) (string, error) {
	args := append(s.clientArgs("/usr/bin/mysql"), "-N")
	if s.SkipExec && s.IORunner == nil {
		return "", nil
	}
	r, err := s.runIO()(ctx, d, strings.NewReader(sqlText), args...)
	if s.SkipExec {
		return r.Stdout, nil
	}
	if err != nil {
		return "", fmt.Errorf("mysql：%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	return r.Stdout, nil
}

func (s *Service) ResetServerUUID(ctx context.Context) error {
	if s.SkipExec {
		return nil
	}
	_ = s.admin(ctx, "shutdown")
	_ = os.Remove("/var/lib/mysql/auto.cnf")
	if err := s.startDaemon(ctx); err != nil {
		if exec.SystemdLive() {
			_, _ = s.run()(ctx, 30*time.Second, "/usr/bin/systemctl", "start", "mysql")
		} else {
			return err
		}
	}
	return s.waitReady(ctx)
}

func (s *Service) RestorePanelUser(ctx context.Context) error {
	if s.SkipExec {
		return nil
	}
	if err := s.admin(ctx, "ping"); err == nil {
		return nil
	}
	_, _ = s.execRoot(ctx, "SET GLOBAL super_read_only=OFF; SET GLOBAL read_only=OFF;")
	pass := s.clientPassword()
	if pass != "" {
		stmt := "CREATE USER IF NOT EXISTS 'lnmp_panel'@'localhost' IDENTIFIED BY " + quoteSQL(pass) +
			"; ALTER USER 'lnmp_panel'@'localhost' IDENTIFIED BY " + quoteSQL(pass) +
			"; GRANT ALL ON *.* TO 'lnmp_panel'@'localhost' WITH GRANT OPTION; FLUSH PRIVILEGES;"
		if _, err := s.execRoot(ctx, stmt); err == nil {
			if err := s.admin(ctx, "ping"); err == nil {
				return nil
			}
		}
	}
	return s.EnsurePanelUser(ctx)
}

func (s *Service) execRoot(ctx context.Context, sqlText string) (string, error) {
	if s.SkipExec && s.IORunner == nil {
		return "", nil
	}
	r, err := s.runIO()(ctx, 20*time.Second, strings.NewReader(sqlText), "/usr/bin/mysql", "-uroot", "-N", "-B")
	if s.SkipExec {
		return r.Stdout, nil
	}
	if err != nil {
		return "", fmt.Errorf("mysql root：%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	return r.Stdout, nil
}

func (s *Service) PanelPassword() string {
	return s.clientPassword()
}

func (s *Service) WritePanelCNF(pass string) error {
	if strings.TrimSpace(pass) == "" {
		return fmt.Errorf("面板密码为空")
	}
	if err := panelFS.Mkdir(filepath.Dir(s.Defaults), 0700); err != nil {
		return err
	}
	sock := s.Socket
	if sock == "" {
		sock = "/var/run/mysqld/mysqld.sock"
	}
	body := fmt.Sprintf("[client]\nuser=lnmp_panel\npassword=%q\nsocket=%s\n", pass, sock)
	return panelFS.WriteAtomic(s.Defaults, []byte(body), 0600)
}

func (s *Service) clientPassword() string {
	b, err := os.ReadFile(s.Defaults)
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		k, v, ok := strings.Cut(ln, "=")
		if !ok || strings.TrimSpace(k) != "password" {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		return v
	}
	return ""
}
