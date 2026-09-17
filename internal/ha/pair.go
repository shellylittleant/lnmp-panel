package ha

import (
	"context"
	"fmt"
	"os"
	"strings"

	panelFS "lnmp-panel/internal/fs"
	"lnmp-panel/internal/migrate"
	"lnmp-panel/internal/mysql"
)

func (s *Service) Token() (migrate.TokenOut, error) {
	if s.Migrate == nil {
		return migrate.TokenOut{}, fmt.Errorf("迁移模块未初始化")
	}
	return s.Migrate.TokenHA()
}

func (s *Service) Accept(in AcceptIn) (AcceptOut, error) {
	if s.Migrate == nil {
		return AcceptOut{}, fmt.Errorf("迁移模块未初始化")
	}
	if err := s.Migrate.ConsumeHAToken(in.Token); err != nil {
		return AcceptOut{}, err
	}
	pub, err := s.ensureHAKey(context.Background())
	if err != nil {
		return AcceptOut{}, err
	}
	if err := migrate.UpsertHAAuthorizedKeys(s.AuthKeys, in.SSHPub, in.From); err != nil {
		return AcceptOut{}, fmt.Errorf("写入授权公钥：%w", err)
	}
	now := s.now().Format("2006-01-02T15:04:05Z")
	_ = now
	addr := in.Address
	if addr == "" {
		addr = in.From
	}
	res, err := s.db.SQL.Exec(`INSERT INTO peers(address, role, fingerprint, last_seen_at) VALUES (?,?,?,?)`,
		addr, "ha_peer", in.Fingerprint, s.now().Format("2006-01-02T15:04:05Z"))
	if err != nil {
		return AcceptOut{}, err
	}
	id, _ := res.LastInsertId()
	hostKey, _ := migrate.ReadHostKeyPub(s.HostKeyPub)
	port := s.sshListenPort()
	user := s.SSHUser
	if user == "" {
		user = "root"
	}
	token := randomHex(24)
	if s.Settings != nil {
		_ = s.Settings.Set("ha_machine_token", token)
		_ = s.Settings.Set("ha_peer_address", addr)
		_ = s.Settings.Set("ha_peer_fingerprint", in.Fingerprint)
		_ = s.Settings.Set("ha_peer_ssh_user", in.SSHUser)
		_ = s.Settings.Set("ha_peer_ssh_port", fmt.Sprintf("%d", in.SSHPort))
		if in.From != "" {
			_ = s.Settings.Set("ha_peer_ip", in.From)
		}
		if in.SSHUser == "" {
			_ = s.Settings.Set("ha_peer_ssh_user", "root")
		}
		if in.SSHPort <= 0 {
			_ = s.Settings.Set("ha_peer_ssh_port", "22")
		}
	}
	if in.SSHHostKey != "" && addr != "" {
		sshHost, err := migrate.SSHHostOf(addr)
		if err == nil {
			sp := in.SSHPort
			if sp <= 0 {
				sp = 22
			}
			_ = migrate.WriteKnownHosts(s.haKnownHosts(), sshHost, sp, in.SSHHostKey)
		}
	}
	st, _ := s.loadState()
	term := st.Term
	if term < 1 {
		term = 1
	}
	if err := s.saveRole(RoleMaster, id, term); err != nil {
		return AcceptOut{}, err
	}
	s.setDegraded(false)
	_ = s.writeMasterDropIns()
	s.applyHAFirewall(RoleMaster, s.peerIP())
	_ = s.prepareMasterMySQL(context.Background())
	fp := ""
	if s.TLSCert != "" {
		fp, _ = migrate.FingerprintPEMFile(s.TLSCert)
	}
	return AcceptOut{
		SSHUser: user, SSHPort: port, SSHHostKey: hostKey, SSHPub: pub,
		PeerID: id, Term: term, Role: RoleMaster, MachineToken: token, Fingerprint: fp,
	}, nil
}

