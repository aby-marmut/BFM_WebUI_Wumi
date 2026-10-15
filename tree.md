# Struktur Wumi

## Repo (berkas yang di-track git)
```
.gitignore
CHANGELOG.md
HANDOFF.md
NOTICE
README.md
admin.go
archive.go
bfr.go
bfr_page.html
bfrprov.go
build.sh
docs/api.md
docs/deploy.md
docs/files.md
docs/network.md
docs/rename-wumi.md
files2.go
files3.go
go.mod
main.go
mirror-only.sh
speedtest.go
terminal.go
tools/adb-bundle.sh
tools/build-wumi-module.sh
vnstat.go
web/index.html
web/login.html
web/term.html
web/term/addon-fit.js
web/term/xterm.css
web/term/xterm.js
wumi.go
```

## Modul Magisk wumi (isi zip dari tools/build-wumi-module.sh)
```
customize.sh
andromodem
module.prop
wumi
adb/
adb/bin/
adb/bin/adb
adb/lib/
uninstall.sh
service.sh
adb/lib/  (56 pustaka bersama)
```

## Setelah terpasang di Note 8
- Modul: `/data/adb/modules/wumi/`
- Data: `/data/adb/wumi/` (`wumi`, `auth.json`, `speedtest.json`, `vnstat.json`, `7zz`, `wumi.log`)

## Pasang dan copot
- Pasang: `customize.sh` (dibuat oleh `tools/build-wumi-module.sh`); langkah di `docs/deploy.md`.
- Copot: `uninstall.sh` mematikan proses wumi, andromodem, dan adb, lalu menghapus aturan iptables 8080, 8081, 49153. Tidak memulihkan `andromodem.sh` lama dan tidak menyentuh `/data/adb/wumi`.
