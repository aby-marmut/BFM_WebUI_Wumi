# Files

## Mode
`rootMode` (`atomic.Bool`, memori, global semua sesi). `POST /api/files/mode/set {root}`, `GET /api/files/mode`. **No Root** (default): baca di jalur yang diizinkan `safePathStrict`, tulis hanya di `/storage/emulated/0`, `/data/local/tmp`, `/data/adb/box/clash`. **Root**: jalur absolut apa pun (tetap `EvalSymlinks`), banner merah, daftar lokasi menambah "Root (/)". `regOnly` membungkus `/api/files/get` dan `/text`: mode root menolak (415) non-berkas-biasa serta `/proc/kmsg`, `/proc/kcore`.

## Endpoint
`GET /api/files/{roots,list,get,text,mode,stat}`; `POST /api/files/{upload,mkdir,delete,rename,save,perm,copy,move,mode/set}`.
- `delete`: folder hanya bila kosong. `save`: teks <=256 KB, cadangan `.bak`, hanya jalur tulis.
- `copy`/`move` `{from,names[],to}` (maks 200): tidak pernah menimpa, tidak ke dalam dirinya sendiri; move lintas perangkat (EXDEV) = salin lalu hapus (uid/gid dipertahankan); symlink disalin sebagai symlink; batas 100000 entri; satu operasi sekali (`TryLock`, 409).
- `stat` (root): `mode` oktal, uid, gid, link, dir, `ctx` SELinux. `perm {dir,name,mode?,uid?,gid?}` (root): chown dulu lalu chmod, 0000-7777, symlink ditolak, tidak rekursif.
- `protectedPath` (dipakai `childOf`: hapus, rename, move, salin sumber, izin): turunan langsung `/` dan `/data`, `/data/adb`, `/data/adb/{modules,wumi,box}`, `/storage/emulated`, `/storage/emulated/0`, `/data/local`, `/data/local/tmp`.

## Arsip (`archive.go`)
- `GET /api/files/arcinfo` -> `{7z, rar}` (ada tidaknya `7zz`).
- `POST /api/files/zip {dir,names[],fmt(zip|tar.gz|7z),out,pw}`: tidak menimpa. Tanpa sandi: zip/tar.gz stdlib. Sandi: zip = 7zz AES-256, 7z = `-mhe=on`; tar.gz + sandi ditolak (400).
- `POST /api/files/unzip {dir,name,to,pw}`: zip/tar/tar.gz/tgz stdlib; 7z, rar, dan zip bersandi lewat 7zz. Symlink dan berkas khusus dilewati, nama entri dibersihkan dari `..`, dest dihapus bila gagal. Galat sandi: **403** dengan teks berawalan `SANDI:` (UI menampilkan kolom sandi).
- `POST /api/files/arclist {dir,name,pw}`: daftar (maks 3000 tampil, `total` = jumlah sebenarnya). Zip/tar/tar.gz daftar dari stdlib; 7z/rar via `7zz l -slt -ba`. Header terkunci tanpa sandi = 403 `SANDI:`. Bila ada entri terkunci dan sandi diberikan, diuji dengan `7zz t` pada satu entri (`verified`).
- Sandi dikirim ke 7zz lewat **stdin** (7zz hanya meminta bila perlu), tidak lewat argumen, tidak dicatat di log.
- `7zz`: `/data/adb/wumi/7zz` (atau `7z`/`7za` di PATH). Pasang: `docs/deploy.md`.

## Front-end
Kontrol Mode di kartu File manager; popup aksi bawah/melayang (`.fxbar.fxpop`: Salin, Pindah, Nama, Arsip, Ekstrak, Izin, Hapus) bisa digeser; popup tempel `.fxpb`. Status ceklis dibaca dari DOM (`#fl .fxc:checked`, nama dari `aria-label` "Pilih NAMA"). Dialog berkas: Lihat teks / Edit teks / Unduh / Simpan; untuk arsip "Lihat teks" menjadi **Isi arsip**. Dialog arsip `.sheet.nt` bertinggi tetap.
