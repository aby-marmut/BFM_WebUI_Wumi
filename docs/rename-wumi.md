# Rencana ganti nama note8ui -> wumi

Status: SELESAI (14 Okt 2026). Bisa dilakukan, tetapi bertahap karena nama dipakai saat boot Magisk, aturan iptables, dan jalur data (sandi login). Setiap tahap = satu commit, satu uji, satu rollback. Jangan gabung tahap.

## Tahap A: hanya di repo (aman, tanpa efek ke Note 8)
- `go.mod`: `module wumi`; nama biner di `build.sh` (`-o wumi`); komentar dan judul di kode.
- Nama berkas Go yang bernomor/ambigu (opsional): `files2.go` -> `files_edit.go`, `files3.go` -> `files_root.go`. Perubahan nama berkas Go tidak mengubah perilaku.
- Dokumen: `HANDOFF.md`, `docs/*`, `tools/*` (nama skrip: `build-wumi-module.sh`).
- Uji: `go build`, jalankan lokal, buka UI. Rollback: `git revert`.

## Tahap B: identitas internal aplikasi (satu rilis biner, tanpa ubah jalur di perangkat)
- Header CSRF `X-Requested-With: note8ui` -> `wumi`: **UI (`index.html`, `login.html`, `term.html`) dan server (`main.go` `post`, `admin.go`) harus diubah bersamaan**, kalau tidak semua POST gagal 403.
- Cookie sesi `n8s` -> `wsid` (`main.go`, `admin.go`, `terminal.go`): semua orang keluar sekali; tidak masalah.
- Kunci localStorage/sessionStorage (`n8tab`, `navoff`): mengganti = preferensi UI kembali ke awal sekali.
- Uji: login, simpan sandi, satu POST dari tiap tab (Files hapus/ubah nama, Control Fix TTL, BFR, Admin).

## Tahap C: perangkat (modul Magisk baru `wumi`) - butuh persetujuan pemilik
Titik sentuh: ID modul `note8ui` -> `wumi`; jalur `/data/adb/note8ui` -> `/data/adb/wumi` (berisi `auth.json`, `7zz`, log, cadangan biner); nama biner `note8ui` -> `wumi`; `service.sh` (`ps | grep [n]ote8ui$`, `pkill -x note8ui`, `-addr :8080`); `customize.sh`/`uninstall.sh`; aturan iptables 8080/8081/49153/5555 **tidak berubah** (port sama); `protectedPath` di `files3.go` (`/data/adb/note8ui` -> jalur baru, dan **tetap lindungi jalur lama** selama masih ada); perintah "Cek cepat".
Cara aman:
1. Bangun modul `wumi` v2.0 yang di `customize.sh` **menyalin** `/data/adb/note8ui/auth.json` dan `7zz` ke `/data/adb/wumi/` (jangan memindah; jalur lama dibiarkan utuh sebagai cadangan).
2. Modul lama `note8ui` ditandai `remove` pada pemasangan yang sama, satu reboot.
3. Pastikan hanya satu pemilik port 8080 (modul lama dan baru tidak boleh jalan bersamaan).
4. Uji reboot: login memakai sandi lama, Status, Files, Mirror, iptables (Cek cepat), 7zz.
5. Jalur lama baru dihapus setelah beberapa hari stabil.
Rollback: pasang zip modul `note8ui` v1.1 (jalur lama masih utuh), hapus modul `wumi` (`touch /data/adb/modules/wumi/remove`), reboot.

## Di luar kode
Nama repo `wumi`, nama berkas handoff, nama zip modul, `NOTICE`/README. `~/note8ui` di Debian bisa dipindah ke `~/wumi` setelah Tahap A (perbarui `build-*-module.sh` yang memakai `~/note8ui`).
