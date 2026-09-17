package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) ZipTo(w io.Writer, paths []string) error {
	zw := zip.NewWriter(w)
	defer zw.Close()
	for _, p := range paths {
		abs, err := s.Resolve(p)
		if err != nil {
			return err
		}
		info, err := os.Lstat(abs)
		if err != nil {
			return ErrNotFound
		}
		rootName := filepath.Base(abs)
		walkRoot := abs
		if !info.IsDir() {
			walkRoot = filepath.Dir(abs)
			rootName = ""
		}
		if err := filepath.Walk(abs, func(fp string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if s.Forbidden(fp) {
				if fi.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(walkRoot, fp)
			if err != nil {
				return err
			}
			if rootName != "" {
				rel = filepath.Join(rootName, rel)
			}
			rel = filepath.ToSlash(rel)
			if fi.IsDir() {
				_, err := zw.Create(strings.TrimSuffix(rel, "/") + "/")
				return err
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			hdr, err := zip.FileInfoHeader(fi)
			if err != nil {
				return err
			}
			hdr.Name = rel
			hdr.Method = zip.Deflate
			fw, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			f, err := os.Open(fp)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(fw, f)
			_ = f.Close()
			return copyErr
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Compress(paths []string, dest string) error {
	abs, err := s.Resolve(dest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	f, err := os.Create(abs)
	if err != nil {
		return err
	}
	err = s.ZipTo(f, paths)
	cerr := f.Close()
	if err != nil {
		_ = os.Remove(abs)
		return err
	}
	if cerr != nil {
		return cerr
	}
	_ = os.Chmod(abs, 0644)
	chownWWWData(abs)
	return nil
}

func (s *Service) Unzip(archive, dest string, confirm bool) error {
	src, err := s.Resolve(archive)
	if err != nil {
		return err
	}
	if dest == "" {
		dest = filepath.Dir(src)
	}
	absDest, err := s.Resolve(dest)
	if err != nil {
		return err
	}
	st, err := os.Lstat(src)
	if err != nil {
		return ErrNotFound
	}
	if st.IsDir() {
		return ErrIsDir
	}
	name := strings.ToLower(src)
	switch {
	case strings.HasSuffix(name, ".zip"):
		return s.unzipZip(src, absDest, confirm)
	case strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz"):
		return s.unzipTarGz(src, absDest, confirm)
	default:
		return fmt.Errorf("仅支持 zip 与 tar.gz")
	}
}

func (s *Service) unzipZip(src, dest string, confirm bool) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, err := s.safeExtract(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0755)
			continue
		}
		if _, err := os.Lstat(target); err == nil && !confirm {
			return ErrExists
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		_ = out.Close()
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	chownWWWWalk(dest)
	return nil
}

func (s *Service) unzipTarGz(src, dest string, confirm bool) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target, err := s.safeExtract(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(target, 0755)
		case tar.TypeReg:
			if _, err := os.Lstat(target); err == nil && !confirm {
				return ErrExists
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			_ = out.Close()
			if err != nil {
				return err
			}
		}
	}
	chownWWWWalk(dest)
	return nil
}

func (s *Service) safeExtract(dest, name string) (string, error) {
	name = strings.TrimPrefix(name, "/")
	name = strings.ReplaceAll(name, "\\", "/")
	target := filepath.Join(dest, name)
	target = filepath.Clean(target)
	if !within(dest, target) {
		return "", ErrZipSlip
	}
	if s.Forbidden(target) {
		return "", ErrForbidden
	}
	return target, nil
}
