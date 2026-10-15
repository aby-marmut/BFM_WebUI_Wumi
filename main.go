package main

import (
	"mime"
	"sort"
	"context"
	"io/fs"
	"net/http/httputil"
	"net/url"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed all:web
var webFS embed.FS

const iters = 100000

var (
	dataDir   = "/data/adb/wumi"
	ifaceFlag = flag.String("iface", "", "interface WAN (otomatis jika kosong)")
	mu        sync.Mutex
	sessions  = map[string]time.Time{}
	fails     = map[string][]time.Time{}
	statMu    sync.RWMutex
	stat      Status
	clashPid  string
)

type Status struct {
	DL    float64 `json:"dl"`
	UL    float64 `json:"ul"`
	CPU   float64 `json:"cpu"`
	Clash float64 `json:"clash"`
	Temp  float64 `json:"temp"`
	Bat   float64 `json:"bat"`
	RamGB float64 `json:"ramGB"`
	RamPc float64 `json:"ramPct"`
	RamTot float64 `json:"ramTot"`
	Cores int `json:"cores"`
	Box   bool    `json:"box"`
	Iface string  `json:"iface"`
	Core  string  `json:"core"`
	Mode  string  `json:"mode"`
}

type Auth struct {
	User string `json:"user"`
	Salt string `json:"salt"`
	Hash string `json:"hash"`
}

// PBKDF2-HMAC-SHA256, satu blok 32 byte.
func kdf(pw string, salt []byte) []byte {
	h := hmac.New(sha256.New, []byte(pw))
	h.Write(salt)
	h.Write([]byte{0, 0, 0, 1})
	u := h.Sum(nil)
	t := make([]byte, len(u))
	copy(t, u)
	for i := 1; i < iters; i++ {
		h.Reset()
		h.Write(u)
		u = h.Sum(nil)
		for j := range t {
			t[j] ^= u[j]
		}
	}
	return t
}

func authPath() string { return filepath.Join(dataDir, "auth.json") }

func setPassword() {
	b, _ := io.ReadAll(os.Stdin)
	pw := strings.TrimSpace(string(b))
	if len(pw) < 8 {
		log.Fatal("sandi minimal 8 karakter")
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	a := Auth{User: "admin", Salt: base64.StdEncoding.EncodeToString(salt), Hash: base64.StdEncoding.EncodeToString(kdf(pw, salt))}
	os.MkdirAll(dataDir, 0700)
	j, _ := json.Marshal(a)
	if err := os.WriteFile(authPath(), j, 0600); err != nil {
		log.Fatal(err)
	}
	log.Println("sandi disimpan untuk user admin")
}

func checkLogin(user, pass string) bool {
	b, err := os.ReadFile(authPath())
	if err != nil {
		return false
	}
	var a Auth
	if json.Unmarshal(b, &a) != nil {
		return false
	}
	salt, e1 := base64.StdEncoding.DecodeString(a.Salt)
	want, e2 := base64.StdEncoding.DecodeString(a.Hash)
	if e1 != nil || e2 != nil {
		return false
	}
	okHash := subtle.ConstantTimeCompare(kdf(pass, salt), want) == 1
	okUser := subtle.ConstantTimeCompare([]byte(user), []byte(a.User)) == 1
	return okHash && okUser
}

func newSession() string {
	b := make([]byte, 32)
	rand.Read(b)
	t := base64.RawURLEncoding.EncodeToString(b)
	mu.Lock()
	sessions[t] = time.Now().Add(12 * time.Hour)
	mu.Unlock()
	return t
}

func validSession(r *http.Request) bool {
	c, err := r.Cookie("n8s")
	if err != nil {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	exp, ok := sessions[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(sessions, c.Value)
		return false
	}
	return true
}

func loginH(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "metode salah", 405)
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	mu.Lock()
	recent := []time.Time{}
	for _, t := range fails[ip] {
		if time.Since(t) < 5*time.Minute {
			recent = append(recent, t)
		}
	}
	fails[ip] = recent
	blocked := len(recent) >= 10
	mu.Unlock()
	if blocked {
		http.Error(w, "terlalu banyak percobaan, coba lagi 5 menit", 429)
		return
	}
	var in struct {
		User string `json:"user"`
		Pass string `json:"pass"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "permintaan tidak valid", 400)
		return
	}
	if !checkLogin(in.User, in.Pass) {
		mu.Lock()
		fails[ip] = append(fails[ip], time.Now())
		mu.Unlock()
		time.Sleep(500 * time.Millisecond)
		http.Error(w, "user atau sandi salah", 401)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "n8s", Value: newSession(), Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	w.Write([]byte("ok"))
}

func logoutH(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("n8s"); err == nil {
		mu.Lock()
		delete(sessions, c.Value)
		mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "n8s", Value: "", Path: "/", MaxAge: -1})
	w.Write([]byte("ok"))
}

func page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := webFS.ReadFile("web/" + name)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	}
}

func needAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validSession(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		h(w, r)
	}
}

func statusH(w http.ResponseWriter, r *http.Request) {
	statMu.RLock()
	s := stat
	statMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

// Loopback, rentang privat (IsPrivate), link-local, dan CGNAT 100.64/10 (LAN dan hotspot).
func lanOnly(next http.Handler) http.Handler {
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(h)
		if ip == nil || !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip)) {
			http.Error(w, "forbidden", 403)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func rd(p string) string {
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

func num(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func cpuTimes() (tot, idle uint64) {
	l := strings.SplitN(rd("/proc/stat"), "\n", 2)[0]
	f := strings.Fields(l)
	if len(f) < 2 {
		return
	}
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		tot += n
		if i == 3 || i == 4 {
			idle += n
		}
	}
	return
}

func clashTicks() uint64 {
	if clashPid != "" && rd("/proc/"+clashPid+"/comm") != "clash" {
		clashPid = ""
	}
	if clashPid == "" {
		ents, _ := os.ReadDir("/proc")
		for _, e := range ents {
			n := e.Name()
			if _, err := strconv.Atoi(n); err != nil {
				continue
			}
			if rd("/proc/"+n+"/comm") == "clash" {
				clashPid = n
				break
			}
		}
	}
	if clashPid == "" {
		return 0
	}
	s := rd("/proc/" + clashPid + "/stat")
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 13 {
		return 0
	}
	u, _ := strconv.ParseUint(f[11], 10, 64)
	k, _ := strconv.ParseUint(f[12], 10, 64)
	return u + k
}

func netBytes(iface string) (rx, tx uint64) {
	if iface == "" {
		return
	}
	for _, l := range strings.Split(rd("/proc/net/dev"), "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, iface+":") {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(l, iface+":"))
		if len(f) >= 9 {
			rx, _ = strconv.ParseUint(f[0], 10, 64)
			tx, _ = strconv.ParseUint(f[8], 10, 64)
		}
	}
	return
}

func defaultIfaceOld() string {
	out, err := exec.Command("ip", "route", "get", "1.1.1.1").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	for i, w := range f {
		if w == "dev" && i+1 < len(f) {
			return f[i+1]
		}
	}
	return ""
}

func meminfo() (tot, avail float64) {
	for _, l := range strings.Split(rd("/proc/meminfo"), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			tot = num(f[1])
		case "MemAvailable:":
			avail = num(f[1])
		}
	}
	return
}

func cpuCores() int {
	n := 0
	for _, r := range strings.Split(rd("/sys/devices/system/cpu/present"), ",") {
		a, b, ok := strings.Cut(strings.TrimSpace(r), "-")
		x, e1 := strconv.Atoi(a)
		if e1 != nil {
			continue
		}
		if !ok {
			n++
			continue
		}
		if y, e2 := strconv.Atoi(b); e2 == nil && y >= x {
			n += y - x + 1
		}
	}
	if n == 0 {
		for _, l := range strings.Split(rd("/proc/stat"), "\n") {
			if len(l) > 3 && strings.HasPrefix(l, "cpu") && l[3] >= '0' && l[3] <= '9' {
				n++
			}
		}
	}
	return n
}

func sampler() {
	nCores := cpuCores()
	var pTot, pIdle, pCl, prx, ptx uint64
	var iface string
	var lastIf, last time.Time
	var core, mode string
	cnt := 0
	first := true
	for {
		now := time.Now()
		if *ifaceFlag != "" {
			iface = *ifaceFlag
		} else if iface == "" || now.Sub(lastIf) > 30*time.Second {
			iface = defaultIface()
			lastIf = now
		}
		tot, idle := cpuTimes()
		cl := clashTicks()
		rx, tx := netBytes(iface)
		if iface != wumiPrevIf {
			prx, ptx = rx, tx
			wumiPrevIf = iface
		}
		if cnt%15 == 0 {
			ls := strings.Split(rd(boxDir+"/settings.ini"), "\n")
			core = getSetting(ls, "bin_name")
			mode = getSetting(ls, "network_mode")
		}
		cnt++
		s := Status{Iface: iface, Core: core, Mode: mode, Cores: nCores}
		if !first {
			dt := now.Sub(last).Seconds()
			if dt > 0 {
				if dtot := float64(tot) - float64(pTot); dtot > 0 {
					s.CPU = (dtot - (float64(idle) - float64(pIdle))) / dtot * 100
				}
				if d := float64(cl) - float64(pCl); d > 0 {
					s.Clash = d / dt
				}
				if rx >= prx {
					s.DL = float64(rx-prx) / dt / 1048576
				}
				if tx >= ptx {
					s.UL = float64(tx-ptx) / dt / 1048576
				}
			}
		}
		s.Temp = num(rd("/sys/class/power_supply/battery/temp")) / 10
		s.Bat = num(rd("/sys/class/power_supply/battery/capacity"))
		mt, ma := meminfo()
		if mt > 0 {
			s.RamGB = (mt - ma) / 1048576
			s.RamPc = (mt - ma) / mt * 100
			s.RamTot = mt / 1048576
		}
		s.Box = boxRunning()
		statMu.Lock()
		stat = s
		statMu.Unlock()
		pTot, pIdle, pCl, prx, ptx = tot, idle, cl, rx, tx
		last = now
		first = false
		time.Sleep(time.Second)
	}
}

const boxDir = "/data/adb/box"

func boxRunning() bool {
	pid := rd(boxDir + "/run/box.pid")
	if pid == "" {
		return false
	}
	_, err := os.Stat("/proc/" + pid)
	return err == nil
}

// POST dengan sesi login dan header X-Requested-With (anti lintas-situs).
func post(h http.HandlerFunc) http.HandlerFunc {
	return needAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Requested-With") != "wumi" {
			http.Error(w, "permintaan ditolak", 400)
			return
		}
		h(w, r)
	})
}

func getSetting(lines []string, key string) string {
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, key+"=") {
			return strings.Trim(strings.TrimPrefix(l, key+"="), `"`)
		}
	}
	return ""
}

func proxyTo(target string) *httputil.ReverseProxy {
	u, _ := url.Parse(target)
	p := httputil.NewSingleHostReverseProxy(u)
	d := p.Director
	p.Director = func(r *http.Request) {
		d(r)
		r.Header.Del("Cookie")
	}
	return p
}

// Proxy berlogin ke AndroModem di port terpisah (AndroModem memakai path absolut).
func mirrorServer(addr, mainPort string) {
	p := proxyTo("http://127.0.0.1:49153")
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validSession(r) {
			http.Error(w, "silakan masuk lewat WebUI utama", 401)
			return
		}
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		w.Header().Set("Content-Security-Policy", "frame-ancestors http://"+host+":"+mainPort)
		p.ServeHTTP(w, r)
	})
	srv := &http.Server{Addr: addr, Handler: lanOnly(h), ReadHeaderTimeout: 10 * time.Second}
	log.Println("proxy mirror di", addr)
	log.Println(srv.ListenAndServe())
}

