package webui

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed all:dist
var dist embed.FS

func FS() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}

func IndexHTML(baseHREF string) ([]byte, error) {
	b, err := dist.ReadFile("dist/index.html")
	if err != nil {
		return nil, err
	}
	s := strings.ReplaceAll(string(b), "__BASE_HREF__", baseHREF)
	return []byte(s), nil
}
