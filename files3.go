package main

// files3.go: Files > mode root (akses penuh /), izin (chmod/chown), salin dan pindah.
// Mode root hanya di memori: hilang (kembali non-root) saat wumi dimulai ulang.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var rootMode atomic.Bool

func rootOn() bool { return rootMode.Load() }

// Non-root: perilaku lama (dibatasi roots). Root: semua jalur absolut.
func safePath(p string) (string, bool) {
	if rootOn() {
		if !strings.HasPrefix(p, "/") {
			return "", false
		}
		rp, err := filepath.EvalSymlinks(filepath.Clean(p))
		if err != nil {
			return "", false
		}
		return rp, true
	}
	return safePathStrict(p)
}

func writable(rp string) bool { return rootOn() || writableStrict(rp) }

// Jalur yang tidak boleh dihapus/diganti nama/dipindah/diubah izinnya, walau mode root.
var protectedExtra = []string{
	"/data/adb", "/data/adb/modules", "/data/adb/wumi", "/data/adb/box",
	"/storage/emulated", "/storage/emulated/0", "/data/local", "/data/local/tmp",
	"/data/adb/magisk", "/data/adb/ksu", "/data/adb/ap", "/data/adb/service.d", "/data/adb/post-fs-data.d",
}

func protectedPath(full string) bool {
	full = filepath.Clean(full)
	if d := filepath.Dir(full); d == "/" || d == "/data" {
		return true
	}
	for _, p := range protectedExtra {
		if full == p {
			return true
		}
	}
	return false
}

func i64s(p *int64) string {
	if p == nil {
		return "-"
	}
	return strconv.FormatInt(*p, 10)
}

func f3json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func errText(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	return err.Error()
}

// Daftar lokasi: mode root menambah "/" di urutan pertama.
func filesRootsH(w http.ResponseWriter, r *http.Request) {
	out := []root{}
	if rootOn() {
		out = append(out, root{"Root (/)", "/"})
	}
	for _, x := range roots {
		if st, err := os.Stat(x.Path); err == nil && st.IsDir() {
			out = append(out, x)
		}
	}
	f3json(w, out)
}

// Di mode root, unduh/lihat teks hanya untuk berkas biasa (bukan perangkat/fifo/soket).
func regOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rootOn() {
			if p, ok := safePath(r.URL.Query().Get("path")); ok {
				if st, err := os.Stat(p); err == nil && !st.IsDir() &&
					(!st.Mode().IsRegular() || p == "/proc/kmsg" || p == "/proc/kcore") {
					http.Error(w, "bukan berkas biasa", 415)
					return
				}
			}
		}
		h(w, r)
	}
}

func filesModeH(w http.ResponseWriter, r *http.Request) {
	f3json(w, map[string]any{"root": rootOn()})
}

