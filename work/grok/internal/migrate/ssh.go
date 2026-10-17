package migrate

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	panelFS "lnmp-panel/internal/fs"
)

const (
	authBegin = "# LNMP-PANEL-MIGRATE-BEGIN"
	authEnd   = "# LNMP-PANEL-MIGRATE-END"
)

func restrictOpts() string {
	return "no-pty,no-agent-forwarding,no-port-forwarding,no-X11-forwarding"
}

func authKeyLine(pub, fromIP string) string {
	opts := restrictOpts()
	if ip := net.ParseIP(strings.TrimSpace(fromIP)); ip != nil {
		return fmt.Sprintf("from=%q,%s %s", ip.String(), opts, pub)
	}
	return opts + " " + pub
}

func UpsertHAAuthorizedKeys(path, pub, fromIP string) error {
	return upsertAuthorizedKeysMarked(path, pub, fromIP, haAuthBegin, haAuthEnd)
}

func RemoveHAAuthorizedKeys(path string) error {
	return removeAuthorizedKeysMarked(path, haAuthBegin, haAuthEnd)
}

func upsertAuthorizedKeys(path, pub, fromIP string) error {
	return upsertAuthorizedKeysMarked(path, pub, fromIP, authBegin, authEnd)
}

func removeAuthorizedKeys(path string) error {
	return removeAuthorizedKeysMarked(path, authBegin, authEnd)
}

const (
	haAuthBegin = "# LNMP-PANEL-HA-BEGIN"
	haAuthEnd   = "# LNMP-PANEL-HA-END"
)

func upsertAuthorizedKeysMarked(path, pub, fromIP, begin, end string) error {
	pub = strings.TrimSpace(pub)
	if pub == "" {
		return fmt.Errorf("空公钥")
	}
	if strings.ContainsAny(pub, "\n\r") {
		return fmt.Errorf("公钥含换行")
	}
	existing := ""
	if b, err := os.ReadFile(path); err == nil {
		existing = string(b)
	}
	outside := stripAuthBlockMarked(existing, begin, end)
	var b strings.Builder
	b.WriteString(outside)
	if outside != "" && !strings.HasSuffix(outside, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(begin + "\n")
	b.WriteString(authKeyLine(pub, fromIP) + "\n")
	b.WriteString(end + "\n")
	if err := panelFS.Mkdir(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return panelFS.WriteAtomic(path, []byte(b.String()), 0600)
}

func removeAuthorizedKeysMarked(path, begin, end string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	out := stripAuthBlockMarked(string(b), begin, end)
	if strings.TrimSpace(out) == "" {
		return os.Remove(path)
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return panelFS.WriteAtomic(path, []byte(out), 0600)
}

func stripAuthBlock(s string) string {
	return stripAuthBlockMarked(s, authBegin, authEnd)
}

func stripAuthBlockMarked(s, begin, end string) string {
	var out []string
	in := false
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		ln := sc.Text()
		if strings.TrimSpace(ln) == begin {
			in = true
			continue
		}
		if strings.TrimSpace(ln) == end {
			in = false
			continue
		}
		if !in {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}

func WriteKnownHosts(path, sshHost string, sshPort int, hostKeyLine string) error {
	return writeKnownHosts(path, sshHost, sshPort, hostKeyLine)
}

func SSHHostOf(address string) (string, error) {
	return sshHostOf(address)
}

func ReadHostKeyPub(path string) (string, error) {
	return readHostKeyPub(path)
}

func writeKnownHosts(path, sshHost string, sshPort int, hostKeyLine string) error {
	hostKeyLine = strings.TrimSpace(hostKeyLine)
	if hostKeyLine == "" {
		return fmt.Errorf("空主机钥")
	}
	if strings.ContainsAny(hostKeyLine, "\n\r") {
		return fmt.Errorf("主机钥含换行")
	}
	sshHost = strings.TrimSpace(sshHost)
	if sshHost == "" {
		return fmt.Errorf("空 SSH 主机")
	}
	if sshPort <= 0 {
		sshPort = 22
	}
	if err := panelFS.Mkdir(filepath.Dir(path), 0700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(sshHost + " " + hostKeyLine + "\n")
	b.WriteString(fmt.Sprintf("[%s]:%d %s\n", sshHost, sshPort, hostKeyLine))
	return panelFS.WriteAtomic(path, []byte(b.String()), 0600)
}

func sshHostOf(address string) (string, error) {
	hp, _, err := SplitHostPortFromAddr(address)
	if err != nil {
		return "", err
	}
	host, _, err := net.SplitHostPort(hp)
	if err != nil {
		return hp, nil
	}
	return host, nil
}

func readHostKeyPub(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
