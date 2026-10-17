package firewall

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ParseSSHPorts 从 sshd 配置文本取 Port 指令，忽略注释。
func ParseSSHPorts(content string) []int {
	var out []int
	seen := map[int]bool{}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Port") {
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil || !validPort(n) || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func DetectSSHPorts(paths ...string) []int {
	var out []int
	seen := map[int]bool{}
	add := func(ports []int) {
		for _, p := range ports {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	if len(paths) == 0 {
		paths = []string{"/etc/ssh/sshd_config"}
		if matches, err := filepath.Glob("/etc/ssh/sshd_config.d/*.conf"); err == nil {
			paths = append(paths, matches...)
		}
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		add(ParseSSHPorts(string(b)))
	}
	return out
}

// ParseListeningPorts 解析 /proc/net/tcp 或 tcp6，返回处于 LISTEN(0A) 的本地端口。
func ParseListeningPorts(content string) map[int]bool {
	out := map[int]bool{}
	sc := bufio.NewScanner(strings.NewReader(content))
	header := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if header {
			header = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if !strings.EqualFold(fields[3], "0A") {
			continue
		}
		_, portHex, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(portHex, 16, 32)
		if err != nil {
			continue
		}
		p := int(n)
		if validPort(p) {
			out[p] = true
		}
	}
	return out
}

func readListeningPorts(paths ...string) map[int]bool {
	out := map[int]bool{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for port := range ParseListeningPorts(string(b)) {
			out[port] = true
		}
	}
	return out
}
