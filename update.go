package main

// Cek pembaruan: GET /api/update[?refresh=1], POST /api/update/setting {notif}.
// Hanya memberi tahu (tanpa unduh/pasang). Pengaturan: /data/adb/wumi/update.json

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const updFile = "/data/adb/wumi/update.json"
const updAPI = "https://api.github.com/repos/aby-marmut/BFM_WebUI_Wumi/releases/latest"

type updInfo struct {
	Cur       string   `json:"cur"`
	Latest    string   `json:"latest"`
	Available bool     `json:"available"`
	Notif     bool     `json:"notif"`
	Checked   int64    `json:"checked"`
	Err       string   `json:"err,omitempty"`
	URL       string   `json:"url,omitempty"`
	Fixes     []string `json:"fixes"`
	Feats     []string `json:"feats"`
	Other     []string `json:"other"`
}

var (
	updMu    sync.Mutex
	updC     updInfo
	updAt    time.Time
	updNotif = true
)

func updLoad() {
	b, err := os.ReadFile(updFile)
	if err != nil {
		return
	}
	var s struct {
		Notif *bool `json:"notif"`
	}
	if json.Unmarshal(b, &s) == nil && s.Notif != nil {
		updNotif = *s.Notif
	}
}

func verNum(s string) []int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	var out []int
	for _, p := range strings.Split(s, ".") {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	return out
}

// verNewer: a lebih baru dari b.
func verNewer(a, b string) bool {
	x, y := verNum(a), verNum(b)
	for i := 0; i < len(x) || i < len(y); i++ {
		p, q := 0, 0
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p != q {
			return p > q
		}
	}
	return false
}

func updFetch() (tag, body, page string, err error) {
	var last error
	for _, px := range []string{"http://127.0.0.1:7890", ""} {
		tr := &http.Transport{}
		if px != "" {
			u, _ := url.Parse(px)
			tr.Proxy = http.ProxyURL(u)
		}
		cl := &http.Client{Timeout: 8 * time.Second, Transport: tr}
		req, _ := http.NewRequest("GET", updAPI, nil)
		req.Header.Set("User-Agent", "wumi-update")
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, e := cl.Do(req)
		if e != nil {
			last = e
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			last = errors.New("HTTP " + strconv.Itoa(resp.StatusCode))
			continue
		}
		var r struct {
			TagName string `json:"tag_name"`
			Body    string `json:"body"`
			HTMLURL string `json:"html_url"`
		}
		if e := json.Unmarshal(b, &r); e != nil || r.TagName == "" {
			last = errors.New("balasan tidak valid")
			continue
		}
		return r.TagName, r.Body, r.HTMLURL, nil
	}
	return "", "", "", last
}

func updNotes(body string) (fx, ft, ot []string) {
	sec := 2
	for _, ln := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") {
			l := strings.ToLower(t)
			switch {
			case strings.Contains(l, "perbaikan") || strings.Contains(l, "bug") || strings.Contains(l, "fix"):
				sec = 0
			case strings.Contains(l, "fitur") || strings.Contains(l, "baru") || strings.Contains(l, "new") || strings.Contains(l, "feature"):
				sec = 1
			default:
				sec = 2
			}
			continue
		}
		t = strings.TrimSpace(strings.TrimLeft(t, "-*+ "))
		if t == "" {
			continue
		}
		if r := []rune(t); len(r) > 200 {
			t = string(r[:200]) + "…"
		}
		switch sec {
		case 0:
			fx = append(fx, t)
		case 1:
			ft = append(ft, t)
		default:
			ot = append(ot, t)
		}
		if len(fx)+len(ft)+len(ot) >= 30 {
			break
		}
	}
	return
}

func updGet(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("refresh") == "1"
	updMu.Lock()
	defer updMu.Unlock()
	if force || (updNotif && time.Since(updAt) > 6*time.Hour) {
		tag, body, page, err := updFetch()
		if err != nil {
			updC.Err = err.Error()
			updAt = time.Now().Add(-5 * time.Hour) // coba lagi sekitar 1 jam
		} else {
			fx, ft, ot := updNotes(body)
			updC = updInfo{Latest: tag, URL: page, Checked: time.Now().Unix(), Fixes: fx, Feats: ft, Other: ot}
			updAt = time.Now()
		}
	}
	out := updC
	out.Cur = appVersion
	out.Notif = updNotif
	out.Available = out.Latest != "" && verNewer(out.Latest, appVersion)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func updSet(w http.ResponseWriter, r *http.Request) {
	var s struct {
		Notif bool `json:"notif"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&s); err != nil {
		http.Error(w, "data tidak valid", 400)
		return
	}
	updMu.Lock()
	updNotif = s.Notif
	updMu.Unlock()
	b, _ := json.Marshal(map[string]bool{"notif": s.Notif})
	if err := os.WriteFile(updFile, b, 0600); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Write([]byte("ok"))
}

func registerUpdate(mux *http.ServeMux) {
	updLoad()
	mux.HandleFunc("/api/update", needAuth(updGet))
	mux.HandleFunc("/api/update/setting", post(updSet))
}
