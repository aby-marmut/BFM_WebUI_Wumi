# Build, deploy, modul, rollback

Semua di Debian (`ssh alfa@<IP-DEBIAN>`), folder repo (saat ini `~/wumi`). `N=<IP-ADB>:5555`.

## Build
`sh build.sh` (butuh `~/metacubexd-dist.tgz`; mengisi `web/zash/`, lalu `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o wumi .`). Sandbox: `go build` biasa lalu `-addr 127.0.0.1:18080 -mirror ''`, `go test -vet=off` (ada `log.Fatal` berformat di `main.go`).
AndroModem (di luar repo): `cd ~/andromodem && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o andromodem-arm64 cmd/andromodem/main.go`.

## Deploy biner
```sh
N=<IP-ADB>:5555
cd ~/wumi
adb -s $N shell 'su -c "cp /data/adb/wumi/wumi /data/adb/wumi/wumi.bak-NAMA"'
adb -s $N push wumi /data/local/tmp/wumi.new
adb -s $N shell 'su -c "cp /data/local/tmp/wumi.new /data/adb/wumi/wumi.new && chmod 755 /data/adb/wumi/wumi.new && mv -f /data/adb/wumi/wumi.new /data/adb/wumi/wumi && reboot"'
```
Rollback: salin `wumi.bak-NAMA` ke `wumi.new`, `mv -f` ke `wumi`, reboot.

## 7zz (untuk 7z, rar, zip bersandi)
```sh
cd ~ && curl -fsSL -o 7z.tar.xz https://github.com/ip7z/7zip/releases/download/24.09/7z2409-linux-arm64.tar.xz && tar xf 7z.tar.xz 7zzs && adb -s $N push 7zzs /data/local/tmp/7zz && adb -s $N shell 'su -c "cp /data/local/tmp/7zz /data/adb/wumi/7zz && chmod 755 /data/adb/wumi/7zz"'
```
(`7zzs` = statis, aman di Android.) Cek: `adb -s $N shell 'su -c "/data/adb/wumi/7zz | head -2"'`.

## Modul Magisk
`~/build-wumi-module.sh` (salinan di `tools/build-wumi-module.sh`) -> `~/wumi-module-v2.0.zip` (saat mengganti versi, ubah `v2.0` di skrip dan nama zip). v2.0 lulus uji reboot. Bangun ulang modul setiap biner berubah, karena zip membawa biner di dalamnya. Isi: `wumi`, `andromodem`, `adb/` (bundel mandiri, patchelf), `service.sh` (iptables DROP `-i rmnet+` untuk 5555/49153/8080/8081; jalankan wumi `-addr :8080`, AndroModem, server ADB lokal), `customize.sh`, `uninstall.sh` (hanya mematikan proses dan membersihkan iptables).
Pasang: `adb -s $N push ZIP /sdcard/Download/` lalu `adb -s $N shell 'su -c "magisk --install-module /sdcard/Download/ZIP"'`, reboot. Rollback modul: pasang zip versi sebelumnya, atau nonaktifkan dengan `su -c "touch /data/adb/modules/wumi/disable; reboot"`.
Blokir 3001: `/data/adb/service.d/block3001.sh` (`iptables -C` lalu `-I`; argumen `now` melewati `sleep 30`).

## Ekspor sumber lama (zip) - tidak lagi perlu
Sejak repo dipakai, lampirkan repo, bukan zip.

## Cek cepat (satu blok)
```sh
N=<IP-ADB>:5555
adb -s $N shell 'su -c "ps -A | grep -E \"wumi|andromodem\"; ss -ltn | grep -E \":(8080|8081|49153|9090) \"; iptables -S INPUT | grep -E \"5555|8080|8081|49153\"; ls /data/adb/modules; grep ^version /data/adb/modules/wumi/module.prop"'
```
Sehat: 49153 dan 9090 di `127.0.0.1`, 8080/8081 di `*`; 8080/8081/49153/5555 masing-masing satu `DROP -i rmnet+`; tanpa `ACCEPT 127.0.0.1` dan tanpa aturan 3001; modul tanpa `UNIVERSALBFRWEBUI`; `version=v1.1`. Uji dari ponsel: `http://<IP-Note8>:8080`.

## Reset sandi login
`printf %s SANDI | /data/adb/wumi/wumi -set-password` (di Note 8, lewat adb shell su).

## Cadangan yang boleh dihapus setelah stabil
Note 8: `/data/adb/wumi/wumi.bak-*`, `/data/adb/box/clash/Alfa-net.yaml.bak-9090`, `/data/adb/andromodem/andromodem.bak-lan`, `/data/local/tmp/*`. Debian: `~/bak-fileroot*`, `~/bak-archive2`, `*.bak*`. Simpan lebih lama: `~/backup-bfrwebui.tgz`.
