package main

// Speed test bawaan Wumi, LEWAT PROXY CLASH (port proxy dibaca dari /configs), server Cloudflare.
// GET /api/speedtest, POST /api/speedtest/{start,stop}. Riwayat: /data/adb/wumi/speedtest.json

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	spdFile = "/data/adb/wumi/speedtest.json"
	spdBase = "https://speed.cloudflare.com"
)

type spdRes struct {
	At   int64   `json:"at"`
	Ping float64 `json:"ping"`
	Jit  float64 `json:"jitter"`
	Down float64 `json:"down"`
	Up   float64 `json:"up"`
	Mode string  `json:"mode,omitempty"`
	IP   string  `json:"ip,omitempty"`
}

type spdState struct {
	State string  `json:"state"`
	Phase string  `json:"phase"`
	Cur   float64 `json:"cur"`
	Ping  float64 `json:"ping"`
	Jit   float64 `json:"jitter"`
	Down  float64 `json:"down"`
	Up    float64 `json:"up"`
	Mode  string  `json:"mode,omitempty"`
	IP    string  `json:"ip,omitempty"`
	Colo  string  `json:"colo,omitempty"`
	Err   string  `json:"err,omitempty"`
}

var (
	spdMu     sync.Mutex
	spdSt     = spdState{State: "idle"}
	spdHist   []spdRes
	spdCancel context.CancelFunc
)

func spdSet(f func(s *spdState)) {
	spdMu.Lock()
	f(&spdSt)
	spdMu.Unlock()
}

// spdZero: isi unggahan berupa byte nol, menghitung byte yang dibaca transport.
type spdZero struct {
	n   int64
	add func(int64)
}

func (z *spdZero) Read(p []byte) (int, error) {
	if z.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > z.n {
		p = p[:z.n]
	}
	for i := range p {
		p[i] = 0
	}
	z.n -= int64(len(p))
	z.add(int64(len(p)))
	return len(p), nil
}

// spdPhase menjalankan beberapa pekerja paralel selama dur, mengembalikan Mbps
// (detik pertama dibuang sebagai pemanasan).
func spdPhase(ctx context.Context, phase string, dur time.Duration, workers int, work func(ctx context.Context, add func(int64))) float64 {
	spdSet(func(s *spdState) { s.Phase = phase; s.Cur = 0 })
	pctx, cancel := context.WithTimeout(ctx, dur)
	defer cancel()
	var total int64
	add := func(n int64) { atomic.AddInt64(&total, n) }
	for i := 0; i < workers; i++ {
		go work(pctx, add)
	}
	t0 := time.Now()
	var b1 int64
	var t1 time.Time
	warm := false
	tk := time.NewTicker(500 * time.Millisecond)
	defer tk.Stop()
	rate := func() float64 {
		cur := atomic.LoadInt64(&total)
		if warm {
			if el := time.Since(t1).Seconds(); el > 0 {
				return float64(cur-b1) * 8 / 1e6 / el
			}
			return 0
		}
		if el := time.Since(t0).Seconds(); el > 0 {
			return float64(cur) * 8 / 1e6 / el
		}
		return 0
	}
loop:
	for {
		select {
		case <-pctx.Done():
			break loop
		case <-tk.C:
			if !warm && time.Since(t0) >= time.Second {
				warm = true
				b1 = atomic.LoadInt64(&total)
				t1 = time.Now()
			}
			v := rate()
			spdSet(func(s *spdState) { s.Cur = v })
		}
	}
	return rate()
}

// spdPing: 7 permintaan kecil lewat koneksi yang sama; yang pertama (handshake) dibuang.
func spdPing(ctx context.Context, c *http.Client) (float64, float64, bool) {
	var v []float64
	for i := 0; i < 7; i++ {
		if ctx.Err() != nil {
			break
		}
		t0 := time.Now()
		req, err := http.NewRequestWithContext(ctx, "GET", spdBase+"/__down?bytes=0", nil)
		if err != nil {
			break
		}
		resp, err := c.Do(req)
		if err != nil {
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if i > 0 {
			v = append(v, float64(time.Since(t0).Microseconds())/1000)
		}
	}
	if len(v) == 0 {
		return 0, 0, false
	}
	var j float64
	for i := 1; i < len(v); i++ {
		d := v[i] - v[i-1]
		if d < 0 {
			d = -d
		}
		j += d
	}
	if len(v) > 1 {
		j /= float64(len(v) - 1)
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2], j, true
}

// spdTrace: IP keluar dan lokasi server Cloudflare (untuk memastikan jalur Clash atau langsung).
func spdTrace(ctx context.Context, c *http.Client) (string, string) {
	req, err := http.NewRequestWithContext(ctx, "GET", spdBase+"/cdn-cgi/trace", nil)
	if err != nil {
		return "", ""
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var ip, colo string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "ip=") {
			ip = strings.TrimSpace(l[3:])
		} else if strings.HasPrefix(l, "colo=") {
			colo = strings.TrimSpace(l[5:])
		}
	}
	return ip, colo
}

func spdDet(v *atomic.Value) string {
	if m, ok := v.Load().(string); ok && m != "" {
		return ": " + m
	}
	return ""
}

