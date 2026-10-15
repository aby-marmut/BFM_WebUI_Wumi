package main

// bfrprov.go: BFR > Config > kartu "Update subscription" (clash | proxy provider | rule provider):
// daftar, unggah, unduh, edit, hapus; serta Backup cfg / Restore cfg (.tar.gz).
// Bergantung pada bfr.go (bfrP, bfrJSON, bfrFail, bfrBody, bfrBackup, bfrMu, bfrReadSettings)
// dan main.go (validName). Hanya pustaka standar.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	provMaxEdit  = 32 << 20  // batas Lihat/Edit teks
	provMaxYaml  = 8 << 20   // batas pemeriksaan isi .yaml
	rsMaxEntries = 5000      // batas entri arsip restore
	rsMaxTotal   = 256 << 20 // batas total isi arsip restore
	cfgMaxDepth  = 6         // kedalaman maksimum jalur di bawah clash/
)

type provDir struct {
	Rel  string // relatif terhadap <box>/clash
	Yaml bool   // hanya .yaml/.yml
}

var provDirs = map[string]provDir{
	"clash":          {"", true},
	"proxy_provider": {"proxy_provider", false},
	"rule_provider":  {"rule_provider", false},
}

func provPath(key string) (string, provDir, bool) {
	d, ok := provDirs[key]
	if !ok {
		return "", d, false
	}
	return bfrP("clash", d.Rel), d, true
}

func provName(d provDir, n string) error {
	if !validName(n) || strings.HasPrefix(n, ".") {
		return fmt.Errorf("nama berkas tidak valid")
	}
	if d.Yaml {
		if e := strings.ToLower(filepath.Ext(n)); e != ".yaml" && e != ".yml" {
			return fmt.Errorf("hanya berkas .yaml/.yml")
		}
	}
	return nil
}

// provFile: jalur lengkap berkas biasa (bukan symlink/folder) di dalam folder kartu.
func provFile(key, name string) (string, os.FileInfo, error) {
	dir, d, ok := provPath(key)
	if !ok {
		return "", nil, fmt.Errorf("folder tidak dikenal")
	}
	if err := provName(d, name); err != nil {
		return "", nil, err
	}
	p := filepath.Join(dir, name)
	st, err := os.Lstat(p)
	if err != nil {
		return "", nil, fmt.Errorf("berkas tidak ada")
	}
	if !st.Mode().IsRegular() {
		return "", nil, fmt.Errorf("bukan berkas biasa")
	}
	return p, st, nil
}

// provPost: hanya POST, wajib header X-Requested-With: bfr, batas body sesuai parameter.
func provPost(h http.HandlerFunc, limit int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			bfrFail(w, 405, "harus POST")
			return
		}
		if r.Header.Get("X-Requested-With") != "bfr" {
			bfrFail(w, 403, "header tidak valid")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		h(w, r)
	}
}

// placeFile menaruh src di dst. Bila dst sudah ada, isinya ditimpa di tempat
// (pemilik/izin/label SELinux tetap); bila belum, src dipindah (atau disalin lintas filesystem).
func placeFile(src, dst string) error {
	if st, err := os.Lstat(dst); err == nil {
		if !st.Mode().IsRegular() {
			return fmt.Errorf("bukan berkas biasa: %s", filepath.Base(dst))
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0)
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
		return xerr
	}
	os.Chmod(src, 0644)
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	xerr := out.Close()
	if cerr != nil {
		os.Remove(dst)
		return cerr
	}
	return xerr
}

// ---------- daftar / unduh / teks / simpan ----------

func provList(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("d")
	dir, d, ok := provPath(key)
	if !ok {
		bfrFail(w, 400, "folder tidak dikenal")
		return
	}
	active := bfrReadSettings()["name_clash_config"].str
	type row struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		Mtime  int64  `json:"mtime"`
		Active bool   `json:"active"`
		Edit   bool   `json:"edit"`
	}
	rows := []row{}
	ents, rerr := os.ReadDir(dir)
	for _, e := range ents {
		if len(rows) >= 2000 {
			break
		}
		n := e.Name()
		if provName(d, n) != nil {
			continue
		}
		st, err := os.Lstat(filepath.Join(dir, n))
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		rows = append(rows, row{n, st.Size(), st.ModTime().Unix(), key == "clash" && n == active, st.Size() <= provMaxEdit})
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	bfrJSON(w, map[string]interface{}{"dir": dir, "exists": rerr == nil, "files": rows})
}

