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

func (s *Service) CheckZipSources(paths []string) error {
	for _, p := range paths {
		abs, err := s.Resolve(p)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(abs); err != nil {
			return ErrNotFound
		}
		if err := s.rejectProtectedTree(abs); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ZipTo(w io.Writer, paths []string) error {
	if err := s.CheckZipSources(paths); err != nil {
		return err
	}
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

func (s *Service) Compress(paths []string, dest string, confirm bool) error {
	for _, p := range paths {
		abs, err := s.Resolve(p)
		if err != nil {
			return err
		}
		if err := s.rejectProtectedTree(abs); err != nil {
			return err
		}
	}
	abs, err := s.Resolve(dest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err == nil && !confirm {
		return ErrExists
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
	resolved, err := filepath.EvalSymlinks(absDest)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ErrNotDir
	}
	if s.ContainsProtected(resolved) {
		return ErrContains
	}
	absDest = resolved
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
	var fresh []string
	for _, f := range zr.File {
		target, err := s.safeExtract(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := mkdirFresh(target, &fresh); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Lstat(target); err == nil && !confirm {
			return ErrExists
		}
		existed := false
		if _, err := os.Lstat(target); err == nil {
			existed = true
		}
		if err := mkdirFresh(filepath.Dir(target), &fresh); err != nil {
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
		if !existed {
			fresh = append(fresh, target)
		}
	}
	ownFresh(fresh)
	return nil
}

func (s *Service) unzipTarGz(src, dest string, confirm bool) error {
	var fresh []string
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
			if err := mkdirFresh(target, &fresh); err != nil {
				return err
			}
		case tar.TypeReg:
			if _, err := os.Lstat(target); err == nil && !confirm {
				return ErrExists
			}
			existed := false
			if _, err := os.Lstat(target); err == nil {
				existed = true
			}
			if err := mkdirFresh(filepath.Dir(target), &fresh); err != nil {
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
			if !existed {
				fresh = append(fresh, target)
			}
		}
	}
	ownFresh(fresh)
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
	ancestor := existingAncestor(target)
	if resolved, err := filepath.EvalSymlinks(ancestor); err == nil {
		if !within(dest, resolved) && resolved != filepath.Clean(dest) {
			return "", ErrZipSlip
		}
		if ancestor != target {
			rest, err := filepath.Rel(ancestor, target)
			if err != nil {
				return "", err
			}
			target = filepath.Join(resolved, rest)
		}
	}
	if !within(dest, target) && target != filepath.Clean(dest) {
		return "", ErrZipSlip
	}
	if s.Forbidden(target) {
		return "", ErrForbidden
	}
	return target, nil
}

func existingAncestor(p string) string {
	for {
		if _, err := os.Lstat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

func mkdirFresh(path string, fresh *[]string) error {
	var created []string
	cur := path
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		}
		created = append(created, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	if len(created) == 0 {
		return nil
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	*fresh = append(*fresh, created...)
	return nil
}

func ownFresh(paths []string) {
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		chownWWWData(p)
		info, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			_ = os.Chmod(p, 0755)
		} else if info.Mode().IsRegular() {
			_ = os.Chmod(p, 0644)
		}
	}
}
