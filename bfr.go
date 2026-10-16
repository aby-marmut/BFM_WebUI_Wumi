package main

// bfr.go: backend halaman BFR (Home, Config, Apps, Logs, Settings) untuk wumi.
// Mandiri: tidak bergantung pada fungsi lain di main.go selain mux dan pembungkus auth
// yang dioper lewat registerBFR.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed bfr_page.html
var bfrPage []byte

func bfrEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var (
	bfrDir     = bfrEnv("BFR_DIR", "/data/adb/box")
	bfrModProp = bfrEnv("BFR_MODPROP", "/data/adb/modules/box_for_root/module.prop")
	bfrSh      = bfrEnv("BFR_SH", "sh")
	bfrMu      sync.Mutex
)

func bfrP(parts ...string) string {
	return filepath.Join(append([]string{bfrDir}, parts...)...)
}

// ---------- util HTTP ----------

func bfrJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func bfrFail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// bfrPost: hanya POST, wajib header X-Requested-With (penangkal CSRF), batas body 8 MB.
func bfrPost(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			bfrFail(w, 405, "harus POST")
			return
		}
		if r.Header.Get("X-Requested-With") != "bfr" {
			bfrFail(w, 403, "header tidak valid")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		h(w, r)
	}
}

func bfrBody(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ---------- util sistem ----------

// bfrExec menjalankan perintah, keluaran ke berkas sementara (bukan pipe) supaya
// proses daemon anak yang mewarisi stdout tidak membuat Wait menggantung.
func bfrExec(timeout time.Duration, name string, args ...string) (string, error) {
	tdir := bfrP("webui-tmp")
	os.MkdirAll(tdir, 0700)
	f, err := os.CreateTemp(tdir, "out-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Env = append(os.Environ(), "PATH=/data/adb/magisk:/data/adb/ksu/bin:/data/adb/ap/bin:"+os.Getenv("PATH")+":/system/bin:/system/xbin")
	err = cmd.Run()
	b, _ := os.ReadFile(f.Name())
	if len(b) > 30000 {
		b = b[len(b)-30000:]
	}
	if ctx.Err() == context.DeadlineExceeded {
		return string(b), fmt.Errorf("timeout setelah %s", timeout)
	}
	return string(b), err
}

func bfrRunning() (bool, string) {
	b, err := os.ReadFile(bfrP("run", "box.pid"))
	if err != nil {
		return false, ""
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return false, ""
	}
	if _, err := strconv.Atoi(f[0]); err != nil {
		return false, ""
	}
	if _, err := os.Stat("/proc/" + f[0]); err != nil {
		return false, f[0]
	}
	return true, f[0]
}

func bfrTail(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	const max = 1 << 20
	size := st.Size()
	start := int64(0)
	if size > max {
		start = size - max
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && int64(len(buf)) > 0 {
		// ReadAt dapat mengembalikan EOF bersama data; abaikan bila ada data
		if len(buf) == 0 {
			return "", err
		}
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // baris pertama mungkin terpotong
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

func bfrBackup(path, label string) (string, error) {
	dir := bfrP("webui-backups")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, label+"."+time.Now().Format("20060102-150405"))
	if err := os.WriteFile(dst, b, 0644); err != nil {
		return "", err
	}
	// simpan 20 terbaru per label
	ents, _ := os.ReadDir(dir)
	var mine []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), label+".") {
			mine = append(mine, e.Name())
		}
	}
	sort.Strings(mine)
	for len(mine) > 20 {
		os.Remove(filepath.Join(dir, mine[0]))
		mine = mine[1:]
	}
	return dst, nil
}

// ---------- parser settings.ini (skrip shell) ----------

type bfrVal struct {
	kind   string // "str" atau "list"
	str    string
	list   []string
	quoted bool
	ok     bool
}

var (
	bfrKV   = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	bfrItem = regexp.MustCompile(`"([^"]*)"`)
	bfrBare = regexp.MustCompile(`^[A-Za-z0-9_.:/-]+$`)
)

func bfrUnsafe(s string) bool {
	return strings.ContainsAny(s, "$`\\\"\n\r")
}

func bfrParseVal(raw string) bfrVal {
	raw = strings.TrimSpace(raw)
	switch {
	case len(raw) >= 2 && raw[0] == '(' && raw[len(raw)-1] == ')':
		inner := raw[1 : len(raw)-1]
		if strings.ContainsAny(inner, "$`\\") {
			return bfrVal{}
		}
		if strings.TrimSpace(bfrItem.ReplaceAllString(inner, "")) != "" {
			return bfrVal{}
		}
		l := []string{}
		for _, m := range bfrItem.FindAllStringSubmatch(inner, -1) {
			l = append(l, m[1])
		}
		return bfrVal{kind: "list", list: l, ok: true}
	case len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"':
		inner := raw[1 : len(raw)-1]
		if bfrUnsafe(inner) {
			return bfrVal{}
		}
		return bfrVal{kind: "str", str: inner, quoted: true, ok: true}
	case bfrBare.MatchString(raw):
		return bfrVal{kind: "str", str: raw, ok: true}
	}
	return bfrVal{}
}

func bfrReadSettings() map[string]bfrVal {
	out := map[string]bfrVal{}
	b, err := os.ReadFile(bfrP("settings.ini"))
	if err != nil {
		return out
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if m := bfrKV.FindStringSubmatch(strings.TrimRight(ln, "\r")); m != nil {
			if _, dup := out[m[1]]; !dup {
				out[m[1]] = bfrParseVal(m[2])
			}
		}
	}
	return out
}

// ---------- daftar putih field Settings ----------

type bfrField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Group    string   `json:"group"`
	Kind     string   `json:"kind"` // bool enum num name cron ug mem text list
	Sub      string   `json:"-"`    // validasi item list: "name" atau ""
	Opts     []string `json:"opts,omitempty"`
	Value    string   `json:"value"`
	List     []string `json:"list"`
	Present  bool     `json:"present"`
	Editable bool     `json:"editable"`
}

