package files

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Entry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Dir    bool   `json:"dir"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
	Owner  string `json:"owner"`
	Group  string `json:"group"`
	Mtime  string `json:"mtime"`
	Link   bool   `json:"link"`
	Locked bool   `json:"locked"`
}

func (s *Service) List(dir, filter string) ([]Entry, string, error) {
	abs, err := s.Resolve(dir)
	if err != nil {
		return nil, "", err
	}
	st, err := os.Lstat(abs)
	if err != nil {
		return nil, "", ErrNotFound
	}
	if !st.IsDir() {
		return nil, "", ErrNotDir
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		return nil, "", err
	}
	filter = strings.ToLower(strings.TrimSpace(filter))
	var out []Entry
	for _, e := range ents {
		if filter != "" && !strings.Contains(strings.ToLower(e.Name()), filter) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, s.toEntry(filepath.Join(abs, e.Name()), info))
	}
	if out == nil {
		out = []Entry{}
	}
	return out, abs, nil
}

func (s *Service) toEntry(p string, info os.FileInfo) Entry {
	e := Entry{
		Name:   info.Name(),
		Path:   p,
		Dir:    info.IsDir(),
		Size:   info.Size(),
		Mode:   fmt.Sprintf("%04o", info.Mode().Perm()),
		Mtime:  info.ModTime().UTC().Format(time.RFC3339),
		Link:   info.Mode()&os.ModeSymlink != 0,
		Locked: s.Forbidden(p),
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if u, err := user.LookupId(strconv.Itoa(int(st.Uid))); err == nil {
			e.Owner = u.Username
		} else {
			e.Owner = strconv.Itoa(int(st.Uid))
		}
		if g, err := user.LookupGroupId(strconv.Itoa(int(st.Gid))); err == nil {
			e.Group = g.Name
		} else {
			e.Group = strconv.Itoa(int(st.Gid))
		}
	}
	return e
}

func (s *Service) Mkdir(path string) error {
	abs, err := s.Resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return err
	}
	_ = os.Chmod(abs, 0755)
	chownWWWData(abs)
	return nil
}

func (s *Service) CreateFile(path string) error {
	abs, err := s.Resolve(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err == nil {
		return ErrExists
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_ = f.Close()
	chownWWWData(abs)
	return nil
}

func (s *Service) Delete(path string) error {
	abs, err := s.Resolve(path)
	if err != nil {
		return err
	}
	if abs == "/" {
		return ErrRoot
	}
	if err := s.rejectProtectedTree(abs); err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err != nil {
		return ErrNotFound
	}
	return os.RemoveAll(abs)
}

func (s *Service) Rename(src, dest string, confirm bool) error {
	from, err := s.Resolve(src)
	if err != nil {
		return err
	}
	to, err := s.Resolve(dest)
	if err != nil {
		return err
	}
	if from == "/" || to == "/" {
		return ErrRoot
	}
	if err := s.rejectProtectedTree(from); err != nil {
		return err
	}
	if err := s.rejectProtectedTree(to); err != nil {
		return err
	}
	if destUnderSrc(from, to) {
		return ErrIntoSelf
	}
	if _, err := os.Lstat(to); err == nil {
		if !confirm {
			return ErrExists
		}
		if err := os.RemoveAll(to); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
		return err
	}
	return os.Rename(from, to)
}

func (s *Service) Copy(src, dest string, confirm bool) error {
	from, err := s.Resolve(src)
	if err != nil {
		return err
	}
	to, err := s.Resolve(dest)
	if err != nil {
		return err
	}
	if err := s.rejectProtectedTree(from); err != nil {
		return err
	}
	if err := s.rejectProtectedTree(to); err != nil {
		return err
	}
	if destUnderSrc(from, to) {
		return ErrIntoSelf
	}
	if _, err := os.Lstat(to); err == nil {
		if !confirm {
			return ErrExists
		}
		if err := os.RemoveAll(to); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
		return err
	}
	return copyAll(from, to)
}

func destUnderSrc(src, dest string) bool {
	src, dest = filepath.Clean(src), filepath.Clean(dest)
	return dest == src || within(src, dest)
}

func copyAll(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		ents, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if err := copyAll(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}

func (s *Service) Chmod(path, owner, group string, mode os.FileMode, recursive bool) error {
	abs, err := s.Resolve(path)
	if err != nil {
		return err
	}
	uid, gid := -1, -1
	if owner != "" {
		u, err := user.Lookup(owner)
		if err != nil {
			return fmt.Errorf("用户不存在：%s", owner)
		}
		uid, _ = strconv.Atoi(u.Uid)
	}
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			return fmt.Errorf("组不存在：%s", group)
		}
		gid, _ = strconv.Atoi(g.Gid)
	}
	apply := func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if s.Forbidden(p) {
			return nil
		}
		if mode != 0 {
			_ = os.Chmod(p, mode)
		}
		if uid >= 0 || gid >= 0 {
			_ = os.Chown(p, uid, gid)
		}
		return nil
	}
	if err := s.rejectProtectedTree(abs); err != nil {
		return err
	}
	if recursive {
		return filepath.Walk(abs, apply)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return ErrNotFound
	}
	return apply(abs, info, nil)
}

func (s *Service) Users() []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	add("www-data")
	add("root")
	b, err := os.ReadFile("/etc/passwd")
	if err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			name, _, ok := strings.Cut(ln, ":")
			if ok && name != "" && !strings.HasPrefix(name, "#") {
				add(name)
			}
		}
	}
	return out
}

func (s *Service) Groups() []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	add("www-data")
	add("root")
	b, err := os.ReadFile("/etc/group")
	if err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			name, _, ok := strings.Cut(ln, ":")
			if ok && name != "" && !strings.HasPrefix(name, "#") {
				add(name)
			}
		}
	}
	return out
}
