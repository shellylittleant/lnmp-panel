package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"lnmp-panel/internal/store"
)

const (
	SessionTTL     = 2 * time.Hour
	SessionAbsMax  = 12 * time.Hour
	FailWindow     = 15 * time.Minute
	FailThreshold  = 5
	LockDuration   = 30 * time.Minute
	PendingTOTPTTL = 5 * time.Minute
	Issuer         = "LNMP面板"
)

var dummyHash string

func init() {
	h, err := HashPassword("timing-pad-not-a-real-password")
	if err == nil {
		dummyHash = h
	}
}

var (
	ErrInvalidCreds = errors.New("用户名或密码错误")
	ErrLocked       = errors.New("该 IP 已锁定，请稍后再试")
	ErrNeedTOTP     = errors.New("需要 TOTP")
	ErrNoSession    = errors.New("未登录或会话无效")
	ErrBadTOTP      = errors.New("验证码错误")
	ErrPolicy       = errors.New("密码不符合策略")
	ErrTOTPActive   = errors.New("请先关闭 TOTP 再重新绑定")
)

type Session struct {
	ID        string
	IP        string
	UserAgent string
	CSRF      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type pending struct {
	username string
	ip       string
	ua       string
	expires  time.Time
}

type Service struct {
	db     *store.DB
	mu     sync.Mutex
	pend   map[string]pending
	OnLock func(ip string)
}

func New(db *store.DB) *Service {
	return &Service{db: db, pend: map[string]pending{}}
}

func (s *Service) AdminExists() (bool, error) {
	var n int
	err := s.db.SQL.QueryRow(`SELECT COUNT(1) FROM admin WHERE id = 1`).Scan(&n)
	return n > 0, err
}

func (s *Service) EnsureAdmin(username, password string) error {
	if err := CheckPolicy(password); err != nil {
		return err
	}
	exists, err := s.AdminExists()
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("管理员已存在，拒绝覆盖")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	now := s.db.Now()
	_, err = s.db.SQL.Exec(`INSERT INTO admin(id, username, password_hash, created_at, updated_at) VALUES (1, ?, ?, ?, ?)`,
		username, hash, now, now)
	return err
}

func (s *Service) Locked(ip string) (bool, time.Time, error) {
	var until string
	err := s.db.SQL.QueryRow(`SELECT locked_until FROM ip_locks WHERE ip = ?`, ip).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, until)
	if err != nil {
		return false, time.Time{}, err
	}
	if time.Now().UTC().After(t) {
		_, _ = s.db.SQL.Exec(`DELETE FROM ip_locks WHERE ip = ?`, ip)
		return false, time.Time{}, nil
	}
	return true, t, nil
}

func (s *Service) recordAttempt(ip, username string, ok bool) error {
	_, err := s.db.SQL.Exec(`INSERT INTO login_attempts(ip, at, success, username) VALUES (?, ?, ?, ?)`,
		ip, s.db.Now(), boolInt(ok), username)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	since := time.Now().UTC().Add(-FailWindow).Format(time.RFC3339)
	var n int
	if err := s.db.SQL.QueryRow(`SELECT COUNT(1) FROM login_attempts WHERE ip = ? AND success = 0 AND at >= ?`, ip, since).Scan(&n); err != nil {
		return err
	}
	if n >= FailThreshold {
		until := time.Now().UTC().Add(LockDuration).Format(time.RFC3339)
		_, err = s.db.SQL.Exec(`INSERT INTO ip_locks(ip, locked_until, reason) VALUES (?, ?, 'login')
			ON CONFLICT(ip) DO UPDATE SET locked_until = excluded.locked_until, reason = excluded.reason`, ip, until)
		if err == nil && s.OnLock != nil {
			s.OnLock(ip)
		}
		return err
	}
	return nil
}

type LoginResult struct {
	NeedTOTP bool   `json:"need_totp"`
	Ticket   string `json:"ticket,omitempty"`
	Session  *Session
}

func (s *Service) Login(ip, ua, username, password string) (*LoginResult, error) {
	if locked, _, err := s.Locked(ip); err != nil {
		return nil, err
	} else if locked {
		return nil, ErrLocked
	}
	var hash string
	var totpOn int
	err := s.db.SQL.QueryRow(`SELECT password_hash, totp_enabled FROM admin WHERE username = ?`, username).Scan(&hash, &totpOn)
	if errors.Is(err, sql.ErrNoRows) {
		_ = CheckPassword(password, dummyHash)
		_ = s.recordAttempt(ip, username, false)
		return nil, ErrInvalidCreds
	}
	if err != nil {
		return nil, err
	}
	if !CheckPassword(password, hash) {
		_ = s.recordAttempt(ip, username, false)
		return nil, ErrInvalidCreds
	}
	if totpOn == 1 {
		ticket, err := randomHex(16)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.pend[ticket] = pending{username: username, ip: ip, ua: ua, expires: time.Now().UTC().Add(PendingTOTPTTL)}
		s.mu.Unlock()
		return &LoginResult{NeedTOTP: true, Ticket: ticket}, nil
	}
	if err := s.recordAttempt(ip, username, true); err != nil {
		return nil, err
	}
	sess, err := s.createSession(ip, ua)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Session: sess}, nil
}

