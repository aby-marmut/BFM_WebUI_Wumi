# CHANGELOG

Ringkas; detail ada di git log.

## v2.8 (2026-10-16)

### Baru
- Menu **Keluar** (logout) di paling bawah menu samping, terpisah dari menu lain. Ketuk ikon, lalu konfirmasi "Lanjut keluar halaman?" (OK / Batal). Saat keluar tampil lingkaran loading, dengan batas waktu 6 detik dan pesan galat yang jelas bila gagal.
- Status: kartu CPU menampilkan jumlah core, kartu RAM menampilkan total RAM.

### Perbaikan
- BFR: tombol **Stop** kini membersihkan aturan iptables Box (`box.iptables disable`). Sebelumnya aturan TPROXY dan pengalihan DNS tertinggal sehingga klien LAN buntu walau perangkat online.
