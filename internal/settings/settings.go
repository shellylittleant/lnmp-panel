package settings

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"strings"

	"lnmp-panel/internal/store"
)

type Service struct {
	db *store.DB
}

func New(db *store.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Get(key string) (string, error) {
	var v string
	err := s.db.SQL.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Service) Set(key, value string) error {
	_, err := s.db.SQL.Exec(`INSERT INTO settings(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

type Public struct {
	BindMode        string   `json:"bind_mode"`
	OpsIPCIDRs      []string `json:"ops_ip_cidrs"`
	ListenPort      string   `json:"listen_port"`
	PathPrefix      string   `json:"path_prefix"`
	MetricsRawDays  string   `json:"metrics_raw_days"`
	MetricsHourDays string   `json:"metrics_hourly_days"`
	LogKeepDays     string   `json:"log_keep_days"`
	TOTPSuggested   bool     `json:"totp_suggested"`
}

func (s *Service) Public() (Public, error) {
	keys := []string{"bind_mode", "ops_ip_cidrs", "listen_port", "path_prefix", "metrics_raw_days", "metrics_hourly_days", "log_keep_days", "totp_suggested"}
	m := map[string]string{}
	for _, k := range keys {
		v, err := s.Get(k)
		if err != nil {
			return Public{}, err
		}
		m[k] = v
	}
	var cidrs []string
	if m["ops_ip_cidrs"] != "" {
		_ = json.Unmarshal([]byte(m["ops_ip_cidrs"]), &cidrs)
	}
	if cidrs == nil {
		cidrs = []string{}
	}
	return Public{
		BindMode:        or(m["bind_mode"], "public"),
		OpsIPCIDRs:      cidrs,
		ListenPort:      m["listen_port"],
		PathPrefix:      m["path_prefix"],
		MetricsRawDays:  or(m["metrics_raw_days"], "7"),
		MetricsHourDays: or(m["metrics_hourly_days"], "90"),
		LogKeepDays:     or(m["log_keep_days"], "14"),
		TOTPSuggested:   m["totp_suggested"] == "1" || m["totp_suggested"] == "true",
	}, nil
}

type Update struct {
	BindMode   *string  `json:"bind_mode"`
	OpsIPCIDRs []string `json:"ops_ip_cidrs"`
}

func (s *Service) Apply(u Update) error {
	if u.BindMode != nil {
		v := *u.BindMode
		if v != "public" && v != "localhost" {
			return errors.New("bind_mode 只能是 public 或 localhost")
		}
		if err := s.Set("bind_mode", v); err != nil {
			return err
		}
	}
	if u.OpsIPCIDRs != nil {
		for _, c := range u.OpsIPCIDRs {
			if _, _, err := net.ParseCIDR(strings.TrimSpace(c)); err != nil {
				if ip := net.ParseIP(strings.TrimSpace(c)); ip == nil {
					return errors.New("无效的运维 IP/CIDR：" + c)
				}
			}
		}
		b, _ := json.Marshal(u.OpsIPCIDRs)
		if err := s.Set("ops_ip_cidrs", string(b)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) IPAllowed(ip string) (bool, error) {
	raw, err := s.Get("ops_ip_cidrs")
	if err != nil {
		return false, err
	}
	if raw == "" || raw == "[]" {
		return true, nil
	}
	var cidrs []string
	if err := json.Unmarshal([]byte(raw), &cidrs); err != nil {
		return false, err
	}
	if len(cidrs) == 0 {
		return true, nil
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false, nil
	}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if ipn := net.ParseIP(c); ipn != nil && ipn.Equal(parsed) {
			return true, nil
		}
		_, n, err := net.ParseCIDR(c)
		if err == nil && n.Contains(parsed) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) IsOpsIP(ip string) (bool, error) {
	raw, err := s.Get("ops_ip_cidrs")
	if err != nil {
		return false, err
	}
	if raw == "" || raw == "[]" {
		return false, nil
	}
	return s.IPAllowed(ip)
}

func or(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