func provGet(w http.ResponseWriter, r *http.Request) {
	p, st, err := provFile(r.URL.Query().Get("d"), r.URL.Query().Get("n"))
	if err != nil {
		bfrFail(w, 404, err.Error())
		return
	}
	f, err := os.Open(p)
	if err != nil {
		bfrFail(w, 500, "berkas tidak terbaca")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": st.Name()}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func provText(w http.ResponseWriter, r *http.Request) {
	p, st, err := provFile(r.URL.Query().Get("d"), r.URL.Query().Get("n"))
	if err != nil {
		bfrFail(w, 404, err.Error())
		return
	}
	if st.Size() > provMaxEdit {
		bfrFail(w, 413, "berkas lebih dari 32 MB, gunakan Unduh")
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		bfrFail(w, 500, "berkas tidak terbaca")
		return
	}
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		bfrFail(w, 415, "bukan teks UTF-8, gunakan Unduh")
		return
	}
	bfrJSON(w, map[string]interface{}{"text": string(b), "mtime": st.ModTime().Unix(), "size": st.Size()})
}

// Simpan teks ke berkas yang sudah ada: cadangan ke webui-backups dulu, lalu tulis di tempat.
func provSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		D     string `json:"d"`
		N     string `json:"n"`
		Text  string `json:"text"`
		Mtime int64  `json:"mtime"`
	}
	if err := bfrBody(r, &in); err != nil {
		bfrFail(w, 400, "permintaan tidak valid atau lebih dari 64 MB")
		return
	}
	if len(in.Text) > provMaxEdit {
		bfrFail(w, 413, "teks lebih dari 32 MB")
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		bfrFail(w, 400, "isi kosong ditolak")
		return
	}
	p, st, err := provFile(in.D, in.N)
	if err != nil {
		bfrFail(w, 404, err.Error())
		return
	}
	if in.Mtime != 0 && st.ModTime().Unix() != in.Mtime {
		bfrFail(w, 409, "berkas sudah berubah, buka ulang")
		return
	}
	bfrMu.Lock()
	defer bfrMu.Unlock()
	bk, err := bfrBackup(p, in.D+"."+in.N)
	if err != nil {
		bfrFail(w, 500, "gagal membuat cadangan: "+err.Error())
		return
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		bfrFail(w, 500, "gagal membuka berkas")
		return
	}
	_, werr := io.WriteString(f, in.Text)
	serr := f.Sync()
	cerr := f.Close()
	if werr != nil || serr != nil || cerr != nil {
		bfrFail(w, 500, "gagal menulis; cadangan: "+filepath.Base(bk))
		return
	}
	bfrJSON(w, map[string]interface{}{"saved": true, "backup": filepath.Base(bk)})
}

// ---------- unggah / hapus ----------

// provWrite menulis satu berkas unggahan ke dir. Berkas lama dicadangkan lebih dulu.
func provWrite(dir, key, name string, src io.Reader) (bool, error) {
	target := filepath.Join(dir, name)
	replaced := false
	if st, err := os.Lstat(target); err == nil {
		if !st.Mode().IsRegular() {
			return false, fmt.Errorf("bukan berkas biasa")
		}
		replaced = true
	}
	tmp, err := os.CreateTemp(dir, ".up-*")
	if err != nil {
		return false, fmt.Errorf("gagal membuat berkas sementara")
	}
	tn := tmp.Name()
	defer os.Remove(tn)
	n, err := io.Copy(tmp, src)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, fmt.Errorf("unggahan terputus atau terlalu besar")
	}
	if n == 0 {
		return false, fmt.Errorf("berkas kosong ditolak")
	}
	if e := strings.ToLower(filepath.Ext(name)); (e == ".yaml" || e == ".yml") && n <= provMaxYaml {
		b, rerr := os.ReadFile(tn)
		if rerr != nil || !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
			return false, fmt.Errorf("bukan teks UTF-8 yang valid")
		}
	}
	bfrMu.Lock()
	defer bfrMu.Unlock()
	if replaced {
		if _, err := bfrBackup(target, key+"."+name); err != nil {
			return false, fmt.Errorf("gagal membuat cadangan")
		}
	}
	if err := placeFile(tn, target); err != nil {
		return false, fmt.Errorf("gagal menyimpan")
	}
	return replaced, nil
}

