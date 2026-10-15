set -e
for f in ~/wumi/wumi ~/andromodem/andromodem-arm64 ~/adb-bundle/bin/adb; do [ -f "$f" ] || { echo "STOP: $f tidak ada"; exit 1; }; done
command -v zip >/dev/null || sudo apt install -y zip
M=$HOME/wumi-module; rm -rf $M; mkdir -p $M
cp ~/wumi/wumi $M/wumi
cp ~/andromodem/andromodem-arm64 $M/andromodem
cp -r ~/adb-bundle $M/adb
cp ~/wumi/LICENSE ~/wumi/NOTICE $M/
cat > $M/module.prop <<'E1'
id=wumi
name=Wumi (WebUI + AndroModem Mirror)
version=v2.7
versionCode=7
author=aby-marmut
description=WebUI mandiri: Status, BFR, Files, Control, Mirror. Jalan otomatis saat boot.
E1
cat > $M/service.sh <<'E2'
#!/system/bin/sh
A=/data/adb/andromodem
U=/data/adb/wumi
mkdir -p $A/home $A/tmp
until [ "$(getprop sys.boot_completed)" = 1 ]; do sleep 5; done
sleep 20
X=$(/system/bin/ip route get 1.1.1.1 2>/dev/null | sed -n 's/.* dev \([^ ]*\).*/\1/p' | head -n1)
case "$X" in rmnet*|ccmni*|wwan*) X="" ;; esac
for p in 5555 49153 8080 8081; do
  while iptables -D INPUT -p tcp --dport $p -j DROP 2>/dev/null; do :; done
  while iptables -D INPUT -p tcp --dport $p -s 192.168.0.0/16 -j ACCEPT 2>/dev/null; do :; done
  while iptables -D INPUT -p tcp --dport $p -s 127.0.0.1 -j ACCEPT 2>/dev/null; do :; done
  for i in rmnet+ ccmni+ wwan+ $X; do
    iptables -C INPUT -i $i -p tcp --dport $p -j DROP 2>/dev/null || iptables -I INPUT -i $i -p tcp --dport $p -j DROP
    command -v ip6tables >/dev/null 2>&1 && { ip6tables -C INPUT -i $i -p tcp --dport $p -j DROP 2>/dev/null || ip6tables -I INPUT -i $i -p tcp --dport $p -j DROP; }
  done
