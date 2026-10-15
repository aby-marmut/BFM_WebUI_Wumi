package main

// archive.go: Files > Arsip. Buat zip / tar.gz / 7z dan ekstrak zip / tar / tar.gz / tgz / 7z.
// zip dan tar memakai pustaka standar. 7z memakai biner 7zz (opsional, lihat sevenZipBin).
// Tidak pernah menimpa: arsip baru dan folder hasil ekstrak harus belum ada.
// Saat ekstrak, tautan simbolik dan berkas khusus dilewati, nama entri dibersihkan dari "..".

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	arcMaxEntries = 100000
	arcMaxBytes   = int64(32) << 30
)

var arcMu sync.Mutex

var arcExt = map[string]string{"zip": ".zip", "tar.gz": ".tar.gz", "7z": ".7z"}

// Biner 7-Zip (7zz). Letakkan di /data/adb/wumi/7zz (chmod 755).
func sevenZipBin() string {
	for _, p := range []string{"/data/adb/wumi/7zz", "/data/adb/wumi/7z"} {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0111 != 0 {
			return p
		}
	}
	for _, n := range []string{"7zz", "7z", "7za"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

func arcInfoH(w http.ResponseWriter, r *http.Request) {
	b := sevenZipBin() != ""
	f3json(w, map[string]any{"7z": b, "rar": b})
}

func arcKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".tar.gz"), strings.HasSuffix(n, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(n, ".tar"):
		return "tar"
	case strings.HasSuffix(n, ".zip"):
		return "zip"
	case strings.HasSuffix(n, ".7z"):
		return "7z"
	case strings.HasSuffix(n, ".rar"):
		return "rar"
	}
	return ""
}

func arcStem(name string) string {
	l := strings.ToLower(name)
	for _, e := range []string{".tar.gz", ".tgz", ".tar", ".zip", ".7z", ".rar"} {
		if strings.HasSuffix(l, e) && len(name) > len(e) {
			return name[:len(name)-len(e)]
		}
	}
	return name
}

func arcTail(o []byte, err error) string {
	o = bytes.TrimSpace(o)
	if len(o) > 300 {
		o = o[len(o)-300:]
	}
	if len(o) == 0 {
		return err.Error()
	}
	return string(o)
}

// Sandi tidak pernah lewat argumen perintah (tidak tampak di ps) dan tidak dicatat di log:
// 7zz meminta sandi lewat stdin hanya bila arsip memang terkunci.
func arcPwOK(pw string) bool {
	if len(pw) > 256 {
		return false
	}
	return !strings.ContainsAny(pw, "\r\n\x00")
}

func arcRun(bin, dir, pw string, repeat int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	in := ""
	if pw != "" {
		in = strings.Repeat(pw+"\n", repeat)
	}
	cmd.Stdin = strings.NewReader(in)
	return cmd.CombinedOutput()
}

// Galat sandi dari keluaran 7zz. Awalan "SANDI:" dipakai UI untuk menampilkan kolom sandi.
func arcPwErr(o []byte) string {
	s := string(o)
	switch {
	case strings.Contains(s, "Wrong password"):
		return "SANDI: sandi salah"
	case strings.Contains(s, "Enter password"):
		return "SANDI: arsip terkunci, masukkan sandi"
	}
	return ""
}

func arcFail(o []byte, err error) error {
	if m := arcPwErr(o); m != "" {
		return errors.New(m)
	}
	return errors.New(arcTail(o, err))
}

func zipEncrypted(src string) bool {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return false
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Flags&1 != 0 || f.Method == 99 {
			return true
		}
	}
	return false
}

