package files

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	ErrForbidden      = errors.New("禁止操作面板密钥或数据库文件")
	ErrRoot           = errors.New("不能删除文件系统根")
	ErrNotFound       = errors.New("路径不存在")
	ErrExists         = errors.New("目标已存在，请确认覆盖")
	ErrTooLarge       = errors.New("超过 1 MB，请下载后编辑")
	ErrUploadTooLarge = errors.New("超过 2 GB 上限")
	ErrBinary         = errors.New("不是文本文件")
	ErrZipSlip        = errors.New("压缩包内路径超出目标目录")
	ErrNotDir         = errors.New("不是目录")
	ErrIsDir          = errors.New("这是目录")
	ErrContains       = errors.New("该目录包含面板密钥或数据库，请缩小范围")
	ErrIntoSelf       = errors.New("不能复制或移动到自身或其子目录")
)

type Service struct {
	DataDir  string
	Secrets  string
	EtcDir   string
	WWW      string
	PanelKey string
}

func New() *Service {
	return &Service{
		DataDir:  "/var/lib/lnmp-panel",
		Secrets:  "/var/lib/lnmp-panel/secrets",
		EtcDir:   "/etc/lnmp-panel",
		WWW:      "/var/www",
		PanelKey: "/var/lib/lnmp-panel/tls/panel.key",
	}
}

func (s *Service) Resolve(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		p = s.WWW
		if p == "" {
			p = "/var/www"
		}
	}
	if !filepath.IsAbs(p) {
		p = "/" + strings.TrimPrefix(p, "/")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if s.Forbidden(abs) {
		return "", ErrForbidden
	}
	return abs, nil
}

func (s *Service) Forbidden(abs string) bool {
	eval := canon(abs)
	for _, d := range s.deny() {
		if d == "" {
			continue
		}
		d = canon(d)
		if eval == d || strings.HasPrefix(eval, d+string(os.PathSeparator)) {
			return true
		}
	}
	base := filepath.Base(eval)
	if base == "panel.db" || strings.HasPrefix(base, "panel.db-") {
		data := canon(s.DataDir)
		if data != "" && (eval == filepath.Join(data, base) || strings.HasPrefix(eval, data+string(os.PathSeparator))) {
			return true
		}
	}
	return false
}

func (s *Service) deny() []string {
	out := []string{s.Secrets, s.EtcDir, s.PanelKey}
	if s.DataDir != "" {
		out = append(out, filepath.Join(s.DataDir, "secrets"))
		out = append(out, filepath.Join(s.DataDir, "tls", "panel.key"))
	}
	return out
}

// ContainsProtected 为真表示 path 本身是例外，或例外路径位于其下（父目录整棵操作会带出密钥/数据库）。
func (s *Service) ContainsProtected(abs string) bool {
	abs = canon(abs)
	if abs == "" || abs == "/" {
		return true
	}
	if s.Forbidden(abs) {
		return true
	}
	for _, d := range s.protectedLeaves() {
		if d == "" {
			continue
		}
		d = canon(d)
		if within(abs, d) {
			return true
		}
	}
	return false
}

func (s *Service) protectedLeaves() []string {
	out := s.deny()
	if s.DataDir != "" {
		data := s.DataDir
		out = append(out,
			filepath.Join(data, "panel.db"),
			filepath.Join(data, "panel.db-wal"),
			filepath.Join(data, "panel.db-shm"),
		)
	}
	return out
}

func (s *Service) rejectProtectedTree(abs string) error {
	if s.ContainsProtected(abs) {
		return ErrContains
	}
	return nil
}

func canon(p string) string {
	p = filepath.Clean(p)
	if p == "" {
		return p
	}
	if e, err := filepath.EvalSymlinks(p); err == nil {
		return e
	}
	if e, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(e, filepath.Base(p))
	}
	if a, err := filepath.Abs(p); err == nil {
		return filepath.Clean(a)
	}
	return p
}

func within(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

func chownWWWData(path string) {
	u, err := user.Lookup("www-data")
	if err != nil {
		return
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return
	}
	_ = os.Chown(path, uid, gid)
}
