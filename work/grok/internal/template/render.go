package template

import (
	"bytes"
	"fmt"
	"strings"
	gotmpl "text/template"
)

func join(ss []string, sep string) string {
	return strings.Join(ss, sep)
}

func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func indent(n int, s string) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = pad + ln
		}
	}
	return strings.Join(lines, "\n")
}

func Render(name, content string, data any) (string, error) {
	t, err := gotmpl.New(name).Option("missingkey=error").Funcs(gotmpl.FuncMap{
		"join":   join,
		"quote":  quote,
		"indent": indent,
	}).Parse(content)
	if err != nil {
		return "", fmt.Errorf("模板语法错误：%w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("模板渲染失败：%w", err)
	}
	return buf.String(), nil
}