func filesModeSetH(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Root bool `json:"root"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil {
		http.Error(w, "permintaan tidak valid", 400)
		return
	}
	rootMode.Store(in.Root)
	log.Printf("files: mode root=%v (dari %s)", in.Root, r.RemoteAddr)
	f3json(w, map[string]any{"root": rootOn()})
}

// Jalur anak tanpa syarat tulis (untuk baca/stat/salin).
func entryPath(dir, name string) (string, string) {
	if !validName(name) {
		return "", "nama tidak valid"
	}
	d, ok := safePath(dir)
	if !ok {
		return "", "jalur tidak diizinkan"
	}
	return filepath.Join(d, name), ""
}

func selinuxCtx(full string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/system/bin/ls", "-Zd", full).Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

func filesStatH(w http.ResponseWriter, r *http.Request) {
	if !rootOn() {
		http.Error(w, "hanya di mode root", 403)
		return
	}
	full, msg := entryPath(r.URL.Query().Get("dir"), r.URL.Query().Get("name"))
	if msg != "" {
		http.Error(w, msg, 403)
		return
	}
	st, err := os.Lstat(full)
	if err != nil {
		http.Error(w, "tidak ada", 404)
		return
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		http.Error(w, "stat tidak tersedia", 500)
		return
	}
	f3json(w, map[string]any{
		"path": full,
		"mode": fmt.Sprintf("%04o", sys.Mode&07777),
		"uid":  sys.Uid,
		"gid":  sys.Gid,
		"link": st.Mode()&os.ModeSymlink != 0,
		"dir":  st.IsDir(),
		"ctx":  selinuxCtx(full),
	})
}

// Ubah izin/pemilik satu entri. Hanya mode root. chown dulu, baru chmod (chown menghapus bit setuid).
func filesPermH(w http.ResponseWriter, r *http.Request) {
	if !rootOn() {
		http.Error(w, "hanya di mode root", 403)
		return
	}
	var in struct {
		Dir  string `json:"dir"`
		Name string `json:"name"`
		Mode string `json:"mode"`
		UID  *int64 `json:"uid"`
		GID  *int64 `json:"gid"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "permintaan tidak valid", 400)
		return
	}
	full, msg := childOf(in.Dir, in.Name)
	if msg != "" {
		http.Error(w, msg, 403)
		return
	}
	st, err := os.Lstat(full)
	if err != nil {
		http.Error(w, "tidak ada: "+in.Name, 404)
		return
	}
	if st.Mode()&os.ModeSymlink != 0 {
		http.Error(w, "tautan simbolik tidak didukung", 400)
		return
	}
	var fm os.FileMode
	doMode := in.Mode != ""
	if doMode {
		m, err := strconv.ParseUint(in.Mode, 8, 32)
		if err != nil || m > 07777 {
			http.Error(w, "mode tidak valid (oktal 0000 sampai 7777)", 400)
			return
		}
		fm = os.FileMode(m & 0777)
		if m&04000 != 0 {
			fm |= os.ModeSetuid
		}
		if m&02000 != 0 {
			fm |= os.ModeSetgid
		}
		if m&01000 != 0 {
			fm |= os.ModeSticky
		}
	}
	if in.UID != nil || in.GID != nil {
		uid, gid := -1, -1
		if in.UID != nil {
			if *in.UID < 0 || *in.UID > 4294967294 {
				http.Error(w, "uid tidak valid", 400)
				return
			}
			uid = int(*in.UID)
		}
		if in.GID != nil {
			if *in.GID < 0 || *in.GID > 4294967294 {
				http.Error(w, "gid tidak valid", 400)
				return
			}
			gid = int(*in.GID)
		}
		if err := os.Chown(full, uid, gid); err != nil {
			http.Error(w, "gagal chown: "+errText(err), 500)
			return
		}
	}
	if doMode {
		if err := os.Chmod(full, fm); err != nil {
			http.Error(w, "gagal chmod: "+errText(err), 500)
			return
		}
	}
	log.Printf("files: izin %s mode=%q uid=%s gid=%s", full, in.Mode, i64s(in.UID), i64s(in.GID))
	w.Write([]byte("ok"))
}

// ---- salin dan pindah (tanpa menimpa) ----

var xferMu sync.Mutex

func copyEntry(src, dst string, st os.FileInfo, budget *int, keepOwner bool) error {
	*budget--
	if *budget < 0 {
		return errors.New("terlalu banyak berkas (batas 100000)")
	}
	bits := st.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	owner := func() {
		if keepOwner {
			if sys, ok := st.Sys().(*syscall.Stat_t); ok {
				os.Lchown(dst, int(sys.Uid), int(sys.Gid))
			}
		}
	}
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		t, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.Symlink(t, dst); err != nil {
			return err
		}
		owner()
		return nil
	case st.IsDir():
		if err := os.Mkdir(dst, 0700); err != nil {
			return err
		}
		ents, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range ents {
			cs := filepath.Join(src, e.Name())
			info, err := os.Lstat(cs)
			if err != nil {
				return err
			}
			if err := copyEntry(cs, filepath.Join(dst, e.Name()), info, budget, keepOwner); err != nil {
				return err
			}
		}
		owner()
		if err := os.Chmod(dst, bits); err != nil {
			return err
		}
		os.Chtimes(dst, st.ModTime(), st.ModTime())
		return nil
	case st.Mode().IsRegular():
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, cerr := io.Copy(out, in)
		serr := out.Sync()
		xerr := out.Close()
		if cerr != nil {
			return cerr
		}
		if serr != nil {
			return serr
		}
		if xerr != nil {
			return xerr
		}
		owner()
		if err := os.Chmod(dst, bits); err != nil {
			return err
		}
		os.Chtimes(dst, st.ModTime(), st.ModTime())
		return nil
	}
	return errors.New("berkas khusus (perangkat/fifo/soket) dilewati")
}

