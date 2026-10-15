package main

// Wumi: (1) GET /api/adb = daftar perangkat dari server ADB lokal (127.0.0.1:5037, protokol host:devices-l),
// (2) deteksi antarmuka WAN cadangan bila `ip route get` gagal (trafik Status tidak nol terus).

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const adbAddr = "127.0.0.1:5037"

type adbDev struct {
	Serial string `json:"serial"`
	State  string `json:"state"`
	Model  string `json:"model,omitempty"`
	Device string `json:"device,omitempty"`
}

var (
	adbMu      sync.Mutex
	adbAt      time.Time
	adbOut     []byte
	wumiPrevIf string
)

// adbQuery mengirim satu perintah host ke server ADB dan mengembalikan isinya.
func adbQuery(addr, cmd string) (string, error) {
	c, err := net.DialTimeout("tcp", addr, 1500*time.Millisecond)
	if err != nil {
		return "", fmt.Errorf("server ADB tidak aktif")
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(c, "%04x%s", len(cmd), cmd); err != nil {
		return "", err
	}
	var st [4]byte
	if _, err := io.ReadFull(c, st[:]); err != nil {
		return "", err
	}
	var ln [4]byte
	if _, err := io.ReadFull(c, ln[:]); err != nil {
		return "", err
	}
	n, err := strconv.ParseUint(string(ln[:]), 16, 32)
	if err != nil || n > 65536 {
		return "", fmt.Errorf("balasan ADB tidak valid")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c, buf); err != nil {
		return "", err
	}
	if string(st[:]) != "OKAY" {
		return "", fmt.Errorf("ADB: %s", strings.TrimSpace(string(buf)))
	}
	return string(buf), nil
}

func parseDevices(s string) []adbDev {
	out := []adbDev{}
	for _, l := range strings.Split(s, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		d := adbDev{Serial: f[0], State: f[1]}
		for _, kv := range f[2:] {
			switch {
			case strings.HasPrefix(kv, "model:"):
				d.Model = strings.TrimPrefix(kv, "model:")
			case strings.HasPrefix(kv, "device:"):
				d.Device = strings.TrimPrefix(kv, "device:")
			}
		}
		out = append(out, d)
	}
	return out
}

func adbH(w http.ResponseWriter, r *http.Request) {
	adbMu.Lock()
	defer adbMu.Unlock()
	if time.Since(adbAt) > 2*time.Second || adbOut == nil {
		res := struct {
			Devices []adbDev `json:"devices"`
			Err     string   `json:"err,omitempty"`
		}{Devices: []adbDev{}}
		s, err := adbQuery(adbAddr, "host:devices-l")
		if err != nil {
			res.Err = err.Error()
		} else {
			res.Devices = parseDevices(s)
		}
		adbOut, _ = json.Marshal(res)
		adbAt = time.Now()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(adbOut)
}

func registerWumi(mux *http.ServeMux) {
	mux.HandleFunc("/api/adb", needAuth(adbH))
}

// ifaceTraffic: byte rx+tx tiap antarmuka dari /proc/net/dev.
func ifaceTraffic() map[string]uint64 {
	m := map[string]uint64{}
	b, _ := os.ReadFile("/proc/net/dev")
	for _, l := range strings.Split(string(b), "\n") {
		i := strings.Index(l, ":")
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(l[:i])
		f := strings.Fields(l[i+1:])
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		m[name] = rx + tx
	}
	return m
}

func pickWAN(m map[string]uint64) string {
	best, bv := "", uint64(0)
	for _, pre := range [][]string{{"rmnet", "ccmni", "wwan"}, nil} {
		for n, v := range m {
			if n == "lo" {
				continue
			}
			ok := false
			if pre == nil {
				ok = true
				for _, bad := range []string{"tun", "dummy", "ifb", "sit", "ip6", "bond", "p2p", "veth", "br", "docker", "tether", "wlan", "ap", "rndis", "usb", "eth"} {
					if strings.HasPrefix(n, bad) {
						ok = false
					}
				}
			} else {
				for _, p := range pre {
					if strings.HasPrefix(n, p) {
						ok = true
					}
				}
			}
			if ok && (best == "" || v > bv) {
				best, bv = n, v
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}

// defaultIface: pakai hasil `ip route get` bila antarmukanya ada di /proc/net/dev, selain itu tebak (rmnet*).
func defaultIface() string {
	m := ifaceTraffic()
	if i := defaultIfaceOld(); i != "" {
		if _, ok := m[i]; ok {
			return i
		}
	}
	return pickWAN(m)
}
