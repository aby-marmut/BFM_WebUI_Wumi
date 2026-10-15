# API dan keamanan

Semua endpoint perlu sesi (cookie `n8s`), kecuali `/login` dan `POST /api/login`. Semua POST wajib header `X-Requested-With: wumi` (BFR: `bfr`).

## Umum
- `POST /api/login`, `POST /api/logout`, `GET /login`
- `POST /api/admin/password` (`admin.go`) `{"cur","new"}`: sandi saat ini salah = **403** (bukan 401); batas 5 gagal/10 menit/IP; sandi baru 8-128 karakter, tanpa spasi di tepi, harus beda; `auth.json` ditulis atomik 0600; sesi lain dikeluarkan.
- `GET /api/status`: CPU, Clash CPU, suhu/baterai, RAM, trafik WAN, Box aktif, core, mode (sampler 1 dtk)
- `GET /api/overview`: ringkasan `/connections` Clash
- `GET /api/netcheck[?ip=1&refresh=1]`: latensi lewat port proxy Clash ke gstatic generate_204; IP publik lewat `api.ip.sb` lalu `ipwho.is` (cache 5 menit)
- `GET /api/adb` (`wumi.go`): perangkat dari server adb lokal `127.0.0.1:5037`, dipakai penanda "Wumi : ON/OFF"
- `GET /api/control/state`, `POST /api/control/ttl`, `POST /api/control/power` (reboot|recovery|shutdown)
- `GET /api/sms` (30 SMS terbaru)
- `/ui/` MetaCubeXD (config.js dinamis, `sw.js` dinetralkan, fallback SPA); path lain di root diteruskan ke Clash (WebSocket didukung)

## BFR (`bfr.go`, `bfrprov.go`)
`GET /bfr`, `/api/bfr/{home,ctl,tool,logs,log,settings,config/*,apps/*}`; `/api/bfr/prov/{list,get,text}` dan `POST .../{save,upload,delete}` (hanya `.yaml/.yml`, `proxy_provider`, `rule_provider`, batas teks 32 MB, unggahan 64 MB); `GET /api/bfr/cfg/backup` (.tar.gz), `POST /api/bfr/cfg/restore` (maks 5000 entri, 256 MB). Pengaman: nama divalidasi, tanpa berawalan titik, hanya berkas biasa. Settings: daftar putih kunci, cadangan di `/data/adb/box/webui-backups`, `sh -n`, rollback otomatis bila inti mati setelah restart. Bergantung pada `bfrP, bfrJSON, bfrFail, bfrBody, bfrBackup, bfrMu, bfrReadSettings` (bfr.go) dan `validName` (main.go).

## Files
Lihat `files.md`.

## Terminal web (`terminal.go`) - PENDING uji pemilik
Shell root `/system/bin/sh` lewat PTY + WebSocket tulis-tangan: `GET /term`, `/term/{xterm.js,xterm.css,addon-fit.js}`, `GET /api/term/ws`. Satu sesi aktif (sambungan baru menggantikan). Pengaman: `validSession`, cek Origin sama-host, `lanOnly`. Uji: (1) tanpa login `/term` dialihkan ke /login; (2) setelah login `id` = `uid=0`; (3) mengetik dari ponsel nyaman (Ctrl/Tab); (4) tab kedua menggantikan sesi pertama; (5) dari LTE tidak terjangkau; (6) rotasi/ubah ukuran tidak merusak tampilan. Ubah kode terminal hanya dengan persetujuan pemilik.

## Keamanan yang ada
Sandi PBKDF2-SHA256 100k di `/data/adb/wumi/auth.json` (user `admin`); cookie `n8s` HttpOnly SameSite=Strict 12 jam (memori); batas 5 gagal/10 menit/IP; middleware menerima loopback, jaringan privat (10/8, 172.16/12, 192.168/16), link-local, dan CGNAT 100.64/10; lapisan pertama dari sisi LTE adalah iptables DROP di `service.sh`; header keamanan (XFO SAMEORIGIN, nosniff); unduhan selalu `attachment` + octet-stream; jalur berkas lewat `safePath` (EvalSymlinks). Go memakai sertifikat Android lewat `SSL_CERT_DIR` di `main()`. Atur sandi: `printf %s SANDI | /data/adb/wumi/wumi -set-password` (stdin, min 8 karakter).

## Front-end
Tab: Status, BFR (iframe `/bfr`), Files, Control, Mirror. Menu samping KIRI 64px, bisa disembunyikan (`#nh`, localStorage `navoff`), menutup otomatis saat ketuk di luar (termasuk dalam iframe BFR/Mirror). Tab terakhir di `sessionStorage n8tab`. Helper JS: `api()` (menyertakan header, redirect /login pada 401), `J()` (POST JSON), `op(judul,html,fn,nt)/cl()/cdr()` (dialog; `nt=1` tanpa timer), `arm()` (dua ketukan), `toast()`, `esc()`. Patch UI ditambahkan sebagai blok bertanda `id` (`ui5`, `ui6`, `uiadm`, `fileroot`, `fxarc-css`, `fxarc`, `arc2-css`, `arc2`) sebelum `</body></html>`.
