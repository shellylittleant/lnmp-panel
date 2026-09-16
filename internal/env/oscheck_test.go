package env

import (
	"strings"
	"testing"
)

func TestCheckSupported(t *testing.T) {
	ok := OSInfo{ID: "ubuntu", VersionID: "24.04"}
	if err := CheckSupported(ok); err != nil {
		t.Fatal(err)
	}
	ok26 := OSInfo{ID: "ubuntu", VersionID: "26.04"}
	if err := CheckSupported(ok26); err != nil {
		t.Fatal(err)
	}
	bads := []OSInfo{
		{ID: "debian", VersionID: "12"},
		{ID: "ubuntu", VersionID: "22.04"},
		{ID: "ubuntu", VersionID: "24.10"},
		{ID: "ubuntu", VersionID: "25.04"},
	}
	for _, b := range bads {
		if err := CheckSupported(b); err == nil {
			t.Fatalf("%+v 应拒绝", b)
		}
	}
}

func TestParseOSRelease(t *testing.T) {
	in := `NAME="Ubuntu"
ID=ubuntu
VERSION_ID="24.04"
PRETTY_NAME="Ubuntu 24.04.1 LTS"
`
	info, err := ParseOSRelease(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "ubuntu" || info.VersionID != "24.04" {
		t.Fatalf("%+v", info)
	}
}
