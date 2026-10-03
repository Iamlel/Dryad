#!/usr/bin/env bash
# Build Groot on this machine and run it on the Arduino UNO Q (via adb).
#
#   ./deploy.sh             build the Go server for the board, push it, restart it
#   ./deploy.sh firmware    compile + flash the MCU sketch
#   ./deploy.sh all         firmware, then server
#   ./deploy.sh install     one time: start the server on every boot (asks for the
#                           board's sudo password)
#   ./deploy.sh logs        follow the server's log on the board
#   ./deploy.sh watch       print the latest sensor reading every second
#
# With several adb devices attached, pick one with ANDROID_SERIAL=<serial>.
set -euo pipefail
cd "$(dirname "$0")"

REMOTE_DIR=/home/arduino/groot
FQBN=arduino:zephyr:unoq

server() {
    echo "==> building server (linux/arm64)"
    mkdir -p build
    (cd server && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
        go build -trimpath -ldflags="-s -w" -o ../build/groot .)

    echo "==> pushing to $REMOTE_DIR"
    adb shell "mkdir -p $REMOTE_DIR"
    adb push build/groot "$REMOTE_DIR/groot.new" >/dev/null
    # personalities.json holds the plants' memories on the board: never overwrite it.
    adb shell "[ -e $REMOTE_DIR/personalities.json ]" ||
        adb push server/personalities.json "$REMOTE_DIR/" >/dev/null
    if [[ -f server/.env ]]; then
        adb push server/.env "$REMOTE_DIR/.env" >/dev/null
    fi
    # Rename over the running binary (safe on Linux), then kill the old process;
    # systemd restarts it from the new file.
    adb shell "mv $REMOTE_DIR/groot.new $REMOTE_DIR/groot && chmod +x $REMOTE_DIR/groot"

    if adb shell "systemctl is-enabled --quiet groot 2>/dev/null"; then
        adb shell "pkill -x groot || true"
        sleep 3
        adb shell "systemctl is-active --quiet groot" &&
            echo "==> running. http://$(board_ip):8080/api/sensors" ||
            { echo "service failed to start, see ./deploy.sh logs" >&2; exit 1; }
    else
        echo "==> pushed. Run './deploy.sh install' once so it starts on boot."
    fi
}

firmware() {
    local port
    port=$(arduino-cli board list | awk -v fqbn="$FQBN" '$0 ~ fqbn {print $1; exit}')
    [[ -n "$port" ]] || { echo "UNO Q not found by arduino-cli" >&2; exit 1; }
    echo "==> flashing firmware via $port"
    arduino-cli compile -b "$FQBN" firmware
    arduino-cli upload -b "$FQBN" -p "$port" firmware
}

install() {
    adb push deploy/groot.service /tmp/groot.service >/dev/null
    echo "==> installing boot service (enter the board's password if asked)"
    adb shell -t "sudo install -m 644 /tmp/groot.service /etc/systemd/system/groot.service \
        && sudo systemctl daemon-reload && sudo systemctl enable --now groot \
        && sudo systemctl restart groot"
    echo "==> installed. http://$(board_ip):8080/api/sensors"
}

board_ip() {
    adb shell "ip -4 -o addr show wlan0 | awk '{print \$4}' | cut -d/ -f1" | tr -d '\r'
}

case "${1:-server}" in
    server)   server ;;
    firmware) firmware ;;
    all)      firmware; server ;;
    install)  install ;;
    logs)     exec adb shell -t "journalctl -u groot -f -n 50" ;;
    watch)    exec adb shell -t "while sleep 1; do curl -s localhost:8080/api/sensors; done" ;;
    *) sed -n '2,12p' "$0"; exit 1 ;;
esac