type Msg struct {
	Addr string `json:"addr"`
	Body string `json:"body"`
	Date int64  `json:"date"`
	Read bool   `json:"read"`
}

func sh(timeout time.Duration, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, _ := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out))
}

func controlStateH(w http.ResponseWriter, r *http.Request) {
	addrs := []string{}
	for _, l := range strings.Split(sh(5*time.Second, "/system/bin/ip", "-4", "-o", "addr"), "\n") {
		f := strings.Fields(l)
		if len(f) >= 4 && f[1] != "lo" {
			addrs = append(addrs, f[1]+"  "+f[3])
		}
	}
	out := map[string]any{
		"ttl":      rd("/proc/sys/net/ipv4/ip_default_ttl"),
		"ttlSaved": rd("/data/adb/ttl_value"),
		"operator": sh(3*time.Second, "/system/bin/getprop", "gsm.operator.alpha"),
		"nettype":  sh(3*time.Second, "/system/bin/getprop", "gsm.network.type"),
		"addrs":    addrs,
		"routes":   sh(5*time.Second, "/system/bin/ip", "route"),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// applyTTL menulis TTL IPv4 (global) dan hop_limit IPv6 semua interface.
func applyTTL(v string) (int, error) {
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_default_ttl", []byte(v), 0644); err != nil {
		return 0, err
	}
	files, _ := filepath.Glob("/proc/sys/net/ipv6/conf/*/hop_limit")
	n6 := 0
	for _, f := range files {
		if os.WriteFile(f, []byte(v), 0644) == nil {
			n6++
		}
	}
	return n6, nil
}

// ttlBoot menerapkan TTL tersimpan saat Wumi mulai (pengganti ttl_fix.sh).
// Nilai dibaca SETELAH menunggu, supaya perubahan dari UI tidak tertimpa nilai lama.
func ttlBoot() {
	time.Sleep(15 * time.Second)
	v := rd("/data/adb/ttl_value")
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 255 {
		return
	}
	if n6, err := applyTTL(v); err != nil {
		log.Printf("ttl boot: gagal: %v", err)
	} else {
		log.Printf("ttl boot: TTL %s diterapkan (ipv6=%d)", v, n6)
	}
}

// Sama dengan ttl_fix.sh: terapkan nilai TTL tersimpan sekarang juga.
func ttlH(w http.ResponseWriter, r *http.Request) {
	var tin struct {
		TTL int `json:"ttl"`
	}
	if r.Body != nil {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&tin)
	}
	if tin.TTL != 0 {
		if tin.TTL < 1 || tin.TTL > 255 {
			http.Error(w, "TTL harus 1 sampai 255", 400)
			return
		}
		if err := os.WriteFile("/data/adb/ttl_value", []byte(strconv.Itoa(tin.TTL)+"\n"), 0644); err != nil {
			http.Error(w, "gagal menyimpan TTL", 500)
			return
		}
	}
	v := rd("/data/adb/ttl_value")
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 255 {
		http.Error(w, "nilai TTL tersimpan belum ada atau tidak valid", 409)
		return
	}
	n6, err := applyTTL(v)
	if err != nil {
		http.Error(w, "gagal menerapkan TTL IPv4", 500)
		return
	}
	w.Write([]byte("ok ipv4 ipv6=" + strconv.Itoa(n6)))
}

