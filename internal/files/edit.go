package files

import (
	"bytes"
	"os"
	"path/filepath"
)

const MaxEdit = 1 << 20

func (s *Service) ReadText(path string) (string, error) {
	abs, err := s.Resolve(path)
	if err != nil {
		return "", err
	}
	st, err := os.Lstat(abs)
	if err != nil {
		return "", ErrNotFound
	}
	if st.IsDir() {
		return "", ErrIsDir
	}
	if st.Size() > MaxEdit {
		return "", ErrTooLarge
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return "", ErrBinary
	}
	return string(b), nil
}

func (s *Service) WriteText(path, content string) error {
	abs, err := s.Resolve(path)
	if err != nil {
		return err
	}
	if int64(len(content)) > MaxEdit {
		return ErrTooLarge
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
		return err
	}
	chownWWWData(abs)
	return nil
}
