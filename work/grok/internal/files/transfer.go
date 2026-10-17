package files

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const MaxUpload = 2 << 30

func looksLikeFilePath(p string) bool {
	ext := filepath.Ext(filepath.Base(p))
	return ext != "" && ext != "."
}

// UploadPathIsFile：path 指向已存在的文件，或不存在但带扩展名，则按目标文件处理。
func (s *Service) UploadPathIsFile(path string) (bool, error) {
	_, _, asFile, err := s.uploadPathInfo(path)
	return asFile, err
}

func (s *Service) uploadPathInfo(path string) (abs string, forceDir, asFile bool, err error) {
	path = strings.TrimSpace(path)
	forceDir = strings.HasSuffix(path, "/")
	if path == "" {
		path = s.WWW
		if path == "" {
			path = "/var/www"
		}
	}
	if !filepath.IsAbs(path) {
		path = "/" + strings.TrimPrefix(path, "/")
	}
	abs, err = filepath.Abs(path)
	if err != nil {
		return "", forceDir, false, err
	}
	abs = filepath.Clean(abs)
	st, err := os.Lstat(abs)
	switch {
	case err == nil:
		return abs, forceDir, !st.IsDir(), nil
	case os.IsNotExist(err):
		return abs, forceDir, !forceDir && looksLikeFilePath(abs), nil
	default:
		return "", forceDir, false, err
	}
}

func (s *Service) ResolveUploadDest(path, filename string) (string, error) {
	abs, _, asFile, err := s.uploadPathInfo(path)
	if err != nil {
		return "", err
	}
	dest := abs
	if !asFile {
		name := filepath.Base(filename)
		if name == "" || name == "." || name == string(os.PathSeparator) {
			return "", fmt.Errorf("缺少文件名")
		}
		dest = filepath.Join(abs, name)
	}
	return s.Resolve(dest)
}

func (s *Service) SaveUpload(dest string, r io.Reader) (int64, error) {
	abs, err := s.Resolve(dest)
	if err != nil {
		return 0, err
	}
	if abs == "/" {
		return 0, ErrRoot
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".lnmp-upload-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	n, err := io.Copy(tmp, io.LimitReader(r, MaxUpload+1))
	_ = tmp.Close()
	if err != nil || n > MaxUpload {
		_ = os.Remove(tmpName)
		if n > MaxUpload {
			return 0, ErrUploadTooLarge
		}
		return 0, err
	}
	_ = os.Chmod(tmpName, 0644)
	if err := os.Rename(tmpName, abs); err != nil {
		_ = os.Remove(tmpName)
		return 0, err
	}
	chownWWWData(abs)
	return n, nil
}

func (s *Service) OpenFile(path string) (*os.File, os.FileInfo, error) {
	abs, err := s.Resolve(path)
	if err != nil {
		return nil, nil, err
	}
	st, err := os.Lstat(abs)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	if st.IsDir() {
		return nil, nil, ErrIsDir
	}
	if st.Mode()&os.ModeSymlink != 0 && s.Forbidden(canon(abs)) {
		return nil, nil, ErrForbidden
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, nil, err
	}
	return f, st, nil
}
