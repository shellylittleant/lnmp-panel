package files

import (
	"io"
	"os"
	"path/filepath"
)

const MaxUpload = 2 << 30

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