done
[ -f /data/adb/modules/wumi/http80.sh ] && { nohup sh /data/adb/modules/wumi/http80.sh >/dev/null 2>&1 & }
ps -A | grep -q "[w]umi$" || { cd $U; nohup $U/wumi -addr :8080 >$U/wumi.log 2>&1 & }
cd $A
export HOME=$A TMPDIR=$A/tmp PATH=$A/adb/bin:$PATH
adb start-server; sleep 2
ps -A | grep -q "[a]ndromodem$" || nohup $A/andromodem >$A/andromodem.log 2>&1 &
E2
cat > $M/customize.sh <<'E3'
SKIPMOUNT=true
ui_print "- Wumi v2.7"
[ "$ARCH" = arm64 ] || abort "! Hanya untuk arm64"
# --- pemeriksaan prasyarat ---
have(){ [ -x /system/bin/$1 ] || command -v $1 >/dev/null 2>&1; }
[ -d /data/adb/box ] || abort "! Box for Root tidak ada di /data/adb/box"
have iptables || abort "! iptables tidak ditemukan"
have ip6tables || ui_print "! ip6tables tidak ada: penjaga IPv6 tidak terpasang"
AP=$(getprop service.adb.tcp.port)
case "$AP" in ""|0|-1) ui_print "! adbd TCP belum aktif: SMS, Mirror, TTL akan nonaktif";; *) ui_print "- adbd TCP port $AP";; esac
W=$(/system/bin/ip route get 1.1.1.1 2>/dev/null | sed -n 's/.* dev \([^ ]*\).*/\1/p' | head -n1)
if [ -n "$W" ]; then ui_print "- WAN: $W $(/system/bin/ip -4 -o addr show dev $W 2>/dev/null | awk '{print $4}' | head -n1)"; else ui_print "- WAN belum terdeteksi (tidak menolak)"; fi
A=/data/adb/andromodem
U=/data/adb/wumi
mkdir -p $A/home $A/tmp $U
inst(){ cp "$1" "$2.new" && chmod 755 "$2.new" && mv -f "$2.new" "$2"; }
inst $MODPATH/wumi $U/wumi
inst $MODPATH/andromodem $A/andromodem
rm -rf $A/adb.new; mkdir -p $A/adb.new; cp -r $MODPATH/adb/. $A/adb.new/
chmod 755 $A/adb.new/bin/adb; chmod 644 $A/adb.new/lib/*
rm -rf $A/adb.old; [ -d $A/adb ] && mv $A/adb $A/adb.old
mv $A/adb.new $A/adb
rm -rf $MODPATH/wumi $MODPATH/andromodem $MODPATH/adb
S=/data/adb/service.d/andromodem.sh
[ -f $S ] && cp -p $S $A/service.d.andromodem.sh.bak && rm -f $S && ui_print "- service.d lama dinonaktifkan"
if [ -f $U/auth.json ]; then ui_print "- Sandi lama dipakai"; else ui_print "! Belum ada sandi. Setelah reboot: printf %s SANDI | $U/wumi -set-password"; fi
set_perm $MODPATH/service.sh 0 0 0755
ui_print " "
ui_print "===== Akses Wumi ====="
ui_print "- Dari perangkat ini: http://127.0.0.1:8080"
ui_print "- Dari perangkat lain (hotspot/Wi-Fi): http://IP-PERANGKAT:8080"
ui_print "  (bila tethering aktif, http://IP-gateway-hotspot juga bisa, tanpa :8080)"
ui_print "- Login: admin"
ui_print "- Selesai. Reboot untuk menjalankan."
E3
cat > $M/uninstall.sh <<'E4'
#!/system/bin/sh
pkill -x wumi; pkill -x andromodem; pkill -x adb
for p in 8080 8081 49153; do
  for r in "-j DROP" "-s 192.168.0.0/16 -j ACCEPT" "-s 127.0.0.1 -j ACCEPT"; do
    iptables -D INPUT -p tcp --dport $p $r 2>/dev/null
  done
done
E4
cat > $M/http80.sh <<'E5'
#!/system/bin/sh
# Wumi: alihkan port 80 -> 8080 hanya untuk IP gateway di interface tethering terdeteksi
MD=/data/adb/modules/wumi
IPT=iptables
IP=/system/bin/ip
$IPT -t nat -S >/dev/null 2>&1 || exit 0
detect() {
  out=$(/system/bin/dumpsys tethering 2>/dev/null)
  if [ -n "$out" ]; then
    echo "$out" | sed -n -e 's/^[[:space:]]*\([A-Za-z0-9_.-]*\) - TetheredState.*/\1/p' -e 's/^[[:space:]]*\([A-Za-z0-9_.-]*\) - LocalHotspotState.*/\1/p' | sort -u
  else
    for d in /sys/class/net/*; do
      i=${d##*/}
      case "$i" in lo|rmnet*|ccmni*|wwan*|tun*|dummy*|ifb*|sit*|ip6*|p2p*|veth*|docker*|umts*|wlan0) continue;; esac
      [ "$(cat $d/operstate 2>/dev/null)" = up ] && echo "$i"
    done
  fi
}
apply() {
  want=""
  for i in $(detect); do
    a=$($IP -4 -o addr show "$i" 2>/dev/null | sed -n 's/.* inet \([0-9.]*\)\/.*/\1/p' | head -n1)
    [ -n "$a" ] && want="$want $i/$a"
  done
  $IPT -t nat -N WUMI80 2>/dev/null
  n=$($IPT -t nat -S WUMI80 2>/dev/null | grep -c REDIRECT)
  set -- $want
  if [ "$want" != "$last" ] || [ "$n" != "$#" ]; then
    $IPT -t nat -F WUMI80
    for w in $want; do
      $IPT -t nat -A WUMI80 -i ${w%/*} -d ${w#*/} -p tcp --dport 80 -j REDIRECT --to-ports 8080
    done
    last="$want"
  fi
  $IPT -t nat -C PREROUTING -j WUMI80 2>/dev/null || $IPT -t nat -I PREROUTING 1 -j WUMI80
}
if [ "$1" = once ]; then apply; exit 0; fi
while [ -f $MD/module.prop ] && [ ! -e $MD/disable ] && [ ! -e $MD/remove ]; do
  apply; sleep 15
done
$IPT -t nat -D PREROUTING -j WUMI80 2>/dev/null
$IPT -t nat -F WUMI80 2>/dev/null
$IPT -t nat -X WUMI80 2>/dev/null
E5
cd $M && rm -f ../wumi-module-v2.7.zip && zip -r9q ../wumi-module-v2.7.zip .
ls -lh ~/wumi-module-v2.7.zip