var bfrDefs = []bfrField{
	{Key: "bin_name", Label: "Core aktif", Group: "Core & jaringan", Kind: "enum", Opts: []string{"clash", "sing-box", "xray", "v2fly", "hysteria"}},
	{Key: "xclash_option", Label: "Varian Clash", Group: "Core & jaringan", Kind: "enum", Opts: []string{"mihomo", "premium"}},
	{Key: "network_mode", Label: "Mode jaringan", Group: "Core & jaringan", Kind: "enum", Opts: []string{"redirect", "tproxy", "mixed", "enhance", "tun"}},
	{Key: "tproxy_port", Label: "Port tproxy", Group: "Core & jaringan", Kind: "num"},
	{Key: "redir_port", Label: "Port redirect", Group: "Core & jaringan", Kind: "num"},
	{Key: "ipv6", Label: "IPv6", Group: "Core & jaringan", Kind: "bool"},
	{Key: "box_user_group", Label: "User:group core", Group: "Core & jaringan", Kind: "ug"},

	{Key: "enable_network_service_control", Label: "Aktifkan Network Control", Group: "Network Control", Kind: "bool"},
	{Key: "use_module_on_wifi_disconnect", Label: "Proxy aktif saat WiFi terputus", Group: "Network Control", Kind: "bool"},
	{Key: "use_module_on_wifi", Label: "Proxy aktif saat WiFi tersambung", Group: "Network Control", Kind: "bool"},
	{Key: "use_ssid_matching", Label: "Cocokkan nama SSID", Group: "Network Control", Kind: "bool"},
	{Key: "use_wifi_list_mode", Label: "Mode daftar SSID", Group: "Network Control", Kind: "enum", Opts: []string{"blacklist", "whitelist"}},
	{Key: "wifi_ssids_list", Label: "Daftar SSID (satu per baris)", Group: "Network Control", Kind: "list"},

	{Key: "run_crontab", Label: "Aktifkan crontab", Group: "Langganan & jadwal", Kind: "bool"},
	{Key: "interva_update", Label: "Interval update (cron)", Group: "Langganan & jadwal", Kind: "cron"},
	{Key: "update_subscription", Label: "Update subscription", Group: "Langganan & jadwal", Kind: "bool"},
	{Key: "update_geo", Label: "Update geo", Group: "Langganan & jadwal", Kind: "bool"},
	{Key: "renew", Label: "Renew", Group: "Langganan & jadwal", Kind: "bool"},
	{Key: "subscription_url_clash", Label: "URL subscription Clash (satu per baris)", Group: "Langganan & jadwal", Kind: "list"},
	{Key: "subscription_url_singbox", Label: "URL subscription Sing-box", Group: "Langganan & jadwal", Kind: "text"},
	{Key: "name_clash_config", Label: "File config Clash aktif", Group: "Langganan & jadwal", Kind: "name"},
	{Key: "name_provide_clash_config", Label: "Nama file provider Clash (satu per baris)", Group: "Langganan & jadwal", Kind: "list", Sub: "name"},
	{Key: "name_sing_config", Label: "File config Sing-box", Group: "Langganan & jadwal", Kind: "name"},
	{Key: "name_xray_config", Label: "File config Xray", Group: "Langganan & jadwal", Kind: "name"},
	{Key: "name_hysteria_config", Label: "File config Hysteria", Group: "Langganan & jadwal", Kind: "name"},

	{Key: "cgroup_memcg", Label: "Batasi memori (cgroup)", Group: "Lanjutan", Kind: "bool"},
	{Key: "memcg_limit", Label: "Batas memori", Group: "Lanjutan", Kind: "mem"},
	{Key: "cgroup_cpuset", Label: "cpuset cgroup", Group: "Lanjutan", Kind: "bool"},
	{Key: "cgroup_blkio", Label: "blkio cgroup", Group: "Lanjutan", Kind: "bool"},
	{Key: "inotify_log_enabled", Label: "Log inotify", Group: "Lanjutan", Kind: "bool"},
}

