package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrTraverse  = errors.New("禁止路径穿越")
	ErrOutOfRoot = errors.New("路径超出允许根")
	ErrForbidden = errors.New("禁止访问面板密钥或数据库文件")
)

func Confine(root, rel string, deny []string) (string, error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" {
		rel = "."
	}
	if strings.ContainsRune(rel, 0) {
		return "", ErrTraverse
	}
	if hasDotDotSegment(rel) {
		return "", ErrTraverse
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "~") || strings.Contains(rel, ":") {
		return "", ErrOutOfRoot
	}
	rootAbs := canon(root)
	full := filepath.Clean(filepath.Join(rootAbs, rel))
	if !within(rootAbs, full) {
		return "", ErrOutOfRoot
	}
	denyCanon := make([]string, 0, len(deny))
	for _, d := range deny {
		if strings.TrimSpace(d) == "" {
			continue
		}
		denyCanon = append(denyCanon, canon(d))
	}
	if denied(full, denyCanon) {
		return "", ErrForbidden
	}
	check := full
	if st, err := os.Lstat(full); err != nil {
		if os.IsNotExist(err) {
			check = filepath.Dir(full)
		}
	} else if st.Mode()&os.ModeSymlink != 0 {
		check = full
	}
	if eval, err := filepath.EvalSymlinks(check); err == nil {
		if !within(rootAbs, eval) {
			return "", ErrOutOfRoot
		}
		if denied(eval, denyCanon) {
			return "", ErrForbidden
		}
	}
	return full, nil
}

func hasDotDotSegment(rel string) bool {
	rel = strings.ReplaceAll(rel, "\\", "/")
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func canon(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if e, err := filepath.EvalSymlinks(a); err == nil {
		return e
	}
	if e, err := filepath.EvalSymlinks(filepath.Dir(a)); err == nil {
		return filepath.Join(e, filepath.Base(a))
	}
	return filepath.Clean(a)
}

func within(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(path, root+sep)
}

func denied(path string, deny []string) bool {
	path = filepath.Clean(path)
	for _, d := range deny {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		d = filepath.Clean(d)
		if path == d || strings.HasPrefix(path, d+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func DefaultDeny(dataDir, secretsDir string) []string {
	out := []string{"/etc/lnmp-panel", "/var/lib/lnmp-panel"}
	if dataDir != "" {
		out = append(out, dataDir)
	}
	if secretsDir != "" {
		out = append(out, secretsDir)
	}
	return out
}
