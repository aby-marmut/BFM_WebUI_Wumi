# CHANGELOG

Ringkas; detail ada di git log.

## v2.9 (2026-10-17)

### Baru
- Kartu **Cek pembaruan** di menu Control: versi terpasang, rilis terbaru, catatan rilis (perbaikan bug dan fitur baru), tombol Cek sekarang, dan saklar notifikasi. Titik merah muncul di menu Control dan di kartu bila ada versi baru. Hanya memberi tahu, tanpa unduh atau pasang otomatis.

### Perbaikan
- Mirror kini bisa dihubungkan saat Wumi dibuka lewat port 80 (tanpa :8080), misalnya dari menu "Kelola router" Android klien.

## v2.8.1 (2026-10-16)

### Perbaikan
- BFR: tombol **Start** kini memasang aturan iptables Box (`box.iptables renew`), seperti **Restart**. Di v2.8, setelah Stop membersihkan aturan, Start menghidupkan Clash tanpa aturan tproxy sehingga klien LAN keluar langsung dengan IP ISP, bukan IP node.

## v2.8 (2026-10-16)

### Baru
- Menu **Keluar** (logout) di paling bawah menu samping, terpisah dari menu lain. Ketuk ikon, lalu konfirmasi "Lanjut keluar halaman?" (OK / Batal). Saat keluar tampil lingkaran loading, dengan batas waktu 6 detik dan pesan galat yang jelas bila gagal.
- Status: kartu CPU menampilkan jumlah core, kartu RAM menampilkan total RAM.

### Perbaikan
- BFR: tombol **Stop** kini membersihkan aturan iptables Box (`box.iptables disable`). Sebelumnya aturan TPROXY dan pengalihan DNS tertinggal sehingga klien LAN buntu walau perangkat online.
