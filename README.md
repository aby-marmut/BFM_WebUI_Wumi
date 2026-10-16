<div align="center">

# BFR/BFM WebUI Wumi

[![Platform](https://img.shields.io/badge/platform-Android-3DDC84?logo=android&logoColor=white)](https://www.android.com/)
[![Language](https://img.shields.io/badge/language-Go-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Architecture](https://img.shields.io/badge/arch-ARM64-blue)](#requirements)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

</div>

---

**A lightweight web UI for rooted Android devices.**

Manage your Android-based LTE modem, hotspot, or gateway from a single web interface.

Built with Go. Runs as a standalone binary on the device.

## 📱 About

Wumi berawal dari proyek pribadi untuk mempermudah pengelolaan perangkat Android yang digunakan sebagai gateway jaringan.
Awalnya Wumi dibuat hanya untuk kebutuhan sendiri. Kodenya kemudian dirapikan dan dibagikan agar pengguna lain bisa mencoba, mengembangkan, atau membantu menguji kompatibilitasnya dengan perangkat lain.
Wumi berjalan langsung di Android sebagai satu biner Go, tanpa PHP maupun Termux. Antarmukanya dirancang untuk layar ponsel dan bekerja bersama Box for Root.

## ✨ Features

| Section | What it does |
| --- | --- |
| 📊 **Status** | Network topology, IP addresses, traffic, latency, speed tests, and vnStat statistics. |
| 📦 **BFR** | Manage Box for Root, Backup & Restore Config, view logs, update subscriptions, include MetaCubeXD dashboard. |
| 📁 **Files** | Browse and manage files in No Root or Root mode, change permissions, copy and move files, edit text, and handle archives. |
| 🎛️ **Control** | Apply TTL settings, persist TTL across reboots, change the admin password, reboot, enter recovery, and shut down the device. |
| 🪞 **Mirror** | Mirror the device screen through AndroModem. |
| 💻 **Web Terminal** | Access a terminal through the browser with root privileges. |

## 📋 Requirements

Before installing Wumi, make sure your device has:

- An ARM64 Android device.
- Root access through Magisk.
- Box for Root installed at `/data/adb/box`.
- TCP debugging (`adbd`) enabled for the Mirror feature.

**Box for Root wajib tersedia. Pemasangan modul akan ditolak jika direktori /data/adb/box tidak ditemukan..

Fitur Mirror memerlukan ADB melalui TCP. Fitur lainnya dapat digunakan tanpa Mirror selama kebutuhan masing-masing terpenuhi.

## 🚀 Installasi

### 1. Install modul

Flash ZIP modul Wumi melalui Magisk seperti biasa, kemudian reboot.
Pemasangan akan gagal otomatis jika menggunakan ARM32.
Setelah reboot untuk masuk wumi perlu setting password sendiri.

### 2. Jika Lupa admin password

Buka termux : shell > su 
Lalu paste ini untuk memasukan password baru.

```sh
printf %s 'YOUR_PASSWORD' | /data/adb/wumi/wumi -set-password
```

Ganti `YOUR_PASSWORD` dengan password barumu.


### 3. Buka antarmuka Wumi

```text
http://<DEVICE-IP>:8080
atau
http://127.0.0.1:8080
```

Masuk menggunakan:
Username: admin
Sandi: sandi yang telah dibuat sebelumnya.
Replace `<DEVICE-IP>` with the Android device's reachable IP address.

## 🔐 Keamanan

Perlindungan Wumi pada antarmuka web:
- Sandi disimpan menggunakan PBKDF2-SHA256.
- Login dibatasi hingga lima percobaan gagal per alamat IP dalam sepuluh menit.
- Cookie sesi menggunakan atribut HttpOnly.
- Permintaan dibatasi untuk loopback dan jaringan privat.
- Modul memasang aturan iptables untuk memblokir port 5555, 8080, 8081, dan 49153 pada antarmuka seluler.

### ⚠️ Wajib menggunakan auto enabler

## 🛠️ Build from Source

Wumi uses the Go standard library for its backend.
To build the ARM64 binary, run:

```sh
sh build.sh
```

The build script expects the MetaCubeXD distribution archive at:

```text
~/metacubexd-dist.tgz
```

Make sure the archive is available before building.

For module packaging and deployment instructions, see [`docs/deploy.md`](docs/deploy.md).

## 📚 Documentation

Additional documentation is available in the repository:

- [`docs/api`](docs/api) — API documentation.
- [`docs/deploy.md`](docs/deploy.md) — Module packaging and deployment.
- [`docs/files`](docs/files) — File manager documentation.
- [`docs/network`](docs/network) — Network-related documentation.
- [`CHANGELOG.md`](CHANGELOG.md) — Changes between versions.
- [`NOTICE`](NOTICE) — Third-party components and their licenses.
- [`LICENSE`](LICENSE) — MIT License.

## 🙏 Credits

Wumi builds on several existing projects. Their work makes it possible to bring these features together in a single interface.

- [Box for Root](https://github.com/taamarin/box_for_magisk) — Root-based proxy environment.
- [AndroModem](https://github.com/basiooo/andromodem) by Bagas Julianto — Foundation for the Mirror feature.
- [MetaCubeXD](https://github.com/MetaCubeX/metacubexd) — Mihomo web dashboard.
- [scrcpy](https://github.com/Genymobile/scrcpy) — Includes `scrcpy-server`.
- [Android Debug Bridge (ADB)](https://developer.android.com/tools/adb) — Android device communication.
- [xterm.js](https://github.com/xtermjs/xterm.js) — Terminal interface for the browser.

Third-party components and their applicable licenses are documented in [`NOTICE`](NOTICE).

## 📄 License

Wumi is released under the [MIT License](LICENSE).

See the license file for the full terms.

---

<div align="center">

**Wumi** · Built for my own setup, shared with the community.

</div>
