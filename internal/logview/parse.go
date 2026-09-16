package logview

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var accessRe = regexp.MustCompile(`^(\S+) \S+ \S+ \[([^\]]+)\] "(\S+) (\S+)[^"]*" (\d+) \d+ "([^"]*)" "([^"]*)"(?: ([0-9.]+))?`)

type AccessHit struct {
	IP, URL, Referer, UA string
	Status               int
	RT                   float64
	At                   time.Time
}

func ParseAccessLine(line string) (AccessHit, bool) {
	m := accessRe.FindStringSubmatch(line)
	if len(m) < 7 {
		return AccessHit{}, false
	}
	st, _ := strconv.Atoi(m[5])
	t, err := time.Parse("02/Jan/2006:15:04:05 -0700", m[2])
	if err != nil {
		t, err = time.Parse("02/Jan/2006:15:04:05 +0000", m[2])
	}
	if err != nil {
		t = time.Time{}
	}
	var rt float64
	if len(m) >= 8 && m[8] != "" {
		rt, _ = strconv.ParseFloat(m[8], 64)
	}
	return AccessHit{IP: m[1], URL: m[4], Referer: m[6], UA: m[7], Status: st, RT: rt, At: t}, true
}

func GuessLevel(line string) string {
	u := strings.ToUpper(line)
	switch {
	case strings.Contains(u, "[ERROR]") || strings.Contains(u, " ERROR ") || strings.Contains(u, "EMERG"):
		return "error"
	case strings.Contains(u, "[WARN") || strings.Contains(u, " WARNING"):
		return "warn"
	case strings.Contains(u, "FAILED PASSWORD") || strings.Contains(u, "AUTHENTICATION FAILURE"):
		return "error"
	default:
		return "info"
	}
}

type Stats struct {
	PV      int            `json:"pv"`
	UV      int            `json:"uv"`
	RTAvg   float64        `json:"rt_avg"`
	Status  map[string]int `json:"status"`
	URL     map[string]int `json:"url"`
	IP      map[string]int `json:"ip"`
	Referer map[string]int `json:"referer"`
	UA      map[string]int `json:"ua"`
}

type accessAgg struct {
	st    Stats
	uv    map[string]bool
	rtSum float64
	rtN   int
}

func newAccessAgg() *accessAgg {
	return &accessAgg{
		st: Stats{Status: map[string]int{}, URL: map[string]int{}, IP: map[string]int{}, Referer: map[string]int{}, UA: map[string]int{}},
		uv: map[string]bool{},
	}
}

func (a *accessAgg) add(ln string) {
	h, ok := ParseAccessLine(ln)
	if !ok {
		return
	}
	a.st.PV++
	a.uv[h.IP] = true
	a.st.Status[strconv.Itoa(h.Status)]++
	a.st.URL[h.URL]++
	a.st.IP[h.IP]++
	if h.Referer != "" && h.Referer != "-" {
		a.st.Referer[h.Referer]++
	}
	if h.UA != "" {
		a.st.UA[h.UA]++
	}
	if h.RT > 0 {
		a.rtSum += h.RT
		a.rtN++
	}
}

func (a *accessAgg) result() Stats {
	a.st.UV = len(a.uv)
	if a.rtN > 0 {
		a.st.RTAvg = a.rtSum / float64(a.rtN)
	}
	a.st.URL = topN(a.st.URL, 20)
	a.st.IP = topN(a.st.IP, 20)
	a.st.Referer = topN(a.st.Referer, 10)
	a.st.UA = topN(a.st.UA, 10)
	return a.st
}

func AggregateAccess(lines []string) Stats {
	a := newAccessAgg()
	for _, ln := range lines {
		a.add(ln)
	}
	return a.result()
}

type slowAgg struct {
	count   int
	top     []SlowItem
	pending float64
	have    bool
}

