<div align="center">

# Wumi

**A lightweight web UI for rooted Android devices.**

Manage your Android-based LTE modem, hotspot, or gateway from a single web interface.

Built with Go. Runs as a standalone binary on the device.

[![Platform](https://img.shields.io/badge/platform-Android-3DDC84?logo=android&logoColor=white)](https://www.android.com/)
[![Language](https://img.shields.io/badge/language-Go-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Architecture](https://img.shields.io/badge/arch-ARM64-blue)](#requirements)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

</div>

---

## 📱 About

Wumi started as a personal project to make managing a rooted Android device as a network gateway a little easier.

Instead of switching between shell commands, configuration files, and separate web interfaces, Wumi brings the tools I use most into one place.

It runs directly on Android as a single Go binary, with no PHP or Termux required. The interface is designed for mobile screens and works alongside [Box for Root](https://github.com/taamarin/box_for_magisk).

Wumi is developed primarily for my own setup, but the source is available for anyone who wants to try it, adapt it, or help test it on other devices.

## ✨ Features

| Section | What it does |
| --- | --- |
| 📊 **Status** | Network topology, IP addresses, traffic, latency, speed tests, and vnStat statistics. |
| 📦 **BFR** | Manage Box for Root, view logs, update subscriptions, and open the MetaCubeXD dashboard. |
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

**Box for Root is required.** The module installation is designed to stop if `/data/adb/box` is missing.

The Mirror feature also requires TCP debugging to be available. Other features may work without Mirror, provided their individual requirements are met.

## 🚀 Installation

### 1. Install the module

Flash the Wumi module ZIP through Magisk, then reboot the device.

### 2. Set the admin password

On the device, open a root shell and run:

```sh
printf %s 'YOUR_PASSWORD' | /data/adb/wumi/wumi -set-password
```

Replace `YOUR_PASSWORD` with your chosen password.

**Keep your password private.** Avoid sharing the command with the actual password in screenshots, logs, or public posts.

### 3. Open the web interface

From another device connected to the same private network, open:

```text
http://<DEVICE-IP>:8080
```

Log in with:

- **Username:** `admin`
- **Password:** The password you configured above.

Replace `<DEVICE-IP>` with the Android device's reachable IP address.

## 🔐 Security

Wumi includes several protections for its web interface:

- Passwords are stored using **PBKDF2-SHA256**.
- Login attempts are rate-limited to five failed attempts per IP address within ten minutes.
- Session cookies use the `HttpOnly` attribute.
- Requests are restricted to loopback and private networks.
- The module installs iptables rules to block ports `5555`, `8080`, `8081`, and `49153` on cellular interfaces.

### ⚠️ Important: ADB over TCP

The `adbd` TCP service on port `5555` may be exposed without authentication, depending on the module providing it.

Wumi relies on its iptables rules to block the listed ports on cellular interfaces. **Do not disable the Wumi module without first arranging an alternative firewall rule for the services it protects.**

These rules are not a substitute for checking your network configuration. Keep administrative access restricted to networks you trust, and do not assume that a private-network restriction makes an exposed service safe.

## 📱 Tested Device

Wumi has been tested on the following setup:

| Component | Configuration |
| --- | --- |
| Device | Samsung Galaxy Note 8 (SM-N950F) |
| Android | LineageOS 20 / Android 13 |
| Root | Magisk |
| Architecture | ARM64 |

Other devices have not been verified yet. Compatibility may depend on the Android build, root environment, available utilities, and network configuration.

If you try Wumi on another device, feedback about what works and what doesn't is welcome.

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