var (
	reName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	reCron = regexp.MustCompile(`^[0-9*/,-]+( [0-9*/,-]+){4}$`)
	reUG   = regexp.MustCompile(`^[a-z_][a-z0-9_]*:[a-z_][a-z0-9_]*$`)
	reMem  = regexp.MustCompile(`^[0-9]{1,6}[KMGkmg]$`)
	rePkg  = regexp.MustCompile(`^[A-Za-z0-9_.]{1,200}$`)
)

func bfrCheck(d bfrField, v string) error {
	switch d.Kind {
	case "bool":
		if v != "true" && v != "false" {
			return fmt.Errorf("harus true atau false")
		}
	case "enum":
		for _, o := range d.Opts {
			if o == v {
				return nil
			}
		}
		return fmt.Errorf("pilihan tidak dikenal")
	case "num":
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("port harus 1-65535")
		}
	case "name":
		if !reName.MatchString(v) {
			return fmt.Errorf("nama berkas tidak valid")
		}
	case "cron":
		if !reCron.MatchString(v) {
			return fmt.Errorf("format cron tidak valid (5 kolom)")
		}
	case "ug":
		if !reUG.MatchString(v) {
			return fmt.Errorf("format user:group tidak valid")
		}
	case "mem":
		if !reMem.MatchString(v) {
			return fmt.Errorf("contoh benar: 100M")
		}
	default: // text, list item
		if bfrUnsafe(v) || len(v) > 500 {
			return fmt.Errorf("karakter terlarang ($ ` \\ \") atau terlalu panjang")
		}
	}
	return nil
}

func bfrFormat(d bfrField, old bfrVal, rm json.RawMessage) (string, error) {
	if (d.Kind == "list") != (old.kind == "list") {
		return "", fmt.Errorf("bentuk nilai di berkas tidak cocok")
	}
	if d.Kind == "list" {
		var items []string
		if err := json.Unmarshal(rm, &items); err != nil {
			return "", fmt.Errorf("harus berupa daftar")
		}
		q := make([]string, 0, len(items))
		for _, it := range items {
			it = strings.TrimSpace(it)
			if it == "" {
				continue
			}
			chk := bfrField{Kind: "text"}
			if d.Sub == "name" {
				chk = bfrField{Kind: "name"}
			}
			if err := bfrCheck(chk, it); err != nil {
				return "", fmt.Errorf("%q: %v", it, err)
			}
			q = append(q, `"`+it+`"`)
		}
		return "(" + strings.Join(q, " ") + ")", nil
	}
	var v string
	if err := json.Unmarshal(rm, &v); err != nil {
		return "", fmt.Errorf("harus berupa teks")
	}
	v = strings.TrimSpace(v)
	if err := bfrCheck(d, v); err != nil {
		return "", err
	}
	if !old.quoted && bfrBare.MatchString(v) {
		return v, nil
	}
	return `"` + v + `"`, nil
}

