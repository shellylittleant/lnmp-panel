package site

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
var phpVerRe = regexp.MustCompile(`^8\.([2-9]|[1-9][0-9]+)$`)

func validDomain(d string) error {
	d = strings.ToLower(strings.TrimSpace(d))
	if !domainRe.MatchString(d) {
		return fmt.Errorf("无效域名：%s", d)
	}
	return nil
}

func injectChars(s string) bool {
	if strings.ContainsAny(s, ";{}\n\r") {
		return true
	}
	for _, r := range s {
		if unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func validToken(label, s string) error {
	if s == "" {
		return nil
	}
	if injectChars(s) {
		return fmt.Errorf("%s 含非法字符", label)
	}
	return nil
}

func validRoot(root string) error {
	if root == "" {
		return fmt.Errorf("根目录不能为空")
	}
	if !filepath.IsAbs(root) {
		return fmt.Errorf("根目录必须是绝对路径")
	}
	if filepath.Clean(root) != root {
		return fmt.Errorf("根目录路径不合法")
	}
	if injectChars(root) {
		return fmt.Errorf("根目录含非法字符")
	}
	return nil
}

func (s *Service) underWWW(root string) bool {
	clean := filepath.Clean(root)
	www := filepath.Clean(s.WWW)
	if clean == www {
		return false
	}
	rel, err := filepath.Rel(www, clean)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
