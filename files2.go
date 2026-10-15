package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// false = hanya folder kosong yang boleh dihapus. true = hapus folder beserta isinya.
const delRecursive = false

const maxEdit = 262144

// Nama anak langsung di dalam dir (dir harus di root dan bisa ditulis).
// Hasil: jalur lengkap, atau pesan galat (jalur kosong).
func childOf(dir, name string) (string, string) {
	if !validName(name) {
		return "", "nama tidak valid"
	}
	d, ok := safePath(dir)
	if !ok {
		return "", "jalur tidak diizinkan"
	}
	if !writable(d) {
		return "", "folder ini hanya baca"
	}
	full := filepath.Join(d, name)
	if protectedPath(full) {
		return "", "jalur sistem dilindungi"
	}
	for _, b := range writeBases {
		if rb, err := filepath.EvalSymlinks(b); err == nil && rb == full {
			return "", "folder akar tidak boleh diubah"
		}
	}
	return full, ""
}

func filesDeleteH(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir   string   `json:"dir"`
		Names []string `json:"names"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&in) != nil || len(in.Names) == 0 || len(in.Names) > 200 {
		http.Error(w, "permintaan tidak valid", 400)
		return
	}
	n := 0
	errs := []string{}
	for _, name := range in.Names {
		full, msg := childOf(in.Dir, name)
		if msg != "" {
			errs = append(errs, name+": "+msg)
			continue
		}
		st, err := os.Lstat(full)
		if err != nil {
			errs = append(errs, name+": tidak ada")
			continue
		}
		if st.IsDir() && delRecursive {
			err = os.RemoveAll(full)
		} else {
			err = os.Remove(full)
		}
		if err != nil {
			if st.IsDir() {
				errs = append(errs, name+": gagal (folder tidak kosong?)")
			} else {
				errs = append(errs, name+": gagal dihapus")
			}
			continue
		}
		n++
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": n, "err": errs})
}

func filesRenameH(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir  string `json:"dir"`
		Name string `json:"name"`
		To   string `json:"to"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "permintaan tidak valid", 400)
		return
	}
	src, msg := childOf(in.Dir, in.Name)
	if msg != "" {
		http.Error(w, msg, 403)
		return
	}
	dst, msg := childOf(in.Dir, in.To)
	if msg != "" {
		http.Error(w, msg, 403)
		return
	}
	if _, err := os.Lstat(src); err != nil {
		http.Error(w, "tidak ada: "+in.Name, 404)
		return
	}
	if _, err := os.Lstat(dst); err == nil {
		http.Error(w, "sudah ada: "+in.To, 409)
		return
	}
	if err := os.Rename(src, dst); err != nil {
		http.Error(w, "gagal mengganti nama", 500)
		return
	}
	w.Write([]byte("ok"))
}

// Simpan teks ke berkas yang sudah ada: cadangan NAMA.bak dulu, lalu tulis di tempat.
func filesSaveH(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path  string `json:"path"`
		Text  string `json:"text"`
		Mtime int64  `json:"mtime"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || len(in.Text) > maxEdit {
		http.Error(w, "permintaan tidak valid atau lebih dari 256 KB", 400)
		return
	}
	p, ok := safePath(in.Path)
	if !ok {
		http.Error(w, "jalur tidak diizinkan", 403)
		return
	}
	if !writable(p) {
		http.Error(w, "folder ini hanya baca", 403)
		return
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		http.Error(w, "berkas tidak ada", 404)
		return
	}
	if st.Size() > maxEdit {
		http.Error(w, "berkas lebih dari 256 KB", 413)
		return
	}
	if in.Mtime != 0 && st.ModTime().Unix() != in.Mtime {
		http.Error(w, "berkas sudah berubah, buka ulang", 409)
		return
	}
	old, err := os.ReadFile(p)
	if err != nil {
		http.Error(w, "berkas tidak terbaca", 500)
		return
	}
	if !utf8.Valid(old) || strings.IndexByte(string(old), 0) >= 0 {
		http.Error(w, "bukan teks UTF-8, gunakan Unduh", 415)
		return
	}
	if err := os.WriteFile(p+".bak", old, 0644); err != nil {
		http.Error(w, "gagal membuat cadangan .bak", 500)
		return
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		http.Error(w, "gagal membuka berkas", 500)
		return
	}
	_, werr := io.WriteString(f, in.Text)
	serr := f.Sync()
	cerr := f.Close()
	if werr != nil || serr != nil || cerr != nil {
		http.Error(w, "gagal menulis, cadangan ada di "+filepath.Base(p)+".bak", 500)
		return
	}
	w.Write([]byte("ok"))
}

func registerFiles2(mux *http.ServeMux, post func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("/api/files/delete", post(filesDeleteH))
	mux.HandleFunc("/api/files/rename", post(filesRenameH))
	mux.HandleFunc("/api/files/save", post(filesSaveH))
}