// bfrCheckShell: sintaks baru harus lolos `sh -n`. Bila berkas asli pun gagal
// (beda shell), pemeriksaan ini dilewati; validasi regex per kunci tetap berlaku.
func bfrCheckShell(newData []byte) error {
	dir := bfrP("webui-tmp")
	os.MkdirAll(dir, 0700)
	nf := filepath.Join(dir, "new.sh")
	if err := os.WriteFile(nf, newData, 0600); err != nil {
		return err
	}
	defer os.Remove(nf)
	o, err := bfrExec(10*time.Second, bfrSh, "-n", nf)
	if err == nil {
		return nil
	}
	if old, e := os.ReadFile(bfrP("settings.ini")); e == nil {
		of := filepath.Join(dir, "old.sh")
		os.WriteFile(of, old, 0600)
		_, e2 := bfrExec(10*time.Second, bfrSh, "-n", of)
		os.Remove(of)
		if e2 != nil {
			return nil
		}
	}
	return fmt.Errorf("sintaks tidak valid: %s", strings.TrimSpace(o))
}

// bfrSaveSettings mengubah baris literal yang diizinkan, mencadangkan, memvalidasi, lalu
// menulis di tempat (menjaga pemilik/izin berkas). Mengembalikan path cadangan.
func bfrSaveSettings(changes map[string]json.RawMessage) (string, error) {
	bfrMu.Lock()
	defer bfrMu.Unlock()
	defs := map[string]bfrField{}
	for _, d := range bfrDefs {
		defs[d.Key] = d
	}
	for k := range changes {
		if _, ok := defs[k]; !ok {
			return "", fmt.Errorf("kunci %s tidak boleh diubah", k)
		}
	}
	raw, err := os.ReadFile(bfrP("settings.ini"))
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(raw), "\n")
	done := map[string]bool{}
	for i, ln := range lines {
		m := bfrKV.FindStringSubmatch(strings.TrimRight(ln, "\r"))
		if m == nil || done[m[1]] {
			continue
		}
		rm, want := changes[m[1]]
		if !want {
			continue
		}
		old := bfrParseVal(m[2])
		if !old.ok {
			return "", fmt.Errorf("baris %s berisi logika/komentar, tidak diedit otomatis", m[1])
		}
		nv, err := bfrFormat(defs[m[1]], old, rm)
		if err != nil {
			return "", fmt.Errorf("%s: %v", m[1], err)
		}
		lines[i] = m[1] + "=" + nv
		done[m[1]] = true
	}
	for k := range changes {
		if !done[k] {
			return "", fmt.Errorf("kunci %s tidak ditemukan di settings.ini", k)
		}
	}
	out := []byte(strings.Join(lines, "\n"))
	if err := bfrCheckShell(out); err != nil {
		return "", err
	}
	bk, err := bfrBackup(bfrP("settings.ini"), "settings.ini")
	if err != nil {
		return "", fmt.Errorf("gagal membuat cadangan: %v", err)
	}
	if err := os.WriteFile(bfrP("settings.ini"), out, 0644); err != nil {
		return "", err
	}
	return bk, nil
}

// bfrRestartSafe: restart; bila inti tidak hidup setelah 6 dtk, kembalikan cadangan.
func bfrRestartSafe(backup string) (ok bool, rolled bool, out string) {
	out, _ = bfrExec(90*time.Second, bfrSh, bfrP("scripts", "box.service"), "restart")
	time.Sleep(6 * time.Second)
	if r, _ := bfrRunning(); r {
		return true, false, out
	}
	if backup != "" {
		if b, err := os.ReadFile(backup); err == nil {
			bfrMu.Lock()
			os.WriteFile(bfrP("settings.ini"), b, 0644)
			bfrMu.Unlock()
			o2, _ := bfrExec(90*time.Second, bfrSh, bfrP("scripts", "box.service"), "restart")
			out += "\n--- ROLLBACK: settings.ini dikembalikan ---\n" + o2
			return false, true, out
		}
	}
	return false, false, out
}