func powerH(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&in) != nil {
		http.Error(w, "permintaan tidak valid", 400)
		return
	}
	var args []string
	switch in.Action {
	case "reboot":
	case "recovery":
		args = []string{"recovery"}
	case "shutdown":
		args = []string{"-p"}
	default:
		http.Error(w, "aksi tidak dikenal", 400)
		return
	}
	w.Write([]byte("ok"))
	time.AfterFunc(1500*time.Millisecond, func() {
		exec.Command("/system/bin/reboot", args...).Run()
	})
}

func parseMsg(rec string) (Msg, bool) {
	if !strings.HasPrefix(rec, "address=") {
		return Msg{}, false
	}
	ib := strings.Index(rec, ", body=")
	id := strings.LastIndex(rec, ", date=")
	ir := strings.LastIndex(rec, ", read=")
	if ib < 0 || id < ib || ir < id {
		return Msg{}, false
	}
	m := Msg{Addr: rec[8:ib], Body: rec[ib+7 : id]}
	m.Date, _ = strconv.ParseInt(rec[id+7:ir], 10, 64)
	m.Read = strings.TrimSpace(rec[ir+7:]) == "1"
	return m, true
}

func smsH(w http.ResponseWriter, r *http.Request) {
	raw := sh(20*time.Second, "/system/bin/content", "query", "--uri", "content://sms/inbox", "--projection", "address:body:date:read", "--sort", "date DESC limit 30")
	var recs []string
	for _, l := range strings.Split(raw, "\n") {
		if strings.HasPrefix(l, "Row: ") {
			rest := l[5:]
			if i := strings.Index(rest, " "); i >= 0 {
				rest = rest[i+1:]
			}
			recs = append(recs, rest)
		} else if len(recs) > 0 {
			recs[len(recs)-1] += "\n" + l
		}
	}
	msgs := []Msg{}
	unread := 0
	for _, rec := range recs {
		if m, ok := parseMsg(rec); ok {
			msgs = append(msgs, m)
			if !m.Read {
				unread++
			}
			if len(msgs) >= 30 {
				break
			}
		}
	}
	out := map[string]any{"messages": msgs, "unread": unread}
	if len(recs) == 0 && raw != "" {
		if len(raw) > 200 {
			raw = raw[:200]
		}
		out["err"] = raw
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

var (
	apiClient = &http.Client{Timeout: 4 * time.Second}
	ncMu      sync.Mutex
	ipCache   map[string]any
	ipAt      time.Time
)

type kv struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

type link struct {
	C int `json:"c"`
	N int `json:"n"`
	K int `json:"k"`
}

func top(m map[string]int, n int) []kv {
	l := make([]kv, 0, len(m))
	for k, v := range m {
		l = append(l, kv{k, v})
	}
	sort.Slice(l, func(i, j int) bool {
		if l[i].N != l[j].N {
			return l[i].N > l[j].N
		}
		return l[i].Name < l[j].Name
	})
	if len(l) > n {
		l = l[:n]
	}
	return l
}

// Ringkasan koneksi Clash untuk kartu Flow dan Topology.
func overviewH(w http.ResponseWriter, r *http.Request) {
	resp, err := apiClient.Get("http://127.0.0.1:9090/connections")
	if err != nil {
		http.Error(w, "Clash tidak merespons", 502)
		return
	}
	defer resp.Body.Close()
	var cr struct {
		DownloadTotal int64 `json:"downloadTotal"`
		UploadTotal   int64 `json:"uploadTotal"`
		Connections   []struct {
			Metadata struct {
				Network  string `json:"network"`
				SourceIP string `json:"sourceIP"`
			} `json:"metadata"`
			Chains []string `json:"chains"`
				Rule string `json:"rule"`
				RulePayload string `json:"rulePayload"`
		} `json:"connections"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&cr) != nil {
		http.Error(w, "respons Clash tidak valid", 502)
		return
	}
	tcp, udp, other := 0, 0, 0
	cc, nn := map[string]int{}, map[string]int{}
	pair := map[[2]string]int{}
	for _, c := range cr.Connections {
		switch strings.ToLower(c.Metadata.Network) {
		case "tcp":
			tcp++
		case "udp":
			udp++
		default:
			other++
		}
		src := c.Metadata.SourceIP
		if src == "" {
			src = "?"
		}
		node := "DIRECT"
		if len(c.Chains) > 0 {
			node = c.Chains[0]
		}
		cc[src]++
		nn[node]++
		pair[[2]string{src, node}]++
	}
	gg, rr := map[string]int{}, map[string]int{}
	gnp, nrp := map[[2]string]int{}, map[[2]string]int{}
	for _, c := range cr.Connections {
		node, grp := "DIRECT", "(tanpa grup)"
		if n := len(c.Chains); n > 0 {
			node = c.Chains[0]
			if n > 1 {
				grp = c.Chains[n-1]
			}
		}
		rule := c.Rule
		if rule == "" {
			rule = "Match"
		}
		if c.RulePayload != "" {
			rule += ": " + c.RulePayload
		}
		gg[grp]++
		rr[rule]++
		gnp[[2]string{grp, node}]++
		nrp[[2]string{node, rule}]++
	}
	tc, tn := top(cc, 5), top(nn, 5)
	ci, ni := map[string]int{}, map[string]int{}
	for i, x := range tc {
		ci[x.Name] = i
	}
	for i, x := range tn {
		ni[x.Name] = i
	}
	links := []link{}
	for p, k := range pair {
		a, ok1 := ci[p[0]]
		b, ok2 := ni[p[1]]
		if ok1 && ok2 {
			links = append(links, link{a, b, k})
		}
	}
	tg, tr := top(gg, 4), top(rr, 6)
	gi, ri := map[string]int{}, map[string]int{}
	for i, x := range tg {
		gi[x.Name] = i
	}
	for i, x := range tr {
		ri[x.Name] = i
	}
	gl, rl2 := []link{}, []link{}
	for p, k := range gnp {
		a, ok1 := gi[p[0]]
		b, ok2 := ni[p[1]]
		if ok1 && ok2 {
			gl = append(gl, link{a, b, k})
		}
	}
	for p, k := range nrp {
		a, ok1 := ni[p[0]]
		b, ok2 := ri[p[1]]
		if ok1 && ok2 {
			rl2 = append(rl2, link{a, b, k})
		}
	}
	out := map[string]any{"down": cr.DownloadTotal, "up": cr.UploadTotal, "conns": len(cr.Connections), "tcp": tcp, "udp": udp, "other": other, "clients": tc, "nodes": tn, "links": links, "groups": tg, "rules": tr, "gn": gl, "nr": rl2, "nClients": len(cc), "nGroups": len(gg), "nNodes": len(nn), "nRules": len(rr)}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func proxyPort() int {
	resp, err := apiClient.Get("http://127.0.0.1:9090/configs")
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var c struct {
		Mixed int `json:"mixed-port"`
		Port  int `json:"port"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&c) != nil {
		return 0
	}
	if c.Mixed > 0 {
		return c.Mixed
	}
	return c.Port
}

func getJSON(c *http.Client, u string) (map[string]any, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func fetchIP(c *http.Client) (map[string]any, string) {
	errs := []string{}
	if m, err := getJSON(c, "https://api.ip.sb/geoip"); err == nil && m["ip"] != nil {
		out := map[string]any{"ip": m["ip"], "country": m["country"], "city": m["city"], "asn": m["asn"]}
		if v, ok := m["organization"]; ok {
			out["org"] = v
		} else {
			out["org"] = m["isp"]
		}
		return out, ""
	} else if err != nil {
		errs = append(errs, "ip.sb: "+err.Error())
	}
	if m, err := getJSON(c, "https://ipwho.is/"); err == nil && m["ip"] != nil {
		out := map[string]any{"ip": m["ip"], "country": m["country"], "city": m["city"]}
		if cn, ok := m["connection"].(map[string]any); ok {
			out["asn"] = cn["asn"]
			out["org"] = cn["org"]
		}
		return out, ""
	} else if err != nil {
		errs = append(errs, "ipwho.is: "+err.Error())
	}
	if len(errs) == 0 {
		errs = append(errs, "respons tidak berisi IP")
	}
	return nil, strings.Join(errs, "; ")
}

// Latensi dan IP publik diukur lewat port proxy Clash (jalur yang sama dengan klien).
func netcheckH(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"latency": -1}
	port := proxyPort()
	if port == 0 {
		out["err"] = "port proxy Clash tidak ditemukan"
	} else {
		u, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
		c := &http.Client{Timeout: 6 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(u), DisableKeepAlives: true}}
		t0 := time.Now()
		if resp, err := c.Get("http://www.gstatic.com/generate_204"); err == nil {
			resp.Body.Close()
			out["latency"] = int(time.Since(t0).Milliseconds())
		} else {
			out["err"] = "tidak merespons"
		}
		if r.URL.Query().Get("ip") == "1" {
			ncMu.Lock()
			stale := ipCache == nil || time.Since(ipAt) > 5*time.Minute || r.URL.Query().Get("refresh") == "1"
			ncMu.Unlock()
			ipErr := ""
			if stale {
				info, e := fetchIP(c)
				if info != nil {
					ncMu.Lock()
					ipCache, ipAt = info, time.Now()
					ncMu.Unlock()
				} else {
					ipErr = e
				}
			}
			ncMu.Lock()
			if ipCache != nil {
				out["ip"] = ipCache
			} else if ipErr != "" {
				out["ipErr"] = ipErr
			}
			ncMu.Unlock()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

type root struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

var roots = []root{
	{"Box (/data/adb/box)", "/data/adb/box"},
	{"Penyimpanan", "/storage/emulated/0"},
	{"WebUI (/data/adb/wumi)", "/data/adb/wumi"},
	{"Tmp (/data/local/tmp)", "/data/local/tmp"},
}

type entry struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
}

// Hanya jalur di dalam salah satu root (symlink yang keluar dari root ditolak).
func safePathStrict(p string) (string, bool) {
	if !strings.HasPrefix(p, "/") {
		return "", false
	}
	rp, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return "", false
	}
	for _, r := range roots {
		rr, err := filepath.EvalSymlinks(r.Path)
		if err != nil {
			continue
		}
		if rp == rr || strings.HasPrefix(rp, rr+"/") {
			return rp, true
		}
	}
	return "", false
}

func filesListH(w http.ResponseWriter, r *http.Request) {
	p, ok := safePath(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "jalur tidak diizinkan", 403)
		return
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		http.Error(w, "folder tidak terbaca", 500)
		return
	}
	list := []entry{}
	for _, e := range ents {
		if len(list) >= 2000 {
			break
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(p, e.Name())); err == nil {
				isDir, info = st.IsDir(), st
			}
		}
		list = append(list, entry{e.Name(), isDir, info.Size(), info.ModTime().Unix()})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Dir != list[j].Dir {
			return list[i].Dir
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func filesGetH(w http.ResponseWriter, r *http.Request) {
	p, ok := safePath(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "jalur tidak diizinkan", 403)
		return
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		http.Error(w, "berkas tidak ada", 404)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.Error(w, "berkas tidak terbaca", 500)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(p)}))
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func filesTextH(w http.ResponseWriter, r *http.Request) {
	p, ok := safePath(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "jalur tidak diizinkan", 403)
		return
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		http.Error(w, "berkas tidak ada", 404)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.Error(w, "berkas tidak terbaca", 500)
		return
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 262144))
	head := b
	if len(head) > 4096 {
		head = head[:4096]
	}
	if strings.IndexByte(string(head), 0) >= 0 {
		http.Error(w, "berkas biner, gunakan Unduh", 415)
		return
	}
	out := strings.ToValidUTF8(string(b), "?")
	if st.Size() > 262144 {
		out += "\n... (dipotong 256 KB)"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(out))
}

