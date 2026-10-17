package ha

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"lnmp-panel/internal/mysql"
)

func (s *Service) Promote(ctx context.Context) error {
	st, err := s.loadState()
	if err != nil {
		return err
	}
	if st.Role != RoleStandby {
		return fmt.Errorf("只有备机可以启用主模式")
	}
	if s.MySQL != nil && s.MySQL.Installed() && net.ParseIP(s.peerIP()) == nil {
		return fmt.Errorf("没有对端 IP，不能创建复制账号")
	}
	if err := s.requestPeerStepDown(ctx); err != nil && !errors.Is(err, errPeerUnreachable) {
		return fmt.Errorf("对端降级失败，已拒绝提升：%s", err.Error())
	}
	_ = s.pullAndAdoptTerm(ctx)
	st, err = s.loadState()
	if err != nil {
		return err
	}
	s.stopSyncLocked()
	if s.MySQL != nil {
		_ = s.MySQL.StopReplica(ctx)
		_ = s.MySQL.WriteHACNF(mysql.ServerIDFromHost(s.hostname()), true)
		if !s.SkipExec && s.MySQL.Installed() {
			_ = s.MySQL.Restart(ctx)
		}
		if err := s.MySQL.SetReadOnly(ctx, false); err != nil {
			return fmt.Errorf("MySQL 切可写：%w", err)
		}
	}
	term := NextTerm(st.Term, s.knownPeerTerm())
	if err := s.saveRole(RoleMaster, st.PeerID, term); err != nil {
		return err
	}
	s.setDegraded(false)
	if err := s.applyNginxSites(); err != nil {
		return fmt.Errorf("启用站点：%w", err)
	}
	_ = s.writeMasterDropIns()
	_ = s.prepareMasterMySQL(ctx)
	s.applyHAFirewall(RoleMaster, s.peerIP())
	s.setDetail("已启用主模式，请人工修改 DNS 指向本机")
	return nil
}

var errPeerUnreachable = errors.New("对端不可达")

func (s *Service) requestPeerStepDown(ctx context.Context) error {
	addr, fp, err := s.peerMeta()
	if err != nil || addr == "" || s.Migrate == nil {
		return errPeerUnreachable
	}
	token := s.machineToken()
	qctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var out map[string]any
	if err := s.Migrate.PostPeer(qctx, addr, fp, "", "/ha/peer/step-down", map[string]string{"token": token}, &out); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "请求对端失败") {
			return errPeerUnreachable
		}
		return err
	}
	return nil
}

func (s *Service) StepDown(ctx context.Context) error {
	st, err := s.loadState()
	if err != nil {
		return err
	}
	if st.Role != RoleMaster {
		return nil
	}
	s.setDegraded(true)
	if s.MySQL != nil {
		if err := s.MySQL.SetReadOnly(ctx, true); err != nil {
			return fmt.Errorf("MySQL 只读失败：%w", err)
		}
	}
	if err := s.applyNginxMaintenance(); err != nil {
		return fmt.Errorf("维护页失败：%w", err)
	}
	s.setDetail("对端正在启用主模式，本机已只读并挂维护页，请将本机设为备机")
	return nil
}

func (s *Service) Demote(ctx context.Context, _ string) error {
	st, err := s.loadState()
	if err != nil {
		return err
	}
	if st.Role == RoleStandalone {
		return fmt.Errorf("尚未配对，无法降级为备")
	}
	s.stopSyncLocked()
	if s.MySQL != nil {
		_ = s.MySQL.StopReplica(ctx)
		_ = s.MySQL.WriteHACNF(mysql.ServerIDFromHost(s.hostname()), false)
		if err := s.MySQL.SetReadOnly(ctx, true); err != nil {
			return fmt.Errorf("MySQL 只读：%w", err)
		}
	}
	if s.Settings != nil {
		_ = s.Settings.Set("ha_mysql_seeded", "")
	}
	if err := s.saveRole(RoleStandby, st.PeerID, st.Term); err != nil {
		return err
	}
	s.setDegraded(false)
	s.removeDropIns()
	if err := s.applyNginxCatchall(); err != nil {
		return fmt.Errorf("备机 404：%w", err)
	}
	s.applyHAFirewall(RoleStandby, s.peerIP())
	s.setDetail("已降级为备机，请点击开始同步")
	return nil
}

func (s *Service) SelfProtect(ctx context.Context) error {
	st, err := s.loadState()
	if err != nil {
		return err
	}
	if st.Role != RoleMaster {
		return nil
	}
	addr, fp, err := s.peerMeta()
	if err != nil || addr == "" {
		return nil
	}
	token := s.machineToken()
	if token == "" || s.Migrate == nil {
		return nil
	}
	qctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var view RoleView
	if err := s.Migrate.GetPeer(qctx, addr, fp, "/ha/peer-role", token, &view); err != nil {
		log.Printf("自我保护：联系对端失败，按原状态启动：%v", err)
		return nil
	}
	if !ShouldDegrade(st.Role, st.Term, true, view.Role, view.Term) {
		return nil
	}
	log.Printf("自我保护：对端角色=%s 任期=%d，本机任期=%d，进入只读与维护页", view.Role, view.Term, st.Term)
	s.setDegraded(true)
	if s.MySQL != nil {
		if err := s.MySQL.SetReadOnly(context.Background(), true); err != nil {
			log.Printf("自我保护：MySQL 只读失败：%v", err)
		}
	}
	if err := s.applyNginxMaintenance(); err != nil {
		log.Printf("自我保护：维护页失败：%v", err)
	}
	s.setDetail("对端已是主机，本机已只读并挂维护页，请将本机设为备机")
	return nil
}