// ---------- handler: Home / kontrol / modul ----------

func bfrModule() map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(bfrModProp)
	if err != nil {
		return m
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if i := strings.Index(ln, "="); i > 0 {
			m[strings.TrimSpace(ln[:i])] = strings.TrimSpace(ln[i+1:])
		}
	}
	return m
}

func bfrHome(w http.ResponseWriter, r *http.Request) {
	s := bfrReadSettings()
	run, pid := bfrRunning()
	lg, _ := bfrTail(bfrP("run", "runs.log"), 8)
	bfrJSON(w, map[string]interface{}{
		"running": run, "pid": pid,
		"core": s["bin_name"].str, "mode": s["network_mode"].str,
		"tproxy": s["tproxy_port"].str, "redir": s["redir_port"].str,
		"ipv6": s["ipv6"].str, "config": s["name_clash_config"].str,
		"log": lg, "module": bfrModule(),
	})
}

// bfrWaitState: tunggu sampai status layanan == want (dua pembacaan berturut-turut), maks timeout.
func bfrWaitState(want bool, timeout time.Duration) (bool, string) {
	end := time.Now().Add(timeout)
	hit := 0
	for {
		time.Sleep(700 * time.Millisecond)
		run, pid := bfrRunning()
		if run == want {
			hit++
			if hit >= 2 {
				return run, pid
			}
		} else {
			hit = 0
		}
		if time.Now().After(end) {
			return run, pid
		}
	}
}

func bfrCtl(w http.ResponseWriter, r *http.Request) {
	a := r.URL.Query().Get("a")
	if a != "start" && a != "stop" && a != "restart" {
		bfrFail(w, 400, "aksi tidak dikenal")
		return
	}
	out, err := bfrExec(90*time.Second, bfrSh, bfrP("scripts", "box.service"), a)
	if a == "stop" {
		// v2.8: box.service stop tidak membersihkan iptables (TPROXY 9898, DNS 7874)
		o2, _ := bfrExec(30*time.Second, bfrSh, bfrP("scripts", "box.iptables"), "disable")
		out += o2
	}
	want := a != "stop"
	run, pid := bfrWaitState(want, 15*time.Second)
	res := map[string]interface{}{"output": out, "running": run, "pid": pid, "settled": run == want}
	if err != nil {
		res["error_exec"] = err.Error()
	}
	bfrJSON(w, res)
}

func bfrTool(w http.ResponseWriter, r *http.Request) {
	a := r.URL.Query().Get("a")
	if a != "subs" && a != "upkernel" && a != "upxui" {
		bfrFail(w, 400, "aksi tidak dikenal")
		return
	}
	out, err := bfrExec(5*time.Minute, bfrSh, bfrP("scripts", "box.tool"), a)
	res := map[string]interface{}{"output": out}
	if err != nil {
		res["error_exec"] = err.Error()
	}
	bfrJSON(w, res)
}

// ---------- handler: Logs ----------

func bfrLogNames() []string {
	m, _ := filepath.Glob(bfrP("run", "*.log"))
	var n []string
	for _, p := range m {
		n = append(n, filepath.Base(p))
	}
	sort.Strings(n)
	return n
}

func bfrLogs(w http.ResponseWriter, r *http.Request) {
	type row struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	rows := []row{}
	for _, n := range bfrLogNames() {
		var sz int64
		if st, err := os.Stat(bfrP("run", n)); err == nil {
			sz = st.Size()
		}
		rows = append(rows, row{n, sz})
	}
	bfrJSON(w, map[string]interface{}{"logs": rows})
}