func (s *Service) VerifyTOTPLogin(ip, ua, ticket, code string) (*Session, error) {
	if locked, _, err := s.Locked(ip); err != nil {
		return nil, err
	} else if locked {
		return nil, ErrLocked
	}
	s.mu.Lock()
	p, ok := s.pend[ticket]
	if ok {
		delete(s.pend, ticket)
	}
	s.mu.Unlock()
	if !ok || time.Now().UTC().After(p.expires) || p.ip != ip {
		_ = s.recordAttempt(ip, "", false)
		return nil, ErrBadTOTP
	}
	if err := s.checkTOTP(code); err != nil {
		_ = s.recordAttempt(ip, p.username, false)
		return nil, err
	}
	if err := s.recordAttempt(ip, p.username, true); err != nil {
		return nil, err
	}
	return s.createSession(ip, ua)
}

func totpOpts() totp.ValidateOpts {
	return totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	}
}

func matchingTOTPStep(code, secret string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	cur := now.Unix() / 30
	for _, step := range []int64{cur - 1, cur, cur + 1} {
		t := time.Unix(step*30, 0).UTC()
		ok, err := totp.ValidateCustom(code, secret, t, totp.ValidateOpts{
			Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && ok {
			return step, true
		}
	}
	return 0, false
}

func (s *Service) checkTOTP(code string) error {
	var secret []byte
	var on int
	var last sql.NullInt64
	if err := s.db.SQL.QueryRow(`SELECT totp_secret, totp_enabled, totp_last_step FROM admin WHERE id = 1`).Scan(&secret, &on, &last); err != nil {
		return err
	}
	if len(secret) == 0 {
		return ErrBadTOTP
	}
	step, ok := matchingTOTPStep(code, string(secret), time.Now().UTC())
	if !ok {
		return ErrBadTOTP
	}
	res, err := s.db.SQL.Exec(`UPDATE admin SET totp_last_step=? WHERE id=1 AND (totp_last_step IS NULL OR totp_last_step < ?)`, step, step)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrBadTOTP
	}
	return nil
}

func (s *Service) createSession(ip, ua string) (*Session, error) {
	id, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	sess := &Session{
		ID: id, IP: ip, UserAgent: ua, CSRF: csrf,
		CreatedAt: now, ExpiresAt: now.Add(SessionTTL),
	}
	_, err = s.db.SQL.Exec(`INSERT INTO sessions(id, ip, user_agent, csrf_token, created_at, expires_at) VALUES (?,?,?,?,?,?)`,
		sess.ID, sess.IP, sess.UserAgent, sess.CSRF,
		sess.CreatedAt.Format(time.RFC3339), sess.ExpiresAt.Format(time.RFC3339))
	return sess, err
}

func (s *Service) GetSession(id, ip string) (*Session, error) {
	var sess Session
	var created, expires string
	err := s.db.SQL.QueryRow(`SELECT id, ip, user_agent, csrf_token, created_at, expires_at FROM sessions WHERE id = ?`, id).
		Scan(&sess.ID, &sess.IP, &sess.UserAgent, &sess.CSRF, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	sess.CreatedAt, _ = time.Parse(time.RFC3339, created)
	sess.ExpiresAt, _ = time.Parse(time.RFC3339, expires)
	now := time.Now().UTC()
	if now.After(sess.CreatedAt.Add(SessionAbsMax)) || now.After(sess.ExpiresAt) {
		_, _ = s.db.SQL.Exec(`DELETE FROM sessions WHERE id = ?`, id)
		return nil, ErrNoSession
	}
	if sess.IP != ip {
		return nil, ErrNoSession
	}
	slide := now.Add(SessionTTL)
	abs := sess.CreatedAt.Add(SessionAbsMax)
	if slide.After(abs) {
		slide = abs
	}
	if slide.After(sess.ExpiresAt) {
		sess.ExpiresAt = slide
		_, _ = s.db.SQL.Exec(`UPDATE sessions SET expires_at = ? WHERE id = ?`, slide.Format(time.RFC3339), id)
	}
	return &sess, nil
}

func (s *Service) Logout(id string) error {
	_, err := s.db.SQL.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Service) ListSessions() ([]Session, error) {
	rows, err := s.db.SQL.Query(`SELECT id, ip, user_agent, csrf_token, created_at, expires_at FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	now := time.Now().UTC()
	for rows.Next() {
		var sess Session
		var created, expires string
		if err := rows.Scan(&sess.ID, &sess.IP, &sess.UserAgent, &sess.CSRF, &created, &expires); err != nil {
			return nil, err
		}
		sess.CreatedAt, _ = time.Parse(time.RFC3339, created)
		sess.ExpiresAt, _ = time.Parse(time.RFC3339, expires)
		if now.After(sess.ExpiresAt) {
			continue
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

func (s *Service) ChangePassword(old, neu string) error {
	if err := CheckPolicy(neu); err != nil {
		return err
	}
	var hash string
	if err := s.db.SQL.QueryRow(`SELECT password_hash FROM admin WHERE id = 1`).Scan(&hash); err != nil {
		return err
	}
	if !CheckPassword(old, hash) {
		return ErrInvalidCreds
	}
	nh, err := HashPassword(neu)
	if err != nil {
		return err
	}
	_, err = s.db.SQL.Exec(`UPDATE admin SET password_hash = ?, updated_at = ? WHERE id = 1`, nh, s.db.Now())
	return err
}

func (s *Service) InvalidateOtherSessions(keepID string) error {
	_, err := s.db.SQL.Exec(`DELETE FROM sessions WHERE id != ?`, keepID)
	return err
}

type TOTPSetup struct {
	Secret string `json:"secret"`
	URI    string `json:"otpauth_uri"`
}

func (s *Service) SetupTOTP() (*TOTPSetup, error) {
	var on int
	if err := s.db.SQL.QueryRow(`SELECT totp_enabled FROM admin WHERE id = 1`).Scan(&on); err != nil {
		return nil, err
	}
	if on == 1 {
		return nil, ErrTOTPActive
	}
	var username string
	if err := s.db.SQL.QueryRow(`SELECT username FROM admin WHERE id = 1`).Scan(&username); err != nil {
		return nil, err
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      Issuer,
		AccountName: username,
		Period:      30,
		SecretSize:  20,
	})
	if err != nil {
		return nil, err
	}
	_, err = s.db.SQL.Exec(`UPDATE admin SET totp_secret = ?, totp_enabled = 0, updated_at = ? WHERE id = 1`, []byte(key.Secret()), s.db.Now())
	if err != nil {
		return nil, err
	}
	return &TOTPSetup{Secret: key.Secret(), URI: key.URL()}, nil
}

func (s *Service) EnableTOTP(code string) error {
	var secret []byte
	if err := s.db.SQL.QueryRow(`SELECT totp_secret FROM admin WHERE id = 1`).Scan(&secret); err != nil {
		return err
	}
	if len(secret) == 0 {
		return fmt.Errorf("请先调用 setup")
	}
	if err := s.checkTOTP(code); err != nil {
		return err
	}
	_, err := s.db.SQL.Exec(`UPDATE admin SET totp_enabled = 1, updated_at = ? WHERE id = 1`, s.db.Now())
	return err
}

func (s *Service) DisableTOTP(password, code string) error {
	var hash string
	if err := s.db.SQL.QueryRow(`SELECT password_hash FROM admin WHERE id = 1`).Scan(&hash); err != nil {
		return err
	}
	if !CheckPassword(password, hash) {
		return ErrInvalidCreds
	}
	if err := s.checkTOTP(code); err != nil {
		return err
	}
	_, err := s.db.SQL.Exec(`UPDATE admin SET totp_enabled = 0, totp_secret = NULL, totp_last_step = 0, updated_at = ? WHERE id = 1`, s.db.Now())
	return err
}

func (s *Service) PurgePending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for k, p := range s.pend {
		if now.After(p.expires) {
			delete(s.pend, k)
		}
	}
}

func (s *Service) TOTPEnabled() (bool, error) {
	var on int
	err := s.db.SQL.QueryRow(`SELECT totp_enabled FROM admin WHERE id = 1`).Scan(&on)
	return on == 1, err
}

func (s *Service) Username() (string, error) {
	var u string
	err := s.db.SQL.QueryRow(`SELECT username FROM admin WHERE id = 1`).Scan(&u)
	return u, err
}

func (s *Service) PurgeSessions() error {
	_, err := s.db.SQL.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Service) PurgeAttempts() error {
	cut := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	_, err := s.db.SQL.Exec(`DELETE FROM login_attempts WHERE at < ?`, cut)
	return err
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
