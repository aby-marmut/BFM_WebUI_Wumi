# CHANGELOG (ringkas; detail ada di git log)

## 2026-10-13
- Arsip: dukungan **RAR** (ekstrak, isi arsip), **sandi** untuk zip (AES-256), 7z (header ikut disandikan), dan rar; **Isi arsip** di dialog berkas (minta sandi bila terkunci). `7zz` dipasang di Note 8. Popup "Sebagian gagal" diperkecil dan overflow horizontal dikunci. (`archive.go`, blok `arc2`). Lulus uji pemilik 100%.
- Arsip dasar (zip/tar.gz/7z buat+ekstrak) dan popup aksi Files yang bisa digeser (`archive.go`, blok `fxarc`).
- Files mode Root, Izin (chmod/chown), Salin/Pindah (`files3.go`, blok `fileroot`); lulus uji.
- Judul login "Welcome Wumi Dashboard"; penanda header "Wumi : ON/OFF" (`wumi.go`); BFR > Config langganan dan backup/restore (`bfrprov.go`); terminal web (`terminal.go`, pending uji).
- `andromodem.sh.bak-termux` dinonaktifkan; 5555 hanya `DROP -i rmnet+`.
- Nama resmi proyek: Wumi.

## 2026-10-11
- Admin (ganti sandi login, `admin.go`). Menu4, ui5, ui6 (menu samping, dialog tengah, tata letak Files/Status/Mirror/Control). Uji tombol Reboot/Recovery/Shutdown lulus.

## 2026-10-10
- Migrasi subnet MikroTik ke subnet baru. Files lanjut (hapus, ganti nama, edit/simpan). Menu samping kiri. Link MetaCube di header. PHP/BFR WebUI lama dihapus. Blokir 3001. Clash 9090 dan AndroModem 49153 dikunci ke 127.0.0.1. Modul Magisk v1.1 lulus uji reboot.