func bfrLog(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	ok := false
	for _, n := range bfrLogNames() {
		if n == name {
			ok = true
		}
	}
	if !ok {
		bfrFail(w, 404, "log tidak ditemukan")
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n < 50 {
		n = 500
	}
	if n > 2000 {
		n = 2000
	}
	txt, err := bfrTail(bfrP("run", name), n)
	if err != nil {
		bfrFail(w, 500, err.Error())
		return
	}
	bfrJSON(w, map[string]interface{}{"name": name, "text": txt})
}

// ---------- handler: Settings ----------

func bfrSettingsGet(w http.ResponseWriter, r *http.Request) {
	cur := bfrReadSettings()
	fields := []bfrField{}
	locked := []string{}
	for _, d := range bfrDefs {
		v, found := cur[d.Key]
		d.List = []string{}
		d.Present = found
		if found && v.ok && (d.Kind == "list") == (v.kind == "list") {
			d.Editable = true
			d.Value = v.str
			if v.kind == "list" {
				d.List = v.list
			}
		} else if found {
			locked = append(locked, d.Key)
		}
		if found {
			fields = append(fields, d)
		}
	}
	bfrJSON(w, map[string]interface{}{"fields": fields, "locked": locked, "module": bfrModule()})
}

func bfrSettingsPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Changes map[string]json.RawMessage `json:"changes"`
		Restart bool                       `json:"restart"`
	}
	if err := bfrBody(r, &req); err != nil || len(req.Changes) == 0 {
		bfrFail(w, 400, "tidak ada perubahan")
		return
	}
	bk, err := bfrSaveSettings(req.Changes)
	if err != nil {
		bfrFail(w, 400, err.Error())
		return
	}
	res := map[string]interface{}{"saved": true, "backup": filepath.Base(bk)}
	if req.Restart {
		ok, rolled, out := bfrRestartSafe(bk)
		res["restarted"] = true
		res["running"] = ok
		res["rolled_back"] = rolled
		res["output"] = out
	}
	bfrJSON(w, res)
}

// ---------- handler: Config ----------

type bfrCfg struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Group  string `json:"group"`
	Size   int64  `json:"size"`
	Active bool   `json:"active"`
	Main   bool   `json:"main"` // config Clash utama: bisa dijadikan aktif
}

func bfrCfgList() []bfrCfg {
	s := bfrReadSettings()
	active := s["name_clash_config"].str
	var out []bfrCfg
	add := func(group, dir string, main bool, exts ...string) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			ok := false
			for _, x := range exts {
				if strings.EqualFold(filepath.Ext(e.Name()), x) {
					ok = true
				}
			}
			if !ok {
				continue
			}
			p := filepath.Join(dir, e.Name())
			st, err := os.Stat(p)
			if err != nil || st.IsDir() {
				continue
			}
			out = append(out, bfrCfg{Path: p, Name: e.Name(), Group: group, Size: st.Size(),
				Main: main, Active: main && e.Name() == active})
		}
	}
	add("Clash", bfrP("clash"), true, ".yaml", ".yml")
	add("Clash / subscription", bfrP("clash", "subscription"), false, ".yaml", ".yml")
	add("Sing-box", bfrP("sing-box"), false, ".json")
	add("Xray", bfrP("xray"), false, ".json")
	add("V2fly", bfrP("v2fly"), false, ".json")
	add("Hysteria", bfrP("hysteria"), false, ".yaml", ".yml", ".json")
	for _, n := range []string{"settings.ini", "ap.list.cfg", "gid.list.cfg"} {
		p := bfrP(n)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, bfrCfg{Path: p, Name: n, Group: "Pengaturan modul", Size: st.Size()})
		}
	}
	return out
}

func bfrCfgFind(path string) (bfrCfg, bool) {
	for _, c := range bfrCfgList() {
		if c.Path == path {
			return c, true
		}
	}
	return bfrCfg{}, false
}

func bfrConfigList(w http.ResponseWriter, r *http.Request) {
	l := bfrCfgList()
	if l == nil {
		l = []bfrCfg{}
	}
	bfrJSON(w, map[string]interface{}{"files": l})
}

func bfrConfigGet(w http.ResponseWriter, r *http.Request) {
	c, ok := bfrCfgFind(r.URL.Query().Get("path"))
	if !ok {
		bfrFail(w, 404, "berkas tidak ada di daftar")
		return
	}
	if c.Size > 2<<20 {
		bfrFail(w, 413, "berkas lebih dari 2 MB")
		return
	}
	b, err := os.ReadFile(c.Path)
	if err != nil {
		bfrFail(w, 500, err.Error())
		return
	}
	bfrJSON(w, map[string]interface{}{"path": c.Path, "name": c.Name, "content": string(b)})
}

func bfrConfigSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := bfrBody(r, &req); err != nil {
		bfrFail(w, 400, "permintaan tidak valid")
		return
	}
	c, ok := bfrCfgFind(req.Path)
	if !ok {
		bfrFail(w, 404, "berkas tidak ada di daftar")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		bfrFail(w, 400, "isi kosong ditolak")
		return
	}
	if strings.EqualFold(filepath.Ext(c.Name), ".json") && !json.Valid([]byte(req.Content)) {
		bfrFail(w, 400, "JSON tidak valid")
		return
	}
	bfrMu.Lock()
	defer bfrMu.Unlock()
	if c.Name == "settings.ini" && c.Group == "Pengaturan modul" {
		if err := bfrCheckShell([]byte(req.Content)); err != nil {
			bfrFail(w, 400, err.Error())
			return
		}
	}
	bk, err := bfrBackup(c.Path, c.Name)
	if err != nil {
		bfrFail(w, 500, "gagal membuat cadangan: "+err.Error())
		return
	}
	if err := os.WriteFile(c.Path, []byte(req.Content), 0644); err != nil {
		bfrFail(w, 500, err.Error())
		return
	}
	bfrJSON(w, map[string]interface{}{"saved": true, "backup": filepath.Base(bk)})
}

func bfrConfigActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Restart bool   `json:"restart"`
	}
	if err := bfrBody(r, &req); err != nil {
		bfrFail(w, 400, "permintaan tidak valid")
		return
	}
	found := false
	for _, c := range bfrCfgList() {
		if c.Main && c.Name == req.Name {
			found = true
		}
	}
	if !found {
		bfrFail(w, 404, "bukan config Clash utama yang ada")
		return
	}
	nv, _ := json.Marshal(req.Name)
	bk, err := bfrSaveSettings(map[string]json.RawMessage{"name_clash_config": nv})
	if err != nil {
		bfrFail(w, 400, err.Error())
		return
	}
	res := map[string]interface{}{"saved": true, "backup": filepath.Base(bk)}
	if req.Restart {
		ok, rolled, out := bfrRestartSafe(bk)
		res["restarted"], res["running"], res["rolled_back"], res["output"] = true, ok, rolled, out
	}
	bfrJSON(w, res)
}

// ---------- handler: Apps ----------

func bfrReadPkgCfg() (mode string, pkgs []string, comments []string) {
	mode = "blacklist"
	b, _ := os.ReadFile(bfrP("package.list.cfg"))
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(strings.TrimRight(ln, "\r"))
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "#") {
			comments = append(comments, ln)
			continue
		}
		if strings.HasPrefix(ln, "mode:") {
			if f := strings.Fields(strings.TrimPrefix(ln, "mode:")); len(f) > 0 {
				mode = f[0]
			}
			continue
		}
		if tok := strings.Fields(ln)[0]; strings.Contains(tok, ".") {
			pkgs = append(pkgs, tok)
		}
	}
	return
}

func bfrPM(args ...string) ([]string, error) {
	out, err := bfrExec(30*time.Second, "pm", append([]string{"list", "packages"}, args...)...)
	var l []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "package:") {
			l = append(l, strings.TrimPrefix(ln, "package:"))
		}
	}
	if len(l) == 0 && err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(out))
	}
	return l, nil
}