func provUpload(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("d")
	dir, d, ok := provPath(key)
	if !ok {
		bfrFail(w, 400, "folder tidak dikenal")
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		bfrFail(w, 400, "permintaan tidak valid")
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		bfrFail(w, 500, "folder tujuan tidak bisa dibuat")
		return
	}
	n := 0
	errs := []string{}
	replaced := []string{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			errs = append(errs, "unggahan terputus atau terlalu besar")
			break
		}
		if part.FileName() == "" {
			continue
		}
		name := filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		if e := provName(d, name); e != nil {
			errs = append(errs, name+": "+e.Error())
			continue
		}
		rep, e := provWrite(dir, key, name, part)
		if e != nil {
			errs = append(errs, name+": "+e.Error())
			continue
		}
		if rep {
			replaced = append(replaced, name)
		}
		n++
	}
	bfrJSON(w, map[string]interface{}{"ok": n, "replaced": replaced, "err": errs})
}

func provDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		D     string   `json:"d"`
		Names []string `json:"names"`
	}
	if bfrBody(r, &in) != nil || len(in.Names) == 0 || len(in.Names) > 200 {
		bfrFail(w, 400, "permintaan tidak valid")
		return
	}
	active := bfrReadSettings()["name_clash_config"].str
	n := 0
	errs := []string{}
	for _, name := range in.Names {
		p, _, err := provFile(in.D, name)
		if err != nil {
			errs = append(errs, name+": "+err.Error())
			continue
		}
		if in.D == "clash" && name == active {
			errs = append(errs, name+": config aktif, jadikan config lain aktif dulu")
			continue
		}
		bfrMu.Lock()
		_, e := bfrBackup(p, in.D+"."+name)
		if e == nil {
			e = os.Remove(p)
		}
		bfrMu.Unlock()
		if e != nil {
			errs = append(errs, name+": gagal dihapus")
			continue
		}
		n++
	}
	bfrJSON(w, map[string]interface{}{"ok": n, "err": errs})
}

// ---------- Backup cfg / Restore cfg ----------

type cfgItem struct {
	abs, rel string
	st       os.FileInfo
}

// cfgItems: yang ikut dicadangkan = berkas .yaml/.yml utama di <box>/clash, serta isi
// folder proxy_provider dan rule_provider.
func cfgItems() []cfgItem {
	base := bfrP("clash")
	var out []cfgItem
	yd := provDirs["clash"]
	if ents, err := os.ReadDir(base); err == nil {
		for _, e := range ents {
			if provName(yd, e.Name()) != nil {
				continue
			}
			p := filepath.Join(base, e.Name())
			if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() {
				out = append(out, cfgItem{p, e.Name(), st})
			}
		}
	}
	for _, sub := range []string{"proxy_provider", "rule_provider"} {
		root := filepath.Join(base, sub)
		if st, err := os.Lstat(root); err != nil || !st.IsDir() {
			continue
		}
		filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if p != root && (!validName(de.Name()) || strings.HasPrefix(de.Name(), ".")) {
				if de.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, e := filepath.Rel(base, p)
			if e != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if len(strings.Split(rel, "/")) > cfgMaxDepth {
				if de.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			st, e := os.Lstat(p)
			if e != nil || !(st.IsDir() || st.Mode().IsRegular()) {
				return nil
			}
			out = append(out, cfgItem{p, rel, st})
			return nil
		})
	}
	return out
}

// cfgWriteTar menulis arsip .tar.gz (entri berawalan clash/). Hasil: jumlah berkas.
func cfgWriteTar(dst io.Writer) (int, error) {
	gz := gzip.NewWriter(dst)
	tw := tar.NewWriter(gz)
	files := 0
	for _, it := range cfgItems() {
		hdr, err := tar.FileInfoHeader(it.st, "")
		if err != nil {
			return files, err
		}
		hdr.Name = "clash/" + it.rel
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		if it.st.IsDir() {
			hdr.Name += "/"
			if err := tw.WriteHeader(hdr); err != nil {
				return files, err
			}
			continue
		}
		f, err := os.Open(it.abs)
		if err != nil {
			return files, err
		}
		if err := tw.WriteHeader(hdr); err != nil {
			f.Close()
			return files, err
		}
		_, err = io.CopyN(tw, f, hdr.Size)
		f.Close()
		if err != nil {
			return files, err
		}
		files++
	}
	if err := tw.Close(); err != nil {
		return files, err
	}
	return files, gz.Close()
}

