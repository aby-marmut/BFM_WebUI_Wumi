# Wumi

WebUI untuk Android ber-root yang dipakai sebagai modem LTE, hotspot, atau gateway. Ditulis dengan Go dan berjalan sebagai satu biner di perangkat, tanpa PHP dan tanpa Termux. Antarmukanya dibuat untuk layar ponsel dan bekerja bersama [Box for Root](https://github.com/taamarin/box_for_magisk).

Wumi proyek pribadi yang dibuat untuk kebutuhan sendiri, lalu dirapikan agar bisa dipakai komunitas yang sudah paham alur Android sebagai router.

## Fitur

- **Status**: topologi jaringan, IP, trafik, latensi, speedtest, dan vnStat.
- **BFR**: pengaturan Box for Root, log, pembaruan langganan, dan dashboard MetaCubeXD.
- **Files**: pengelola berkas dengan mode No Root dan Root, pengaturan izin, salin/pindah, penyuntingan teks, dan arsip.
- **Control**: penerapan TTL (termasuk saat boot), ganti sandi admin, reboot, recovery, dan shutdown.
- **Mirror**: cermin layar lewat AndroModem.
- **Terminal web** dengan akses root.

## Kebutuhan

- Android arm64 dengan root Magisk
- Box for Root terpasang di `/data/adb/box` (pemasangan modul ditolak jika tidak ada)
- adbd TCP aktif, diperlukan oleh fitur Mirror

## Pemasangan

Pasang zip modul lewat Magisk, lalu reboot. Sandi admin belum ada pada pemasangan pertama; atur dari shell root di perangkat:

```sh
printf %s 'SANDI' | /data/adb/wumi/wumi -set-password
```

Setelah itu buka `http://<IP-perangkat>:8080` dari perangkat lain di jaringan yang sama dan masuk sebagai `admin`.

## Keamanan

Sandi disimpan dengan PBKDF2-SHA256. Login dibatasi lima percobaan gagal per sepuluh menit untuk tiap IP, sesi memakai cookie HttpOnly, dan permintaan hanya diterima dari loopback serta jaringan privat.

Modul memasang aturan iptables yang menutup port 5555, 8080, 8081, dan 49153 dari antarmuka seluler. Perhatikan bahwa adbd TCP (port 5555) dibuka tanpa autentikasi oleh modul penyedianya. Satu-satunya penutup dari sisi operator adalah aturan tersebut, jadi jangan menonaktifkan modul Wumi tanpa pengganti.

## Perangkat teruji

Samsung Galaxy Note 8 (SM-N950F), LineageOS 20 (Android 13), Magisk, arm64. Perangkat lain belum diuji, sehingga kompatibilitasnya belum dijamin. Laporan uji dari perangkat lain sangat membantu.

## Build

Kode hanya memakai pustaka standar Go. Build untuk arm64:

```sh
sh build.sh
```

Skrip ini membutuhkan arsip dist MetaCubeXD (`~/metacubexd-dist.tgz`) untuk bagian dashboard. Langkah pembuatan modul dan deploy ada di `docs/deploy.md`.

## Dokumentasi

Lihat folder `docs/` (api, deploy, files, network) dan `CHANGELOG.md`.

## Kredit dan lisensi

Kode Wumi dirilis dengan lisensi MIT, lihat `LICENSE`. Wumi memakai atau membawa komponen berikut, lengkap dengan lisensinya di `NOTICE`:

- [AndroModem](https://github.com/basiooo/andromodem) oleh Bagas Julianto, dasar fitur Mirror
- [MetaCubeXD](https://github.com/MetaCubeX/metacubexd), dashboard Mihomo
- [scrcpy](https://github.com/Genymobile/scrcpy) (scrcpy-server) dan adb
- [xterm.js](https://github.com/xtermjs/xterm.js) untuk terminal web

Antarmuka BFR terinspirasi dari [webui_bfm](https://github.com/geeks121/webui_bfm); tidak ada kodenya yang dipakai.