var writeBases = []string{"/storage/emulated/0", "/data/local/tmp", "/data/adb/box/clash"}

func within(rp, base string) bool {
	b, err := filepath.EvalSymlinks(base)
	if err != nil {
		return false
	}
	return rp == b || strings.HasPrefix(rp, b+"/")
}

func writableStrict(rp string) bool {
	for _, b := range writeBases {
		if within(rp, b) {
			return true
		}
	}
	return false
}

func validName(n string) bool {
	return n != "" && n != "." && n != ".." && len(n) <= 200 && !strings.ContainsAny(n, "/\\\x00")
}

func filesUploadH(w http.ResponseWriter, r *http.Request) {
	dir, ok := safePath(r.URL.Query().Get("dir"))
	if !ok {
		http.Error(w, "jalur tidak diizinkan", 403)
		return
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		http.Error(w, "folder tujuan tidak valid", 400)
		return
	}
	if !writable(dir) {
		http.Error(w, "folder ini hanya baca", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "permintaan tidak valid", 400)
		return
	}
	n := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, "unggahan terputus atau terlalu besar", 400)
			return
		}
		if part.FileName() == "" {
			continue
		}
		name := filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		if !validName(name) {
			http.Error(w, "nama berkas tidak valid", 400)
			return
		}
		dst := filepath.Join(dir, name)
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			if os.IsExist(err) {
				http.Error(w, "sudah ada: "+name, 409)
			} else {
				http.Error(w, "gagal membuat berkas", 500)
			}
			return
		}
		_, cerr := io.Copy(f, part)
		f.Close()
		if cerr != nil {
			os.Remove(dst)
			http.Error(w, "gagal menulis berkas", 500)
			return
		}
		n++
	}
	w.Write([]byte(strconv.Itoa(n)))
}