func bfrApps(w http.ResponseWriter, r *http.Request) {
	mode, sel, _ := bfrReadPkgCfg()
	all, err := bfrPM()
	if err != nil {
		bfrFail(w, 500, "pm gagal: "+err.Error())
		return
	}
	sys, _ := bfrPM("-s")
	sysSet := map[string]bool{}
	for _, p := range sys {
		sysSet[p] = true
	}
	selSet := map[string]bool{}
	for _, p := range sel {
		selSet[p] = true
	}
	type row struct {
		Name      string `json:"name"`
		System    bool   `json:"system"`
		Selected  bool   `json:"selected"`
		Installed bool   `json:"installed"`
	}
	seen := map[string]bool{}
	rows := []row{}
	for _, p := range all {
		seen[p] = true
		rows = append(rows, row{p, sysSet[p], selSet[p], true})
	}
	for _, p := range sel { // pilihan yang paketnya tidak terpasang tetap dipertahankan
		if !seen[p] {
			rows = append(rows, row{p, false, true, false})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	bfrJSON(w, map[string]interface{}{"mode": mode, "apps": rows})
}

func bfrAppsSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode     string   `json:"mode"`
		Packages []string `json:"packages"`
		Restart  bool     `json:"restart"`
	}
	if err := bfrBody(r, &req); err != nil {
		bfrFail(w, 400, "permintaan tidak valid")
		return
	}
	if req.Mode != "blacklist" && req.Mode != "whitelist" {
		bfrFail(w, 400, "mode harus blacklist atau whitelist")
		return
	}
	seen := map[string]bool{}
	var pk []string
	for _, p := range req.Packages {
		p = strings.TrimSpace(p)
		if !rePkg.MatchString(p) {
			bfrFail(w, 400, fmt.Sprintf("nama paket tidak valid: %q", p))
			return
		}
		if !seen[p] {
			seen[p] = true
			pk = append(pk, p)
		}
	}
	sort.Strings(pk)
	bfrMu.Lock()
	_, _, comments := bfrReadPkgCfg()
	var sb strings.Builder
	for _, c := range comments {
		sb.WriteString(c + "\n")
	}
	sb.WriteString("mode:" + req.Mode + "\n")
	for _, p := range pk {
		sb.WriteString(p + "\n")
	}
	path := bfrP("package.list.cfg")
	var bk string
	if _, err := os.Stat(path); err == nil {
		var e error
		if bk, e = bfrBackup(path, "package.list.cfg"); e != nil {
			bfrMu.Unlock()
			bfrFail(w, 500, "gagal membuat cadangan: "+e.Error())
			return
		}
	}
	err := os.WriteFile(path, []byte(sb.String()), 0644)
	bfrMu.Unlock()
	if err != nil {
		bfrFail(w, 500, err.Error())
		return
	}
	res := map[string]interface{}{"saved": true, "count": len(pk)}
	if bk != "" {
		res["backup"] = filepath.Base(bk)
	}
	if req.Restart {
		out, _ := bfrExec(90*time.Second, bfrSh, bfrP("scripts", "box.service"), "restart")
		time.Sleep(4 * time.Second)
		run, _ := bfrRunning()
		res["restarted"], res["running"], res["output"] = true, run, out
	}
	bfrJSON(w, res)
}

// ---------- pendaftaran rute ----------

// registerBFR dipanggil sekali dari main.go. auth membungkus handler dengan
// pemeriksaan login yang sama seperti rute /api/status.
func registerBFR(mux *http.ServeMux, auth func(http.HandlerFunc) http.HandlerFunc) {
	get := func(p string, f http.HandlerFunc) { mux.HandleFunc(p, auth(f)) }
	post := func(p string, f http.HandlerFunc) { mux.HandleFunc(p, auth(bfrPost(f))) }

	get("/bfr", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(bytes.TrimSpace(bfrPage))
	})
	get("/api/bfr/home", bfrHome)
	post("/api/bfr/ctl", bfrCtl)
	post("/api/bfr/tool", bfrTool)
	get("/api/bfr/logs", bfrLogs)
	get("/api/bfr/log", bfrLog)
	// satu rute untuk GET dan POST (ServeMux panik bila path didaftarkan dua kali)
	mux.HandleFunc("/api/bfr/settings", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			bfrSettingsGet(w, r)
			return
		}
		bfrPost(bfrSettingsPost)(w, r)
	}))
	get("/api/bfr/config/list", bfrConfigList)
	get("/api/bfr/config/get", bfrConfigGet)
	post("/api/bfr/config/save", bfrConfigSave)
	post("/api/bfr/config/active", bfrConfigActive)
	get("/api/bfr/apps", bfrApps)
	post("/api/bfr/apps/save", bfrAppsSave)
}