// Telusuri sumber (nama relatif terhadap d). Tautan simbolik tidak diikuti.
func arcWalk(d string, names []string, fn func(rel, full string, info fs.FileInfo) error) (int, error) {
	n := 0
	for _, name := range names {
		root := filepath.Join(d, name)
		err := filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := de.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(d, p)
			if err != nil {
				return err
			}
			n++
			if n > arcMaxEntries {
				return errors.New("terlalu banyak berkas (batas 100000)")
			}
			return fn(filepath.ToSlash(rel), p, info)
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func makeZip(d string, names []string, out string) (int, error) {
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return 0, err
	}
	zw := zip.NewWriter(f)
	n, err := arcWalk(d, names, func(rel, full string, info fs.FileInfo) error {
		m := info.Mode()
		if !(m.IsRegular() || m.IsDir() || m&os.ModeSymlink != 0) {
			return nil
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = rel
		h.Method = zip.Deflate
		if m.IsDir() {
			h.Name += "/"
			h.Method = zip.Store
			_, err = zw.CreateHeader(h)
			return err
		}
		if m&os.ModeSymlink != 0 {
			t, err := os.Readlink(full)
			if err != nil {
				return err
			}
			h.Method = zip.Store
			wr, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			_, err = io.WriteString(wr, t)
			return err
		}
		wr, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		in, err := os.Open(full)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(wr, in)
		return err
	})
	if err == nil {
		err = zw.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func makeTarGz(d string, names []string, out string) (int, error) {
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return 0, err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	n, err := arcWalk(d, names, func(rel, full string, info fs.FileInfo) error {
		m := info.Mode()
		if !(m.IsRegular() || m.IsDir() || m&os.ModeSymlink != 0) {
			return nil
		}
		link := ""
		if m&os.ModeSymlink != 0 {
			t, err := os.Readlink(full)
			if err != nil {
				return err
			}
			link = t
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = rel
		if m.IsDir() {
			h.Name += "/"
		}
		h.Uname, h.Gname = "", ""
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if !m.IsRegular() {
			return nil
		}
		in, err := os.Open(full)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.CopyN(tw, in, h.Size)
		return err
	})
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func make7z(bin, d string, names []string, out, typ, pw string) error {
	args := []string{"a", "-t" + typ, "-bd", "-y", "-spd"}
	if pw != "" {
		args = append(args, "-p")
		if typ == "7z" {
			args = append(args, "-mhe=on")
		} else {
			args = append(args, "-mem=AES256")
		}
	}
	args = append(args, out, "--")
	args = append(args, names...)
	if o, err := arcRun(bin, d, pw, 2, args...); err != nil {
		return arcFail(o, err)
	}
	return nil
}

func arcZipH(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir   string   `json:"dir"`
		Names []string `json:"names"`
		Fmt   string   `json:"fmt"`
		Out   string   `json:"out"`
		Pw    string   `json:"pw"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&in) != nil || len(in.Names) == 0 || len(in.Names) > 200 {
		http.Error(w, "permintaan tidak valid", 400)
		return
	}
	ext, ok := arcExt[in.Fmt]
	if !ok {
		http.Error(w, "format tidak dikenal", 400)
		return
	}
	if !arcPwOK(in.Pw) {
		http.Error(w, "sandi tidak valid (maks 256 karakter, tanpa baris baru)", 400)
		return
	}
	if in.Pw != "" && in.Fmt == "tar.gz" {
		http.Error(w, "tar.gz tidak punya enkripsi bawaan; pakai zip atau 7z untuk sandi", 400)
		return
	}
	bin := ""
	if in.Fmt == "7z" || (in.Fmt == "zip" && in.Pw != "") {
		bin = sevenZipBin()
		if bin == "" {
			http.Error(w, "biner 7zz belum ada di /data/adb/wumi/7zz", 501)
			return
		}
	}
	if !arcMu.TryLock() {
		http.Error(w, "operasi arsip lain sedang berjalan", 409)
		return
	}
	defer arcMu.Unlock()
	d, ok := safePath(in.Dir)
	if !ok {
		http.Error(w, "jalur tidak diizinkan", 403)
		return
	}
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		http.Error(w, "folder tidak valid", 400)
		return
	}
	if !writable(d) {
		http.Error(w, "folder ini hanya baca", 403)
		return
	}
	for _, n := range in.Names {
		p, msg := entryPath(in.Dir, n)
		if msg != "" {
			http.Error(w, n+": "+msg, 403)
			return
		}
		if _, err := os.Lstat(p); err != nil {
			http.Error(w, "tidak ada: "+n, 404)
			return
		}
	}
	out := strings.TrimSpace(in.Out)
	explicit := out != ""
	if !explicit {
		out = "arsip"
		if len(in.Names) == 1 {
			out = in.Names[0]
		} else if b := filepath.Base(d); b != "/" && b != "." {
			out = b
		}
	}
	if !strings.HasSuffix(strings.ToLower(out), ext) {
		out += ext
	}
	full, msg := childOf(in.Dir, out)
	if msg != "" {
		http.Error(w, msg, 403)
		return
	}
	if explicit {
		if _, err := os.Lstat(full); err == nil {
			http.Error(w, "sudah ada: "+out, 409)
			return
		}
	} else {
		stem := strings.TrimSuffix(out, ext)
		for i := 1; ; i++ {
			if _, err := os.Lstat(full); err != nil {
				break
			}
			if i > 999 {
				http.Error(w, "nama arsip penuh", 409)
				return
			}
			out = fmt.Sprintf("%s-%d%s", stem, i, ext)
			full, msg = childOf(in.Dir, out)
			if msg != "" {
				http.Error(w, msg, 403)
				return
			}
		}
	}
	n := 0
	var err error
	switch in.Fmt {
	case "zip":
		if in.Pw != "" {
			err = make7z(bin, d, in.Names, full, "zip", in.Pw)
		} else {
			n, err = makeZip(d, in.Names, full)
		}
	case "tar.gz":
		n, err = makeTarGz(d, in.Names, full)
	case "7z":
		err = make7z(bin, d, in.Names, full, "7z", in.Pw)
	}
	if err != nil {
		os.Remove(full)
		http.Error(w, "gagal mengarsip: "+errText(err), 500)
		return
	}
	log.Printf("files: arsip %s (%s) %d entri, sandi=%v", full, in.Fmt, n, in.Pw != "")
	f3json(w, map[string]any{"ok": true, "out": out, "n": n})
}

// Jalur di dalam dest. ".." dibuang secara leksikal. dest baru dan hanya berisi berkas/folder
// dari ekstrak ini (tanpa tautan simbolik), jadi tidak ada jalan keluar dari dest.
func arcSafeJoin(dest, name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.IndexByte(name, 0) >= 0 {
		return "", false
	}
	c := path.Clean("/" + name)
	if c == "/" {
		return "", false
	}
	full := filepath.Join(dest, filepath.FromSlash(c))
	if !strings.HasPrefix(full, dest+string(filepath.Separator)) {
		return "", false
	}
	return full, true
}

func arcWriteFile(full string, src io.Reader, mode os.FileMode, mt time.Time, lim *int64) error {
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(src, *lim+1))
	*lim -= n
	if err == nil && *lim < 0 {
		err = errors.New("isi arsip melebihi batas 32 GB")
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	os.Chmod(full, mode.Perm()|0600)
	if !mt.IsZero() {
		os.Chtimes(full, mt, mt)
	}
	return nil
}

func extractZip(src, dest string) (int, int, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return 0, 0, err
	}
	defer zr.Close()
	if len(zr.File) > arcMaxEntries {
		return 0, 0, errors.New("terlalu banyak berkas (batas 100000)")
	}
	lim := arcMaxBytes
	n, skip := 0, 0
	for _, f := range zr.File {
		full, ok := arcSafeJoin(dest, f.Name)
		if !ok {
			skip++
			continue
		}
		m := f.Mode()
		switch {
		case m.IsDir() || strings.HasSuffix(f.Name, "/"):
			if err := os.MkdirAll(full, 0755); err != nil {
				return n, skip, err
			}
		case m.IsRegular():
			rc, err := f.Open()
			if err != nil {
				return n, skip, err
			}
			err = arcWriteFile(full, rc, m, f.Modified, &lim)
			rc.Close()
			if err != nil {
				return n, skip, err
			}
		default:
			skip++
			continue
		}
		n++
	}
	return n, skip, nil
}

func extractTar(src, dest string, gz bool) (int, int, error) {
	f, err := os.Open(src)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var rd io.Reader = f
	if gz {
		g, err := gzip.NewReader(f)
		if err != nil {
			return 0, 0, err
		}
		defer g.Close()
		rd = g
	}
	tr := tar.NewReader(rd)
	lim := arcMaxBytes
	n, skip, cnt := 0, 0, 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return n, skip, err
		}
		cnt++
		if cnt > arcMaxEntries {
			return n, skip, errors.New("terlalu banyak berkas (batas 100000)")
		}
		full, ok := arcSafeJoin(dest, h.Name)
		if !ok {
			skip++
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(full, 0755); err != nil {
				return n, skip, err
			}
		case tar.TypeReg:
			if err := arcWriteFile(full, tr, os.FileMode(h.Mode), h.ModTime, &lim); err != nil {
				return n, skip, err
			}
		default:
			skip++
			continue
		}
		n++
	}
	return n, skip, nil
}

func extract7z(bin, src, dest, pw string) (int, int, error) {
	if o, err := arcRun(bin, dest, pw, 1, "x", "-y", "-bd", "-o"+dest, "--", src); err != nil {
		return 0, 0, arcFail(o, err)
	}
	n, skip := 0, 0
	filepath.WalkDir(dest, func(p string, de fs.DirEntry, err error) error {
		if err != nil || p == dest {
			return nil
		}
		if de.Type()&fs.ModeSymlink != 0 {
			os.Remove(p)
			skip++
			return nil
		}
		n++
		return nil
	})
	return n, skip, nil
}

func arcUnzipH(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir  string `json:"dir"`
		Name string `json:"name"`
		To   string `json:"to"`
		Pw   string `json:"pw"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !arcPwOK(in.Pw) {
		http.Error(w, "permintaan tidak valid", 400)
		return
	}
	kind := arcKind(in.Name)
	if kind == "" {
		http.Error(w, "format tidak didukung (zip, tar, tar.gz, tgz, 7z, rar)", 400)
		return
	}
	if !arcMu.TryLock() {
		http.Error(w, "operasi arsip lain sedang berjalan", 409)
		return
	}
	defer arcMu.Unlock()
	d, ok := safePath(in.Dir)
	if !ok {
		http.Error(w, "jalur tidak diizinkan", 403)
		return
	}
	if !writable(d) {
		http.Error(w, "folder ini hanya baca", 403)
		return
	}
	src, msg := entryPath(in.Dir, in.Name)
	if msg != "" {
		http.Error(w, msg, 403)
		return
	}
	if st, err := os.Stat(src); err != nil || !st.Mode().IsRegular() {
		http.Error(w, "arsip tidak ada", 404)
		return
	}
	bin := ""
	use7 := kind == "7z" || kind == "rar"
	if kind == "zip" && zipEncrypted(src) {
		use7 = true
	}
	if use7 {
		bin = sevenZipBin()
		if bin == "" {
			http.Error(w, "biner 7zz belum ada di /data/adb/wumi/7zz (dibutuhkan untuk 7z, rar, dan zip bersandi)", 501)
			return
		}
		if kind == "zip" && in.Pw == "" {
			http.Error(w, "SANDI: arsip terkunci, masukkan sandi", 403)
			return
		}
	}
	to := strings.TrimSpace(in.To)
	if to == "" {
		to = arcStem(in.Name)
	}
	dest, msg := childOf(in.Dir, to)
	if msg != "" {
		http.Error(w, msg, 403)
		return
	}
	if _, err := os.Lstat(dest); err == nil {
		http.Error(w, "sudah ada: "+to, 409)
		return
	}
	if err := os.Mkdir(dest, 0755); err != nil {
		http.Error(w, "gagal membuat folder: "+errText(err), 500)
		return
	}
	var n, skip int
	var err error
	switch {
	case use7:
		n, skip, err = extract7z(bin, src, dest, in.Pw)
	case kind == "zip":
		n, skip, err = extractZip(src, dest)
	case kind == "tar":
		n, skip, err = extractTar(src, dest, false)
	case kind == "tar.gz":
		n, skip, err = extractTar(src, dest, true)
	}
	if err != nil {
		os.RemoveAll(dest)
		if m := err.Error(); strings.HasPrefix(m, "SANDI:") {
			http.Error(w, m, 403)
			return
		}
		http.Error(w, "gagal mengekstrak: "+errText(err), 500)
		return
	}
	log.Printf("files: ekstrak %s -> %s (%d entri, %d dilewati)", src, dest, n, skip)
	f3json(w, map[string]any{"ok": true, "out": to, "n": n, "skip": skip})
}

// ---- Isi arsip (lihat daftar berkas, termasuk arsip bersandi) ----

const arcListMax = 3000

type arcEnt struct {
	N string `json:"n"`
	S int64  `json:"s"`
	D bool   `json:"d,omitempty"`
	E bool   `json:"e,omitempty"`
}

func arcListZip(src string) ([]arcEnt, int, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, 0, err
	}
	defer zr.Close()
	var out []arcEnt
	for _, f := range zr.File {
		if len(out) < arcListMax {
			out = append(out, arcEnt{N: f.Name, S: int64(f.UncompressedSize64), D: f.Mode().IsDir() || strings.HasSuffix(f.Name, "/"), E: f.Flags&1 != 0 || f.Method == 99})
		}
	}
	return out, len(zr.File), nil
}

func arcListTar(src string, gz bool) ([]arcEnt, int, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var rd io.Reader = f
	if gz {
		g, err := gzip.NewReader(f)
		if err != nil {
			return nil, 0, err
		}
		defer g.Close()
		rd = g
	}
	tr := tar.NewReader(rd)
	var out []arcEnt
	total := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, total, err
		}
		total++
		if total > arcMaxEntries {
			return out, total, errors.New("terlalu banyak berkas (batas 100000)")
		}
		if len(out) < arcListMax {
			out = append(out, arcEnt{N: h.Name, S: h.Size, D: h.Typeflag == tar.TypeDir})
		}
	}
	return out, total, nil
}

// Keluaran "7zz l -slt -ba": blok "Key = value" per entri, dipisah baris kosong.
func arcParse7z(o []byte) ([]arcEnt, int) {
	var out []arcEnt
	total := 0
	var cur *arcEnt
	flush := func() {
		if cur != nil {
			total++
			if len(out) < arcListMax {
				out = append(out, *cur)
			}
			cur = nil
		}
	}
	for _, ln := range strings.Split(string(o), "\n") {
		ln = strings.TrimRight(ln, "\r")
		switch {
		case strings.HasPrefix(ln, "Path = "):
			flush()
			cur = &arcEnt{N: ln[7:]}
		case cur == nil:
		case strings.HasPrefix(ln, "Folder = "):
			cur.D = strings.TrimSpace(ln[9:]) == "+"
		case strings.HasPrefix(ln, "Attributes = "):
			if strings.HasPrefix(strings.TrimSpace(ln[13:]), "D") {
				cur.D = true
			}
		case strings.HasPrefix(ln, "Size = "):
			fmt.Sscan(strings.TrimSpace(ln[7:]), &cur.S)
		case strings.HasPrefix(ln, "Encrypted = "):
			cur.E = strings.TrimSpace(ln[12:]) == "+"
		}
	}
	flush()
	return out, total
}

func arcListH(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir  string `json:"dir"`
		Name string `json:"name"`
		Pw   string `json:"pw"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || !arcPwOK(in.Pw) {
		http.Error(w, "permintaan tidak valid", 400)
		return
	}
	kind := arcKind(in.Name)
	if kind == "" {
		http.Error(w, "format tidak didukung (zip, tar, tar.gz, tgz, 7z, rar)", 400)
		return
	}
	if _, ok := safePath(in.Dir); !ok {
		http.Error(w, "jalur tidak diizinkan", 403)
		return
	}
	src, msg := entryPath(in.Dir, in.Name)
	if msg != "" {
		http.Error(w, msg, 403)
		return
	}
	if st, err := os.Stat(src); err != nil || !st.Mode().IsRegular() {
		http.Error(w, "arsip tidak ada", 404)
		return
	}
	var ents []arcEnt
	total := 0
	var err error
	verified := false
	switch kind {
	case "zip":
		ents, total, err = arcListZip(src)
	case "tar":
		ents, total, err = arcListTar(src, false)
	case "tar.gz":
		ents, total, err = arcListTar(src, true)
	}
	anyEnc := false
	for _, e := range ents {
		if e.E {
			anyEnc = true
			break
		}
	}
	if kind == "7z" || kind == "rar" || (kind == "zip" && err == nil && anyEnc && in.Pw != "") {
		bin := sevenZipBin()
		if bin == "" {
			if kind == "zip" {
				err = nil // daftar nama tetap tampil; sandi tak bisa diuji tanpa 7zz
				goto done
			}
			http.Error(w, "biner 7zz belum ada di /data/adb/wumi/7zz", 501)
			return
		}
		if kind != "zip" {
			o, e := arcRun(bin, filepath.Dir(src), in.Pw, 1, "l", "-slt", "-ba", "-bd", "-sccUTF-8", "--", src)
			if e != nil {
				http.Error(w, arcFail(o, e).Error(), 403)
				return
			}
			ents, total = arcParse7z(o)
			err = nil
			anyEnc = false
			for _, e := range ents {
				if e.E {
					anyEnc = true
					break
				}
			}
		}
		if anyEnc && in.Pw != "" {
			first := ""
			for _, e := range ents {
				if e.E && !e.D {
					first = e.N
					break
				}
			}
			if first != "" {
				o, e := arcRun(bin, filepath.Dir(src), in.Pw, 1, "t", "-bd", "-spd", "--", src, first)
				if e != nil {
					http.Error(w, arcFail(o, e).Error(), 403)
					return
				}
				verified = true
			}
		}
	}
done:
	if err != nil && len(ents) == 0 {
		http.Error(w, "gagal membaca arsip: "+errText(err), 500)
		return
	}
	f3json(w, map[string]any{"ok": true, "kind": kind, "total": total, "entries": ents, "enc": anyEnc, "verified": verified, "partial": err != nil})
}

func registerArc(mux *http.ServeMux, post func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("/api/files/arcinfo", needAuth(arcInfoH))
	mux.HandleFunc("/api/files/zip", post(arcZipH))
	mux.HandleFunc("/api/files/unzip", post(arcUnzipH))
	mux.HandleFunc("/api/files/arclist", post(arcListH))
}
