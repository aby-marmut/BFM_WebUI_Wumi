package main

// vnstat bawaan Wumi: pencatat pemakaian data harian/bulanan antarmuka WAN (tanpa biner vnstat).
// GET /api/vnstat, POST /api/vnstat/reset. Data: /data/adb/wumi/vnstat.json

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

const vnFile = "/data/adb/wumi/vnstat.json"

type vnDay struct {
	Rx uint64 `json:"rx"`
	Tx uint64 `json:"tx"`
}

type vnData struct {
	Since string           `json:"since"`
	Days  map[string]vnDay `json:"days"`
	Hours map[string]vnDay `json:"hours,omitempty"`
}

type vnRow struct {
	D  string `json:"d"`
	Rx uint64 `json:"rx"`
	Tx uint64 `json:"tx"`
}

var (
	vnMu    sync.Mutex
	vnD     = vnData{Days: map[string]vnDay{}, Hours: map[string]vnDay{}}
	vnDirty bool
	vnLoc   = time.Local
)

func vnWriteFile(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func vnInit() {
	if z := strings.TrimSpace(sh(2*time.Second, "/system/bin/getprop", "persist.sys.timezone")); z != "" {
		if l, err := time.LoadLocation(z); err == nil {
			vnLoc = l
		}
	}
	if b, err := os.ReadFile(vnFile); err == nil {
		var d vnData
		if json.Unmarshal(b, &d) == nil {
			if d.Days == nil {
				d.Days = map[string]vnDay{}
			}
			if d.Hours == nil {
				d.Hours = map[string]vnDay{}
			}
			vnD = d
		}
	}
	if vnD.Since == "" {
		vnD.Since = time.Now().In(vnLoc).Format("2006-01-02")
	}
}

func vnSave() {
	vnMu.Lock()
	if !vnDirty {
		vnMu.Unlock()
		return
	}
	cut := time.Now().In(vnLoc).AddDate(0, 0, -400).Format("2006-01-02")
	for k := range vnD.Days {
		if k < cut {
			delete(vnD.Days, k)
		}
	}
	hc := time.Now().In(vnLoc).AddDate(0, 0, -14).Format("2006-01-02T15")
	for k := range vnD.Hours {
		if k < hc {
			delete(vnD.Hours, k)
		}
	}
	b, err := json.Marshal(vnD)
	vnDirty = false
	vnMu.Unlock()
	if err != nil {
		return
	}
	if vnWriteFile(vnFile, b) != nil {
		vnMu.Lock()
		vnDirty = true
		vnMu.Unlock()
	}
}

// vnLoop: tiap 5 dtk tambahkan selisih counter antarmuka WAN (yang dipilih sampler Status).
// Counter turun (reboot / antarmuka dibuat ulang) = hitung nilai baru dari nol.
func vnLoop() {
	var prx, ptx uint64
	pif := ""
	lastSave := time.Now()
	for {
		time.Sleep(5 * time.Second)
		statMu.RLock()
		iface := stat.Iface
		statMu.RUnlock()
		if iface == "" {
			continue
		}
		rx, tx := netBytes(iface)
		if iface != pif {
			pif, prx, ptx = iface, rx, tx
			continue
		}
		drx, dtx := rx, tx
		if rx >= prx {
			drx = rx - prx
		}
		if tx >= ptx {
			dtx = tx - ptx
		}
		prx, ptx = rx, tx
		now := time.Now().In(vnLoc)
		if drx > 0 || dtx > 0 {
			k := now.Format("2006-01-02")
			vnMu.Lock()
			d := vnD.Days[k]
			d.Rx += drx
			d.Tx += dtx
			vnD.Days[k] = d
			kh := now.Format("2006-01-02T15")
			hv := vnD.Hours[kh]
			hv.Rx += drx
			hv.Tx += dtx
			vnD.Hours[kh] = hv
			vnDirty = true
			vnMu.Unlock()
		}
		if now.Sub(lastSave) > 60*time.Second {
			vnSave()
			lastSave = now
		}
	}
}

func vnstatH(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(vnLoc)
	today, month := now.Format("2006-01-02"), now.Format("2006-01")
	statMu.RLock()
	iface := stat.Iface
	statMu.RUnlock()
	var t, all vnDay
	mon := map[string]vnDay{}
	days := []vnRow{}
	hours := []vnRow{}
	vnMu.Lock()
	since := vnD.Since
	for k, v := range vnD.Days {
		all.Rx += v.Rx
		all.Tx += v.Tx
		if k == today {
			t = v
		}
		if len(k) >= 7 {
			a := mon[k[:7]]
			a.Rx += v.Rx
			a.Tx += v.Tx
			mon[k[:7]] = a
		}
		days = append(days, vnRow{k, v.Rx, v.Tx})
	}
	for k, v := range vnD.Hours {
		hours = append(hours, vnRow{k, v.Rx, v.Tx})
	}
	vnMu.Unlock()
	sort.Slice(hours, func(i, j int) bool { return hours[i].D > hours[j].D })
	if len(hours) > 48 {
		hours = hours[:48]
	}
	sort.Slice(days, func(i, j int) bool { return days[i].D > days[j].D })
	if len(days) > 31 {
		days = days[:31]
	}
	months := []vnRow{}
	for k, v := range mon {
		months = append(months, vnRow{k, v.Rx, v.Tx})
	}
	sort.Slice(months, func(i, j int) bool { return months[i].D > months[j].D })
	if len(months) > 12 {
		months = months[:12]
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"iface": iface, "since": since, "today": t, "month": mon[month],
		"total": all, "days": days, "months": months,
		"hours": hours, "now": now.Format("2006-01-02T15"),
	})
}

func vnResetH(w http.ResponseWriter, r *http.Request) {
	vnMu.Lock()
	vnD = vnData{Since: time.Now().In(vnLoc).Format("2006-01-02"), Days: map[string]vnDay{}, Hours: map[string]vnDay{}}
	vnDirty = true
	vnMu.Unlock()
	vnSave()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func registerVnstat(mux *http.ServeMux) {
	vnInit()
	go vnLoop()
	mux.HandleFunc("/api/vnstat", needAuth(vnstatH))
	mux.HandleFunc("/api/vnstat/reset", post(vnResetH))
}
