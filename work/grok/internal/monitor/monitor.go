package monitor

import (
	"bufio"
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"lnmp-panel/internal/store"
)

type Sample struct {
	At        string  `json:"at"`
	CPU       float64 `json:"cpu"`
	MemUsed   int64   `json:"mem_used"`
	MemTotal  int64   `json:"mem_total"`
	DiskUsed  int64   `json:"disk_used"`
	DiskTotal int64   `json:"disk_total"`
	NetIn     int64   `json:"net_in"`
	NetOut    int64   `json:"net_out"`
	Load1     float64 `json:"load1"`
	Extra     string  `json:"extra"`
}

type Service struct {
	db       *store.DB
	ProcStat string
	MemInfo  string
	LoadAvg  string
	NetDev   string
	Root     string
	Extra    func() map[string]string
	mu       sync.Mutex
	prevIdle uint64
	prevTot  uint64
}

func New(db *store.DB) *Service {
	return &Service{
		db: db, ProcStat: "/proc/stat", MemInfo: "/proc/meminfo",
		LoadAvg: "/proc/loadavg", NetDev: "/proc/net/dev", Root: "/",
	}
}

func (s *Service) Snapshot() (Sample, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	cpu := s.cpu()
	mu, mt := s.mem()
	du, dt := s.disk()
	ni, no := s.net()
	load := s.load()
	extra := "{}"
	if s.Extra != nil {
		b, _ := json.Marshal(s.Extra())
		extra = string(b)
	}
	return Sample{At: now, CPU: cpu, MemUsed: mu, MemTotal: mt, DiskUsed: du, DiskTotal: dt, NetIn: ni, NetOut: no, Load1: load, Extra: extra}, nil
}

