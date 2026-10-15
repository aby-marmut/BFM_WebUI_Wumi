N=${N:-<IP-ADB>:5555}
T=/data/data/com.termux/files/usr
O=$HOME/adb-bundle
command -v readelf >/dev/null && command -v patchelf >/dev/null || sudo apt install -y binutils patchelf
rm -rf $O; mkdir -p $O/bin $O/lib
pull(){ adb -s $N exec-out "su -c 'cat $1'" > "$2"; [ -s "$2" ] || { echo "GAGAL tarik $1"; rm -f "$2"; return 1; }; }
pull $T/bin/adb $O/bin/adb || exit 1
SKIP=" libc.so libm.so libdl.so liblog.so libandroid.so "
q="$O/bin/adb"; seen=" "
while [ -n "$q" ]; do
  set -- $q; f=$1; shift; q="$*"
  for l in $(readelf -d "$f" | sed -n 's/.*NEEDED.*\[\(.*\)\]/\1/p'); do
    case "$SKIP" in *" $l "*) continue;; esac
    case "$seen" in *" $l "*) continue;; esac
    seen="$seen$l "
    if pull $T/lib/$l $O/lib/$l; then q="$q $O/lib/$l"; fi
  done
done
patchelf --set-rpath '$ORIGIN/../lib' $O/bin/adb
for f in $O/lib/*; do patchelf --set-rpath '$ORIGIN' "$f" 2>/dev/null; done
chmod 755 $O/bin/adb
echo "== BUNDEL: $(ls $O/lib | wc -l) pustaka, $(du -sh $O | cut -f1)"
cat > /tmp/t.sh <<'IN'
A=/data/local/tmp/adbtest
chmod 755 $A/bin/adb; chmod 644 $A/lib/*; mkdir -p $A/home
E="env -i HOME=$A/home"
echo "== VERSI"; $E $A/bin/adb version | head -2
echo "== DEVICE (server port 5099)"; $E $A/bin/adb -P 5099 devices
$E $A/bin/adb -P 5099 kill-server
IN
adb -s $N shell 'su -c "rm -rf /data/local/tmp/adbtest; mkdir -p /data/local/tmp/adbtest"'
adb -s $N push $O/. /data/local/tmp/adbtest/ >/dev/null
adb -s $N push /tmp/t.sh /data/local/tmp/ >/dev/null
adb -s $N shell 'su -c "sh /data/local/tmp/t.sh" 2>&1'
