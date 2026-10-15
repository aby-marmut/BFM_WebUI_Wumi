package main

// Fitur Admin: ganti sandi login lewat WebUI (Control > Admin).
// POST /api/admin/password {"cur":"...","new":"..."} (sesi login + X-Requested-With: wumi).

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func adminPasswordH(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	key := "pw:" + ip
	mu.Lock()
	recent := []time.Time{}
	for _, t := range fails[key] {
		if time.Since(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	fails[key] = recent
	blocked := len(recent) >= 5
	mu.Unlock()
	if blocked {
		http.Error(w, "terlalu banyak percobaan, coba lagi 10 menit", 429)
		return
	}
	var in struct {
		Cur string `json:"cur"`
		New string `json:"new"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "permintaan tidak valid", 400)
		return
	}
	raw, err := os.ReadFile(authPath())
	if err != nil {
		http.Error(w, "berkas sandi tidak terbaca", 500)
		return
	}
	var a Auth
	if json.Unmarshal(raw, &a) != nil || a.User == "" {
		http.Error(w, "berkas sandi rusak", 500)
		return
	}
	if !checkLogin(a.User, in.Cur) {
		mu.Lock()
		fails[key] = append(fails[key], time.Now())
		mu.Unlock()
		time.Sleep(500 * time.Millisecond)
		http.Error(w, "sandi saat ini salah", 403)
		return
	}
	if len(in.New) < 8 || len(in.New) > 128 {
		http.Error(w, "sandi baru minimal 8 dan maksimal 128 karakter", 400)
		return
	}
	if strings.TrimSpace(in.New) != in.New {
		http.Error(w, "sandi baru tidak boleh diawali atau diakhiri spasi", 400)
		return
	}
	if in.New == in.Cur {
		http.Error(w, "sandi baru harus berbeda dari sandi saat ini", 400)
		return
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		http.Error(w, "gagal membuat salt", 500)
		return
	}
	na := Auth{User: a.User, Salt: base64.StdEncoding.EncodeToString(salt), Hash: base64.StdEncoding.EncodeToString(kdf(in.New, salt))}
	j, _ := json.Marshal(na)
	tmp := authPath() + ".new"
	if err := os.WriteFile(tmp, j, 0600); err != nil {
		http.Error(w, "gagal menulis sandi", 500)
		return
	}
	if err := os.Rename(tmp, authPath()); err != nil {
		os.Remove(tmp)
		http.Error(w, "gagal menyimpan sandi", 500)
		return
	}
	// sesi lain (perangkat lain) dikeluarkan; sesi ini tetap
	cur := ""
	if c, err := r.Cookie("n8s"); err == nil {
		cur = c.Value
	}
	mu.Lock()
	for k := range sessions {
		if k != cur {
			delete(sessions, k)
		}
	}
	delete(fails, key)
	mu.Unlock()
	w.Write([]byte("ok"))
}

func registerAdmin(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/password", post(adminPasswordH))
}