func (a *slowAgg) add(ln string) {
	if strings.HasPrefix(ln, "# Query_time:") {
		fs := strings.Fields(ln)
		if len(fs) >= 3 {
			a.pending, _ = strconv.ParseFloat(fs[2], 64)
			a.have = true
			a.count++
		}
		return
	}
	if a.have && ln != "" && !strings.HasPrefix(ln, "#") && !strings.HasPrefix(ln, "SET timestamp") {
		sql := ln
		if len(sql) > 200 {
			sql = sql[:200]
		}
		a.top = append(a.top, SlowItem{Seconds: a.pending, SQL: sql})
		a.have = false
		for i := len(a.top) - 1; i > 0; i-- {
			if a.top[i].Seconds > a.top[i-1].Seconds {
				a.top[i], a.top[i-1] = a.top[i-1], a.top[i]
			}
		}
		if len(a.top) > 20 {
			a.top = a.top[:20]
		}
	}
}

type SlowItem struct {
	Seconds float64 `json:"seconds"`
	SQL     string  `json:"sql"`
}

func TopSlowSQL(lines []string, n int) []SlowItem {
	var items []SlowItem
	var pending float64
	have := false
	for _, ln := range lines {
		if strings.HasPrefix(ln, "# Query_time:") {
			fs := strings.Fields(ln)
			if len(fs) >= 3 {
				pending, _ = strconv.ParseFloat(fs[2], 64)
				have = true
			}
			continue
		}
		if have && ln != "" && !strings.HasPrefix(ln, "#") && !strings.HasPrefix(ln, "SET timestamp") {
			sql := ln
			if len(sql) > 200 {
				sql = sql[:200]
			}
			items = append(items, SlowItem{Seconds: pending, SQL: sql})
			have = false
		}
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Seconds > items[i].Seconds {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	if n > 0 && len(items) > n {
		items = items[:n]
	}
	return items
}

func LineTime(line string) time.Time {
	if h, ok := ParseAccessLine(line); ok && !h.At.IsZero() {
		return h.At
	}
	if strings.HasPrefix(line, "# Time: ") {
		t, err := time.Parse("2006-01-02T15:04:05.999999Z", strings.TrimSpace(strings.TrimPrefix(line, "# Time: ")))
		if err == nil {
			return t
		}
	}
	fs := strings.Fields(line)
	if len(fs) >= 1 {
		if t, err := time.Parse(time.RFC3339, fs[0]); err == nil {
			return t
		}
		if len(fs[0]) >= 19 {
			if t, err := time.Parse("2006-01-02T15:04:05", fs[0][:19]); err == nil {
				return t
			}
		}
	}
	if len(fs) >= 3 {
		raw := fs[0] + " " + fs[1] + " " + fs[2]
		t, err := time.Parse("Jan 2 15:04:05", raw)
		if err != nil {
			t, err = time.Parse("Jan _2 15:04:05", raw)
		}
		if err == nil {
			now := time.Now()
			t = t.AddDate(now.Year(), 0, 0)
			return t
		}
	}
	return time.Time{}
}

func inRange(line, from, to string) bool {
	if from == "" && to == "" {
		return true
	}
	t := LineTime(line)
	if t.IsZero() {
		return true
	}
	if from != "" {
		ft, err := time.Parse(time.RFC3339, from)
		if err == nil && t.Before(ft) {
			return false
		}
	}
	if to != "" {
		tt, err := time.Parse(time.RFC3339, to)
		if err == nil && t.After(tt) {
			return false
		}
	}
	return true
}

func CountSSHFails(lines []string) int {
	n := 0
	for _, ln := range lines {
		if strings.Contains(ln, "Failed password") || strings.Contains(ln, "authentication failure") {
			n++
		}
	}
	return n
}

func topN(m map[string]int, n int) map[string]int {
	if len(m) <= n {
		return m
	}
	type kv struct {
		k string
		v int
	}
	var arr []kv
	for k, v := range m {
		arr = append(arr, kv{k, v})
	}
	for i := 0; i < len(arr); i++ {
		for j := i + 1; j < len(arr); j++ {
			if arr[j].v > arr[i].v {
				arr[i], arr[j] = arr[j], arr[i]
			}
		}
	}
	out := map[string]int{}
	for i := 0; i < n && i < len(arr); i++ {
		out[arr[i].k] = arr[i].v
	}
	return out
}