func (s *Service) Sample() error {
	sm, err := s.Snapshot()
	if err != nil {
		return err
	}
	_, err = s.db.SQL.Exec(`INSERT OR REPLACE INTO metrics_raw(at,cpu,mem_used,mem_total,disk_used,disk_total,net_in,net_out,load1,extra)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, sm.At, sm.CPU, sm.MemUsed, sm.MemTotal, sm.DiskUsed, sm.DiskTotal, sm.NetIn, sm.NetOut, sm.Load1, sm.Extra)
	return err
}

func (s *Service) Rollup() error {
	hour := time.Now().UTC().Truncate(time.Hour)
	from := hour.Add(-time.Hour).Format(time.RFC3339)
	to := hour.Format(time.RFC3339)
	rows, err := s.db.SQL.Query(`SELECT cpu, mem_used, disk_used, net_in, net_out, load1 FROM metrics_raw WHERE at>=? AND at<? ORDER BY at`, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()
	var n int
	var cpu, load float64
	var mem, disk, netIn0, netOut0, netIn1, netOut1 int64
	first := true
	for rows.Next() {
		var c, l float64
		var m, d, ni, no int64
		if err := rows.Scan(&c, &m, &d, &ni, &no, &l); err != nil {
			return err
		}
		cpu += c
		load += l
		mem += m
		disk += d
		if first {
			netIn0, netOut0 = ni, no
			first = false
		}
		netIn1, netOut1 = ni, no
		n++
	}
	if n == 0 {
		return nil
	}
	nf := float64(n)
	_, err = s.db.SQL.Exec(`INSERT OR REPLACE INTO metrics_hourly(bucket,cpu_avg,mem_avg,disk_avg,net_in_sum,net_out_sum,load_avg)
		VALUES (?,?,?,?,?,?,?)`, to, cpu/nf, mem/int64(n), disk/int64(n), netIn1-netIn0, netOut1-netOut0, load/nf)
	return err
}

func (s *Service) Purge(rawDays int) error {
	if rawDays <= 0 {
		rawDays = 7
	}
	cut := time.Now().UTC().Add(-time.Duration(rawDays) * 24 * time.Hour).Format(time.RFC3339)
	_, err := s.db.SQL.Exec(`DELETE FROM metrics_raw WHERE at < ?`, cut)
	return err
}

func (s *Service) PurgeHourly(days int) error {
	if days <= 0 {
		days = 90
	}
	cut := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
	_, err := s.db.SQL.Exec(`DELETE FROM metrics_hourly WHERE bucket < ?`, cut)
	return err
}

func (s *Service) History(from, to, step string) ([]Sample, error) {
	if step == "hour" {
		rows, err := s.db.SQL.Query(`SELECT bucket, cpu_avg, mem_avg, disk_avg, net_in_sum, net_out_sum, load_avg FROM metrics_hourly WHERE bucket>=? AND bucket<=? ORDER BY bucket`, from, to)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Sample
		for rows.Next() {
			var sm Sample
			if err := rows.Scan(&sm.At, &sm.CPU, &sm.MemUsed, &sm.DiskUsed, &sm.NetIn, &sm.NetOut, &sm.Load1); err != nil {
				return nil, err
			}
			out = append(out, sm)
		}
		if out == nil {
			out = []Sample{}
		}
		return out, rows.Err()
	}
	rows, err := s.db.SQL.Query(`SELECT at,cpu,mem_used,mem_total,disk_used,disk_total,net_in,net_out,load1,extra FROM metrics_raw WHERE at>=? AND at<=? ORDER BY at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sample
	for rows.Next() {
		var sm Sample
		if err := rows.Scan(&sm.At, &sm.CPU, &sm.MemUsed, &sm.MemTotal, &sm.DiskUsed, &sm.DiskTotal, &sm.NetIn, &sm.NetOut, &sm.Load1, &sm.Extra); err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	if out == nil {
		out = []Sample{}
	}
	return out, rows.Err()
}

func (s *Service) Latest() (Sample, error) {
	var sm Sample
	err := s.db.SQL.QueryRow(`SELECT at,cpu,mem_used,mem_total,disk_used,disk_total,net_in,net_out,load1,extra FROM metrics_raw ORDER BY at DESC LIMIT 1`).
		Scan(&sm.At, &sm.CPU, &sm.MemUsed, &sm.MemTotal, &sm.DiskUsed, &sm.DiskTotal, &sm.NetIn, &sm.NetOut, &sm.Load1, &sm.Extra)
	return sm, err
}

func (s *Service) cpu() float64 {
	b, err := os.ReadFile(s.ProcStat)
	if err != nil {
		return 0
	}
	var user, nice, system, idle, iowait, irq, soft, steal uint64
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	if !sc.Scan() {
		return 0
	}
	fs := strings.Fields(sc.Text())
	if len(fs) < 5 || fs[0] != "cpu" {
		return 0
	}
	parse := func(i int) uint64 {
		if i >= len(fs) {
			return 0
		}
		n, _ := strconv.ParseUint(fs[i], 10, 64)
		return n
	}
	user, nice, system, idle = parse(1), parse(2), parse(3), parse(4)
	iowait, irq, soft, steal = parse(5), parse(6), parse(7), parse(8)
	idleAll := idle + iowait
	tot := user + nice + system + idleAll + irq + soft + steal
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prevTot == 0 {
		s.prevIdle, s.prevTot = idleAll, tot
		return 0
	}
	dIdle := idleAll - s.prevIdle
	dTot := tot - s.prevTot
	s.prevIdle, s.prevTot = idleAll, tot
	if dTot == 0 {
		return 0
	}
	return 100 * (1 - float64(dIdle)/float64(dTot))
}

func (s *Service) mem() (used, total int64) {
	b, err := os.ReadFile(s.MemInfo)
	if err != nil {
		return 0, 0
	}
	var memTotal, memAvail int64
	for _, ln := range strings.Split(string(b), "\n") {
		fs := strings.Fields(ln)
		if len(fs) < 2 {
			continue
		}
		n, _ := strconv.ParseInt(fs[1], 10, 64)
		n *= 1024
		switch fs[0] {
		case "MemTotal:":
			memTotal = n
		case "MemAvailable:":
			memAvail = n
		}
	}
	return memTotal - memAvail, memTotal
}

func (s *Service) disk() (used, total int64) {
	var st unix.Statfs_t
	if err := unix.Statfs(s.Root, &st); err != nil {
		return 0, 0
	}
	total = int64(st.Blocks) * int64(st.Bsize)
	free := int64(st.Bavail) * int64(st.Bsize)
	return total - free, total
}

func (s *Service) net() (in, out int64) {
	b, err := os.ReadFile(s.NetDev)
	if err != nil {
		return 0, 0
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if !strings.Contains(ln, ":") {
			continue
		}
		if strings.Contains(ln, "lo:") {
			continue
		}
		fs := strings.Fields(ln)
		if len(fs) < 10 {
			continue
		}
		rx, _ := strconv.ParseInt(fs[1], 10, 64)
		tx, _ := strconv.ParseInt(fs[9], 10, 64)
		in += rx
		out += tx
	}
	return in, out
}

func (s *Service) load() float64 {
	b, err := os.ReadFile(s.LoadAvg)
	if err != nil {
		return 0
	}
	fs := strings.Fields(string(b))
	if len(fs) == 0 {
		return 0
	}
	f, _ := strconv.ParseFloat(fs[0], 64)
	_ = runtime.NumCPU
	return f
}