func cfgBackupH(w http.ResponseWriter, r *http.Request) {
	tdir := bfrP("webui-tmp")
	if err := os.MkdirAll(tdir, 0700); err != nil {
		bfrFail(w, 500, err.Error())
		return
	}
	f, err := os.CreateTemp(tdir, "bk-*.tar.gz")
	if err != nil {
		bfrFail(w, 500, "gagal membuat berkas sementara")
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	cnt, err := cfgWriteTar(f)
	if err != nil {
		bfrFail(w, 500, "gagal membuat arsip: "+err.Error())
		return
	}
	if cnt == 0 {
		bfrFail(w, 404, "tidak ada berkas untuk dicadangkan")
		return
	}
	st, err := f.Stat()
	if err != nil {
		bfrFail(w, 500, err.Error())
		return
	}
	name := "bfr-cfg-" + time.Now().Format("20060102-150405") + ".tar.gz"
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// cfgExtract membaca arsip, memvalidasi SETIAP entri, dan mengekstrak ke folder staging stg.
// Diterima hanya: clash/<nama>.yaml|yml, serta berkas/folder di bawah clash/proxy_provider
// dan clash/rule_provider. Link, jalur "..", jalur absolut, nama tersembunyi ditolak.
func cfgExtract(src io.Reader, stg string) (int, error) {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return 0, fmt.Errorf("bukan berkas .tar.gz")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files, entries := 0, 0
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("arsip rusak: %v", err)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		entries++
		if entries > rsMaxEntries {
			return 0, fmt.Errorf("terlalu banyak entri di arsip")
		}
		name := strings.TrimSuffix(strings.TrimPrefix(hdr.Name, "./"), "/")
		if name == "clash" && hdr.Typeflag == tar.TypeDir {
			continue
		}
		if !strings.HasPrefix(name, "clash/") {
			return 0, fmt.Errorf("entri di luar clash/: %q", hdr.Name)
		}
		rel := strings.TrimPrefix(name, "clash/")
		parts := strings.Split(rel, "/")
		if len(parts) > cfgMaxDepth {
			return 0, fmt.Errorf("jalur terlalu dalam: %q", hdr.Name)
		}
		for _, p := range parts {
			if !validName(p) || strings.HasPrefix(p, ".") {
				return 0, fmt.Errorf("nama tidak valid: %q", hdr.Name)
			}
		}
		top := len(parts) == 1
		sub := parts[0] == "proxy_provider" || parts[0] == "rule_provider"
		dst := filepath.Join(stg, filepath.FromSlash(rel))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if !sub {
				return 0, fmt.Errorf("folder tidak diizinkan: %q", hdr.Name)
			}
			if err := os.MkdirAll(dst, 0755); err != nil {
				return 0, err
			}
		case tar.TypeReg:
			e := strings.ToLower(filepath.Ext(rel))
			if !((top && (e == ".yaml" || e == ".yml")) || (sub && !top)) {
				return 0, fmt.Errorf("berkas tidak diizinkan: %q", hdr.Name)
			}
			if top && hdr.Size > provMaxYaml {
				return 0, fmt.Errorf("yaml terlalu besar: %q", hdr.Name)
			}
			total += hdr.Size
			if total > rsMaxTotal {
				return 0, fmt.Errorf("isi arsip terlalu besar")
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				return 0, err
			}
			f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
			if err != nil {
				return 0, fmt.Errorf("entri ganda atau gagal menulis: %q", hdr.Name)
			}
			_, cerr := io.Copy(f, tr)
			xerr := f.Close()
			if cerr != nil || xerr != nil {
				return 0, fmt.Errorf("gagal membaca entri: %q", hdr.Name)
			}
			if top {
				b, rerr := os.ReadFile(dst)
				if rerr != nil || len(b) == 0 || !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
					return 0, fmt.Errorf("yaml tidak valid: %q", hdr.Name)
				}
			}
			files++
		default:
			return 0, fmt.Errorf("jenis entri tidak diizinkan (link/khusus): %q", hdr.Name)
		}
	}
	return files, nil
}

func cfgPrunePre(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var l []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "cfg-pre-restore-") {
			l = append(l, e.Name())
		}
	}
	sort.Strings(l)
	for len(l) > 5 {
		os.Remove(filepath.Join(dir, l[0]))
		l = l[1:]
	}
}

