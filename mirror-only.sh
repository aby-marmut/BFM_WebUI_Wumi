#!/bin/sh
# Jadikan frontend AndroModem hanya Mirroring, lalu build ulang. Jalankan di Debian.
set -e
FE="$HOME/andromodem/templates/andromodem_fe"
S="$FE/src"
[ -d "$S" ] || { echo "STOP: $S tidak ada"; exit 1; }
mkdir -p "$HOME/andromodem-fe-orig"
for f in components/Content/Content.tsx layouts/MainLayout.tsx; do
  [ -f "$HOME/andromodem-fe-orig/$(basename $f)" ] || cp -p "$S/$f" "$HOME/andromodem-fe-orig/$(basename $f)"
done
cat > "$S/components/Content/Content.tsx" <<'E1'
import { type FC } from "react"

import { useDevicesStore } from "@/stores/devicesStore"
import { DeviceState } from "@/types/device"

import Mirroring from "../Mirroring/Mirroring"

const Content: FC = () => {
    const { deviceUsed } = useDevicesStore()

    if (deviceUsed == null) {
        return (
            <div className="w-full mt-5 text-center">
                <b className="text-md">Pilih perangkat</b>
            </div>
        )
    }

    if (deviceUsed.state !== DeviceState.ONLINE) {
        return (
            <div role="alert" className="alert alert-error mt-4">
                <span>Perangkat tidak online.</span>
            </div>
        )
    }

    return (
        <div className="w-full mt-3">
            <Mirroring device={deviceUsed}/>
        </div>
    )
}

export default Content
E1
cat > "$S/layouts/MainLayout.tsx" <<'E2'
import type { FC } from "react"

import Content from "@/components/Content/Content"
import DeviceSelector from "@/components/DeviceSelector/DeviceSelector"
import useHealthCheck from "@/hooks/useHealthCheck"

const MainLayout: FC = () => {
  const { error: healthError } = useHealthCheck()

  if (healthError) {
    throw healthError
  }

  return (
    <main className="my-2">
      <div className="container mx-auto px-2">
        <DeviceSelector/>
        <Content/>
      </div>
    </main>
  )
}

export default MainLayout
E2
(cd "$FE" && npm run build 2>&1 | tail -5)
cd "$HOME/andromodem" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o andromodem-arm64 cmd/andromodem/main.go
ls -lh andromodem-arm64
echo "Cadangan file asli: $HOME/andromodem-fe-orig"