func spdRun(ctx context.Context, port int, mode string) {
	defer func() {
		spdMu.Lock()
		if spdCancel != nil {
			spdCancel()
		}
		spdCancel = nil
		spdMu.Unlock()
	}()
	var prx func(*http.Request) (*url.URL, error)
	via := "koneksi langsung"
	if mode != "direct" {
		pu, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
		prx = http.ProxyURL(pu)
		via = "proxy Clash"
	}
	tr := &http.Transport{
		Proxy:                 prx,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConnsPerHost:   8,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	if mode == "direct" {
		// Go tanpa CGO di Android tidak punya resolver sistem: pakai DNS 1.1.1.1 langsung.
		tr.DialContext = (&net.Dialer{
			Timeout: 10 * time.Second,
			Resolver: &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, "udp", "1.1.1.1:53")
			}},
		}).DialContext
	}
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr}
	var lastErr atomic.Value
	fail := func(msg string) {
		spdSet(func(s *spdState) { s.State = "error"; s.Err = msg; s.Cur = 0; s.Phase = "" })
	}
	ping, jit, ok := spdPing(ctx, c)
	if ctx.Err() != nil {
		fail("dibatalkan")
		return
	}
	if !ok {
		fail("tidak bisa menjangkau speed.cloudflare.com lewat " + via)
		return
	}
	ip, colo := spdTrace(ctx, c)
	spdSet(func(s *spdState) { s.Ping = ping; s.Jit = jit; s.IP = ip; s.Colo = colo })
	down := spdPhase(ctx, "down", 8*time.Second, 4, func(pc context.Context, add func(int64)) {
		buf := make([]byte, 64<<10)
		for pc.Err() == nil {
			req, err := http.NewRequestWithContext(pc, "GET", spdBase+"/__down?bytes=25000000", nil)
			if err != nil {
				return
			}
			resp, err := c.Do(req)
			if err != nil {
				lastErr.Store(err.Error())
				time.Sleep(200 * time.Millisecond)
				continue
			}
			if resp.StatusCode != 200 {
				lastErr.Store("HTTP " + resp.Status)
				resp.Body.Close()
				time.Sleep(200 * time.Millisecond)
				continue
			}
			for {
				n, e := resp.Body.Read(buf)
				if n > 0 {
					add(int64(n))
				}
				if e != nil {
					if e != io.EOF && pc.Err() == nil {
						lastErr.Store(e.Error())
					}
					break
				}
			}
			resp.Body.Close()
		}
	})
	if ctx.Err() != nil {
		fail("dibatalkan")
		return
	}
	if down <= 0 {
		fail("unduhan gagal" + spdDet(&lastErr))
		return
	}
	spdSet(func(s *spdState) { s.Down = down })
	up := spdPhase(ctx, "up", 8*time.Second, 3, func(pc context.Context, add func(int64)) {
		for pc.Err() == nil {
			req, err := http.NewRequestWithContext(pc, "POST", spdBase+"/__up", &spdZero{n: 8 << 20, add: add})
			if err != nil {
				return
			}
			req.ContentLength = 8 << 20
			req.Header.Set("Content-Type", "application/octet-stream")
			resp, err := c.Do(req)
			if err != nil {
				lastErr.Store(err.Error())
				time.Sleep(200 * time.Millisecond)
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 300 {
				lastErr.Store("HTTP " + resp.Status)
				time.Sleep(200 * time.Millisecond)
			}
		}
	})
	if ctx.Err() != nil {
		fail("dibatalkan")
		return
	}
	if up <= 0 {
		fail("unggahan gagal" + spdDet(&lastErr))
		return
	}
	spdMu.Lock()
	spdSt.Up = up
	spdSt.State = "done"
	spdSt.Phase = ""
	spdSt.Cur = 0
	spdHist = append(spdHist, spdRes{At: time.Now().Unix(), Ping: ping, Jit: jit, Down: down, Up: up, Mode: mode, IP: ip})
	if len(spdHist) > 20 {
		spdHist = spdHist[len(spdHist)-20:]
	}
	b, err := json.Marshal(spdHist)
	spdMu.Unlock()
	if err == nil {
		vnWriteFile(spdFile, b)
	}
}

func spdStatusH(w http.ResponseWriter, r *http.Request) {
	spdMu.Lock()
	out := map[string]any{"st": spdSt, "hist": append([]spdRes{}, spdHist...)}
	spdMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func spdStartH(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode != "direct" {
		mode = "clash"
	}
	port := 0
	if mode == "clash" {
		port = proxyPort()
		if port == 0 {
			http.Error(w, "port proxy Clash tidak ditemukan", 503)
			return
		}
	}
	spdMu.Lock()
	if spdCancel != nil {
		spdMu.Unlock()
		http.Error(w, "tes sedang berjalan", 409)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	spdCancel = cancel
	spdSt = spdState{State: "running", Phase: "ping", Mode: mode}
	spdMu.Unlock()
	go spdRun(ctx, port, mode)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func spdStopH(w http.ResponseWriter, r *http.Request) {
	spdMu.Lock()
	if spdCancel != nil {
		spdCancel()
	}
	spdMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func spdResetH(w http.ResponseWriter, r *http.Request) {
	spdMu.Lock()
	if spdCancel != nil {
		spdMu.Unlock()
		http.Error(w, "tes sedang berjalan", 409)
		return
	}
	spdHist = nil
	spdSt = spdState{State: "idle"}
	spdMu.Unlock()
	os.Remove(spdFile)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func registerSpeedtest(mux *http.ServeMux) {
	if b, err := os.ReadFile(spdFile); err == nil {
		json.Unmarshal(b, &spdHist)
	}
	mux.HandleFunc("/api/speedtest", needAuth(spdStatusH))
	mux.HandleFunc("/api/speedtest/start", post(spdStartH))
	mux.HandleFunc("/api/speedtest/stop", post(spdStopH))
	mux.HandleFunc("/api/speedtest/reset", post(spdResetH))
}