func filesMkdirH(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir  string `json:"dir"`
		Name string `json:"name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in) != nil || !validName(in.Name) {
		http.Error(w, "nama folder tidak valid", 400)
		return
	}
	dir, ok := safePath(in.Dir)
	if !ok {
		http.Error(w, "jalur tidak diizinkan", 403)
		return
	}
	if !writable(dir) {
		http.Error(w, "folder ini hanya baca", 403)
		return
	}
	if err := os.Mkdir(filepath.Join(dir, in.Name), 0755); err != nil {
		if os.IsExist(err) {
			http.Error(w, "sudah ada: "+in.Name, 409)
		} else {
			http.Error(w, "gagal membuat folder", 500)
		}
		return
	}
	w.Write([]byte("ok"))
}

func main() {
	if os.Getenv("SSL_CERT_DIR") == "" {
		os.Setenv("SSL_CERT_DIR", "/system/etc/security/cacerts:/apex/com.android.conscrypt/cacerts")
	}
	addr := flag.String("addr", ":8080", "alamat listen")
	sp := flag.Bool("set-password", false, "set sandi admin dari stdin")
	mirror := flag.String("mirror", ":8081", "alamat proxy mirror AndroModem (kosong = mati)")
	flag.Parse()
	if *sp {
		setPassword()
		return
	}
	if _, err := os.Stat(authPath()); err != nil {
		log.Fatal("belum ada sandi. Jalankan: printf %s SANDI | wumi -set-password")
	}
	go sampler()
	go ttlBoot()
	if *mirror != "" {
		_, mp, _ := net.SplitHostPort(*addr)
		go mirrorServer(*mirror, mp)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/login", page("login.html"))
	mux.HandleFunc("/api/login", loginH)
	mux.HandleFunc("/api/logout", logoutH)
	mux.HandleFunc("/api/status", needAuth(statusH))
	registerBFR(mux, needAuth)
	registerBFRProv(mux, needAuth)
	registerAdmin(mux)
	registerTerm(mux)
	registerWumi(mux)
	registerVnstat(mux)
	registerSpeedtest(mux)
	mux.HandleFunc("/api/control/state", needAuth(controlStateH))
	mux.HandleFunc("/api/control/ttl", post(ttlH))
	mux.HandleFunc("/api/control/power", post(powerH))
	mux.HandleFunc("/api/sms", needAuth(smsH))
	mux.HandleFunc("/api/overview", needAuth(overviewH))
	mux.HandleFunc("/api/files/roots", needAuth(filesRootsH))
	mux.HandleFunc("/api/files/list", needAuth(filesListH))
	mux.HandleFunc("/api/files/get", needAuth(regOnly(filesGetH)))
	mux.HandleFunc("/api/files/text", needAuth(regOnly(filesTextH)))
	mux.HandleFunc("/api/files/upload", post(filesUploadH))
	mux.HandleFunc("/api/files/mkdir", post(filesMkdirH))
registerFiles2(mux, post)
	registerFiles3(mux, post)
	registerArc(mux, post)
	mux.HandleFunc("/api/netcheck", needAuth(netcheckH))
	clash := proxyTo("http://127.0.0.1:9090")
	ui, _ := fs.Sub(webFS, "web/zash")
	uiH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/ui/")
		switch p {
		case "config.js":
			w.Header().Set("Content-Type", "application/javascript")
			w.Write([]byte("window.__METACUBEXD_CONFIG__ = { defaultBackendURL: " + strconv.Quote("http://"+r.Host) + ", githubToken: '' }\n"))
			return
		case "sw.js":
			w.Header().Set("Content-Type", "application/javascript")
			w.Write([]byte("self.addEventListener('install',function(){self.skipWaiting()});self.addEventListener('activate',function(e){e.waitUntil(self.registration.unregister())});\n"))
			return
		}
		if p == "" {
			p = "index.html"
		}
		if st, err := fs.Stat(ui, p); err != nil || st.IsDir() {
			p = "index.html"
		}
		http.ServeFileFS(w, r, ui, p)
	})
	mux.HandleFunc("/ui/", func(w http.ResponseWriter, r *http.Request) {
		if !validSession(r) {
			http.Redirect(w, r, "/login", 302)
			return
		}
		uiH.ServeHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if !validSession(r) {
				http.Redirect(w, r, "/login", 302)
				return
			}
			page("index.html")(w, r)
			return
		}
		if !validSession(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		clash.ServeHTTP(w, r)
	})
	srv := &http.Server{Addr: *addr, Handler: secure(lanOnly(mux)), ReadHeaderTimeout: 10 * time.Second}
	log.Println("wumi di", *addr)
	log.Fatal(srv.ListenAndServe())
}