func xferOne(src, dst string, st os.FileInfo, move bool) error {
	if move {
		err := os.Rename(src, dst)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EXDEV) {
			return errors.New("gagal memindah: " + errText(err))
		}
	}
	budget := 100000
	if err := copyEntry(src, dst, st, &budget, move); err != nil {
		os.RemoveAll(dst)
		return errors.New("gagal menyalin: " + errText(err))
	}
	if move {
		if err := os.RemoveAll(src); err != nil {
			return errors.New("sudah tersalin ke tujuan, tetapi sumber gagal dihapus")
		}
	}
	return nil
}

func xferH(move bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			From  string   `json:"from"`
			Names []string `json:"names"`
			To    string   `json:"to"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&in) != nil || len(in.Names) == 0 || len(in.Names) > 200 {
			http.Error(w, "permintaan tidak valid", 400)
			return
		}
		if !xferMu.TryLock() {
			http.Error(w, "operasi salin/pindah lain sedang berjalan", 409)
			return
		}
		defer xferMu.Unlock()
		fromDir, ok := safePath(in.From)
		if !ok {
			http.Error(w, "jalur sumber tidak diizinkan", 403)
			return
		}
		toDir, ok := safePath(in.To)
		if !ok {
			http.Error(w, "jalur tujuan tidak diizinkan", 403)
			return
		}
		if st, err := os.Stat(toDir); err != nil || !st.IsDir() {
			http.Error(w, "folder tujuan tidak valid", 400)
			return
		}
		if !writable(toDir) {
			http.Error(w, "folder tujuan hanya baca", 403)
			return
		}
		if move && fromDir == toDir {
			http.Error(w, "folder tujuan sama dengan sumber", 400)
			return
		}
		n := 0
		errs := []string{}
		for _, name := range in.Names {
			var src, msg string
			if move {
				src, msg = childOf(in.From, name)
			} else {
				src, msg = entryPath(in.From, name)
				if msg == "" && protectedPath(src) {
					msg = "jalur sistem dilindungi"
				}
			}
			if msg != "" {
				errs = append(errs, name+": "+msg)
				continue
			}
			dst, msg := childOf(in.To, name)
			if msg != "" {
				errs = append(errs, name+": "+msg)
				continue
			}
			st, err := os.Lstat(src)
			if err != nil {
				errs = append(errs, name+": tidak ada")
				continue
			}
			if _, err := os.Lstat(dst); err == nil {
				errs = append(errs, name+": sudah ada di tujuan")
				continue
			}
			if st.IsDir() && (dst == src || strings.HasPrefix(dst, src+"/")) {
				errs = append(errs, name+": tidak bisa ke dalam dirinya sendiri")
				continue
			}
			if err := xferOne(src, dst, st, move); err != nil {
				errs = append(errs, name+": "+err.Error())
				continue
			}
			n++
		}
		log.Printf("files: move=%v %s -> %s ok=%d gagal=%d", move, fromDir, toDir, n, len(errs))
		f3json(w, map[string]any{"ok": n, "err": errs})
	}
}

func registerFiles3(mux *http.ServeMux, post func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("/api/files/mode", needAuth(filesModeH))
	mux.HandleFunc("/api/files/mode/set", post(filesModeSetH))
	mux.HandleFunc("/api/files/stat", needAuth(filesStatH))
	mux.HandleFunc("/api/files/perm", post(filesPermH))
	mux.HandleFunc("/api/files/copy", post(xferH(false)))
	mux.HandleFunc("/api/files/move", post(xferH(true)))
}
