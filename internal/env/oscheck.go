package env

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type OSInfo struct {
	ID        string
	VersionID string
	Pretty    string
}

func ReadOSRelease(path string) (OSInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return OSInfo{}, fmt.Errorf("无法读取 %s：%w", path, err)
	}
	defer f.Close()
	return ParseOSRelease(f)
}

func ParseOSRelease(r io.Reader) (OSInfo, error) {
	var info OSInfo
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		switch k {
		case "ID":
			info.ID = v
		case "VERSION_ID":
			info.VersionID = v
		case "PRETTY_NAME":
			info.Pretty = v
		}
	}
	if err := sc.Err(); err != nil {
		return OSInfo{}, err
	}
	return info, nil
}

// CheckSupported：仅 Ubuntu LTS，VERSION_ID 为偶数年的 .04 且 ≥ 24.04。
func CheckSupported(info OSInfo) error {
	if info.ID != "ubuntu" {
		return fmt.Errorf("只支持 Ubuntu LTS（当前 ID=%s）", info.ID)
	}
	parts := strings.Split(info.VersionID, ".")
	if len(parts) != 2 {
		return fmt.Errorf("无法解析 VERSION_ID=%s", info.VersionID)
	}
	year, err1 := strconv.Atoi(parts[0])
	month, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return fmt.Errorf("无法解析 VERSION_ID=%s", info.VersionID)
	}
	if month != 4 || year%2 != 0 {
		return fmt.Errorf("不支持非 LTS 版本 %s（只要偶数年的 .04）", info.VersionID)
	}
	if year < 24 {
		return fmt.Errorf("Ubuntu %s 低于下限 24.04 LTS", info.VersionID)
	}
	return nil
}

func MustCurrentOS() (OSInfo, error) {
	info, err := ReadOSRelease("/etc/os-release")
	if err != nil {
		return OSInfo{}, err
	}
	return info, CheckSupported(info)
}
