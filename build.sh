#!/bin/sh
# Jalankan di folder ini (Debian). Hasil: ./wumi (arm64)
set -e
cd "$(dirname "$0")"
if [ ! -f web/zash/index.html ]; then
  rm -rf web/zash && mkdir -p web/zash
  C="$HOME/metacubexd-dist.tgz"
  if [ ! -f "$C" ]; then
    curl -fsSL -o "$C" https://github.com/MetaCubeX/metacubexd/releases/latest/download/compressed-dist.tgz || { rm -f "$C"; echo "STOP: gagal mengunduh MetaCubeXD"; exit 1; }
  fi
  tar xzf "$C" -C web/zash
  [ -f web/zash/index.html ] || { echo "STOP: paket dashboard tidak valid"; exit 1; }
fi
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o wumi .
ls -lh wumi