func (s *Service) Pair(ctx context.Context, in PairIn) error {
	if s.Migrate == nil {
		return fmt.Errorf("迁移模块未初始化")
	}
	st, _ := s.loadState()
	if st.Role != RoleStandalone && st.Role != "" {
		return fmt.Errorf("已处于热备角色 %s，请先退出", st.Role)
	}
	prefix, _, err := migrate.ParseToken(in.Token)
	if err != nil {
		return err
	}
	hostPort, _, err := migrate.SplitHostPortFromAddr(in.Address)
	if err != nil {
		return err
	}
	dial := s.Migrate.DialFP
	if dial == nil {
		dial = migrate.DialFingerprint
	}
	fp, err := dial(ctx, hostPort)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(fp, prefix) {
		return fmt.Errorf("TLS 指纹与令牌不符")
	}
	if err := panelFS.Mkdir(s.Secrets, 0700); err != nil {
		return err
	}
	pub, err := s.ensureHAKey(ctx)
	if err != nil {
		return err
	}
	hostKey, _ := migrate.ReadHostKeyPub(s.HostKeyPub)
	localFP := ""
	if s.TLSCert != "" {
		localFP, _ = migrate.FingerprintPEMFile(s.TLSCert)
	}
	accIn := AcceptIn{
		Token: in.Token, SSHPub: pub, Address: s.advertisedURL(), SSHHostKey: hostKey,
		SSHPort: s.sshListenPort(), SSHUser: s.SSHUser, Fingerprint: localFP,
	}
	if accIn.SSHUser == "" {
		accIn.SSHUser = "root"
	}
	var acc AcceptOut
	if err := s.Migrate.PostPeer(ctx, in.Address, fp, prefix, "/ha/accept", accIn, &acc); err != nil {
		return err
	}
	sshHost, err := migrate.SSHHostOf(in.Address)
	if err != nil {
		return err
	}
	if acc.SSHHostKey == "" {
		return fmt.Errorf("对端未提供 SSH 主机钥")
	}
	port := acc.SSHPort
	if port <= 0 {
		port = 22
	}
	user := acc.SSHUser
	if user == "" {
		user = "root"
	}
	if err := migrate.WriteKnownHosts(s.haKnownHosts(), sshHost, port, acc.SSHHostKey); err != nil {
		return fmt.Errorf("写入 known_hosts：%w", err)
	}
	if acc.SSHPub != "" {
		_ = migrate.UpsertHAAuthorizedKeys(s.AuthKeys, acc.SSHPub, resolveHostIP(sshHost))
	}
	now := s.now().Format("2006-01-02T15:04:05Z")
	res, err := s.db.SQL.Exec(`INSERT INTO peers(address, role, fingerprint, last_seen_at) VALUES (?,?,?,?)`,
		in.Address, "ha_peer", fp, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	if s.Settings != nil {
		_ = s.Settings.Set("ha_machine_token", acc.MachineToken)
		_ = s.Settings.Set("ha_peer_address", in.Address)
		_ = s.Settings.Set("ha_peer_fingerprint", fp)
		_ = s.Settings.Set("ha_peer_ssh_user", user)
		_ = s.Settings.Set("ha_peer_ssh_port", fmt.Sprintf("%d", port))
		if ip := resolveHostIP(sshHost); ip != "" {
			_ = s.Settings.Set("ha_peer_ip", ip)
		}
	}
	term := acc.Term
	if term < 1 {
		term = 1
	}
	if err := s.saveRole(RoleStandby, id, term); err != nil {
		return err
	}
	s.setDegraded(false)
	s.removeDropIns()
	if err := s.applyNginxCatchall(); err != nil {
		return fmt.Errorf("备机 404：%w", err)
	}
	if s.MySQL != nil {
		_ = s.MySQL.WriteHACNF(mysql.ServerIDFromHost(s.hostname()), false)
		_ = s.MySQL.SetReadOnly(ctx, true)
	}
	s.applyHAFirewall(RoleStandby, s.peerIP())
	s.setDetail("已配对为备机，请点击开始同步")
	return nil
}

func (s *Service) prepareMasterMySQL(ctx context.Context) error {
	if s.MySQL == nil || !s.MySQL.Installed() {
		return nil
	}
	id := mysql.ServerIDFromHost(s.hostname())
	if err := s.MySQL.WriteHACNF(id, true); err != nil {
		return err
	}
	if !s.SkipExec {
		_ = s.MySQL.Restart(ctx)
	}
	pass, _ := s.Settings.Get("ha_repl_password")
	if pass == "" {
		pass = randomHex(12) + "Aa1!"
		_ = s.Settings.Set("ha_repl_password", pass)
	}
	if err := s.MySQL.EnsureReplUser(ctx, pass); err != nil {
		return err
	}
	return s.MySQL.SetReadOnly(ctx, false)
}

func (s *Service) PeerReplSource() (ReplSource, error) {
	st, err := s.loadState()
	if err != nil {
		return ReplSource{}, err
	}
	if st.Role != RoleMaster {
		return ReplSource{}, fmt.Errorf("本机不是主机")
	}
	if s.MySQL != nil {
		_ = s.MySQL.RestorePanelUser(context.Background())
	}
	if err := s.prepareMasterMySQL(context.Background()); err != nil {
		return ReplSource{}, err
	}
	pass, _ := s.Settings.Get("ha_repl_password")
	host := ""
	if s.Settings != nil {
		host, _ = s.Settings.Get("listen_host")
	}
	if host == "" {
		host = s.hostname()
	}
	return ReplSource{Host: host, Port: 3306, User: "lnmp_repl", Pass: pass}, nil
}

func (s *Service) PeerBundle(ctx context.Context) (migrate.Bundle, error) {
	if s.Migrate == nil {
		return migrate.Bundle{}, fmt.Errorf("迁移模块未初始化")
	}
	return s.Migrate.ExportBundle(ctx)
}

func (s *Service) leaveStandalone() error {
	s.stopSyncLocked()
	_ = s.saveRole(RoleStandalone, 0, 0)
	s.setDegraded(false)
	s.removeDropIns()
	_ = migrate.RemoveHAAuthorizedKeys(s.AuthKeys)
	_ = os.Remove(s.haKeyPath())
	_ = os.Remove(s.haKeyPath() + ".pub")
	_ = os.Remove(s.haKnownHosts())
	if s.MySQL != nil {
		_ = s.MySQL.RemoveHACNF()
		_ = s.MySQL.SetReadOnly(context.Background(), false)
		_ = s.MySQL.StopReplica(context.Background())
	}
	return nil
}