func cfgRestoreH(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		bfrFail(w, 400, "permintaan tidak valid")
		return
	}
	tdir := bfrP("webui-tmp")
	if err := os.MkdirAll(tdir, 0700); err != nil {
		bfrFail(w, 500, err.Error())
		return
	}
	up, err := os.CreateTemp(tdir, "rs-*.tar.gz")
	if err != nil {
		bfrFail(w, 500, "gagal membuat berkas sementara")
		return
	}
	defer os.Remove(up.Name())
	defer up.Close()
	got := false
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			bfrFail(w, 400, "unggahan terputus atau terlalu besar")
			return
		}
		if part.FileName() == "" {
			continue
		}
		if _, err := io.Copy(up, part); err != nil {
			bfrFail(w, 400, "unggahan terputus atau terlalu besar")
			return
		}
		got = true
		break
	}
	if !got {
		bfrFail(w, 400, "berkas tidak dikirim")
		return
	}
	if _, err := up.Seek(0, io.SeekStart); err != nil {
		bfrFail(w, 500, err.Error())
		return
	}
	stg, err := os.MkdirTemp(tdir, "rs-stg-")
	if err != nil {
		bfrFail(w, 500, "gagal membuat folder sementara")
		return
	}
	defer os.RemoveAll(stg)
	cnt, err := cfgExtract(up, stg)
	if err != nil {
		bfrFail(w, 400, err.Error())
		return
	}
	if cnt == 0 {
		bfrFail(w, 400, "arsip tidak berisi berkas cadangan BFR")
		return
	}

	base := bfrP("clash")
	type mv struct {
		src, dst string
		dir      bool
	}
	var list []mv
	filepath.WalkDir(stg, func(p string, de fs.DirEntry, err error) error {
		if err != nil || p == stg {
			return nil
		}
		rel, e := filepath.Rel(stg, p)
		if e != nil {
			return nil
		}
		list = append(list, mv{p, filepath.Join(base, rel), de.IsDir()})
		return nil
	})
	for _, m := range list {
		if st, err := os.Lstat(m.dst); err == nil {
			if (m.dir && !st.IsDir()) || (!m.dir && !st.Mode().IsRegular()) {
				bfrFail(w, 400, "tujuan bukan jenis yang sama: "+filepath.Base(m.dst))
				return
			}
		}
	}

	// cadangan otomatis kondisi sekarang, sebelum menimpa apa pun
	bdir := bfrP("webui-backups")
	if err := os.MkdirAll(bdir, 0755); err != nil {
		bfrFail(w, 500, err.Error())
		return
	}
	pre := filepath.Join(bdir, "cfg-pre-restore-"+time.Now().Format("20060102-150405")+".tar.gz")
	pf, err := os.Create(pre)
	if err != nil {
		bfrFail(w, 500, "gagal membuat cadangan sebelum pulihkan")
		return
	}
	_, werr := cfgWriteTar(pf)
	cerr := pf.Close()
	if werr != nil || cerr != nil {
		os.Remove(pre)
		bfrFail(w, 500, "gagal membuat cadangan sebelum pulihkan")
		return
	}
	cfgPrunePre(bdir)

	bfrMu.Lock()
	defer bfrMu.Unlock()
	for _, m := range list {
		if m.dir {
			if err := os.MkdirAll(m.dst, 0755); err != nil {
				bfrFail(w, 500, "gagal membuat folder "+filepath.Base(m.dst)+" (cadangan: "+filepath.Base(pre)+")")
				return
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(m.dst), 0755); err != nil {
			bfrFail(w, 500, "gagal membuat folder (cadangan: "+filepath.Base(pre)+")")
			return
		}
		if err := placeFile(m.src, m.dst); err != nil {
			bfrFail(w, 500, fmt.Sprintf("gagal memulihkan %s: %v (cadangan: %s)", filepath.Base(m.dst), err, filepath.Base(pre)))
			return
		}
	}
	bfrJSON(w, map[string]interface{}{"restored": cnt, "pre_backup": filepath.Base(pre)})
}

// registerBFRProv dipanggil sekali dari main.go, tepat setelah registerBFR(mux, needAuth).
func registerBFRProv(mux *http.ServeMux, auth func(http.HandlerFunc) http.HandlerFunc) {
	get := func(p string, f http.HandlerFunc) { mux.HandleFunc(p, auth(f)) }
	post := func(p string, f http.HandlerFunc, lim int64) { mux.HandleFunc(p, auth(provPost(f, lim))) }
	get("/api/bfr/prov/list", provList)
	get("/api/bfr/prov/get", provGet)
	get("/api/bfr/prov/text", provText)
	post("/api/bfr/prov/save", provSave, 64<<20)
	post("/api/bfr/prov/upload", provUpload, 64<<20)
	post("/api/bfr/prov/delete", provDelete, 64<<10)
	get("/api/bfr/cfg/backup", cfgBackupH)
	post("/api/bfr/cfg/restore", cfgRestoreH, 64<<20)
}
