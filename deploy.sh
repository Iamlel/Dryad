#!/usr/bin/env bash
# Build Groot on this machine and run it on the Arduino UNO Q (via adb).
#
#   ./deploy.sh             build the Go server for the board, push it, restart it
#   ./deploy.sh firmware    compile + flash the MCU sketch
#   ./deploy.sh all         firmware, then server
#   ./deploy.sh install     one time: start the server now and on every boot
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
        go build -trimpath -ldflags="-s -w -X main.version=$(git describe --always --dirty)" -o ../build/groot .)

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
    # keep-running.sh restarts it from the new file.
    adb shell "mv $REMOTE_DIR/groot.new $REMOTE_DIR/groot && chmod +x $REMOTE_DIR/groot"

    if adb shell "crontab -l 2>/dev/null | grep -q keep-running.sh"; then
        local old_pid
        old_pid=$(server_pid)
        adb shell "pkill -x groot || true"
        wait_healthy "$old_pid"
    else
        echo "==> pushed. Run './deploy.sh install' once so it starts on boot."
    fi
}

server_pid() { adb shell "pidof groot || true" | tr -d '\r'; }

# wait_healthy <old pid>: wait until a new server process answers /healthz.
wait_healthy() {
    local pid health
    echo "==> waiting for the server to start"
    for _ in $(seq 30); do
        pid=$(server_pid)
        if [[ -n "$pid" && "$pid" != "$1" ]] &&
            health=$(adb shell "curl -fsS -m 2 localhost:8080/healthz" 2>/dev/null); then
            echo "==> running: $health"
            echo "==> http://$(board_ip):8080/api/plants"
            return
        fi
        sleep 0.5
    done
    echo "the server didn't start, see ./deploy.sh logs" >&2
    exit 1
}

firmware() {
    local port
    port=$(arduino-cli board list | awk -v fqbn="$FQBN" '$0 ~ fqbn {print $1; exit}')
    [[ -n "$port" ]] || { echo "UNO Q not found by arduino-cli" >&2; exit 1; }
    echo "==> flashing firmware via $port"
    arduino-cli compile -b "$FQBN" firmware
    arduino-cli upload -b "$FQBN" -p "$port" firmware
}

# install: run keep-running.sh at every boot from the arduino user's crontab
# (no sudo needed), and start it now.
install() {
    local loop="$REMOTE_DIR/keep-running.sh"
    adb push deploy/keep-running.sh "$loop" >/dev/null
    adb shell "chmod +x $loop"
    echo "==> starting the server at every boot"
    adb shell "(crontab -l 2>/dev/null | grep -v keep-running.sh; echo '@reboot $loop') | crontab -"
    # Anchored, so pgrep doesn't match this very command line.
    if ! adb shell "pgrep -f '^/bin/sh $loop' >/dev/null"; then
        adb shell "(setsid $loop </dev/null >/dev/null 2>&1 &)"
    fi
    wait_healthy ""
}

# watch: print the server's latest sensor reading once a second.
watch() {
    if ! adb shell "curl -fsS -m 2 localhost:8080/healthz" >/dev/null 2>&1; then
        echo "The server isn't running on the board, so there's nothing to watch." >&2
        echo "Start it with './deploy.sh install' (once; after that it starts on every boot)." >&2
        exit 1
    fi
    echo "==> sensor readings, once a second (Ctrl+C to stop)"
    local json
    while true; do
        if json=$(adb shell "curl -fsS -m 2 localhost:8080/api/sensors" 2>/dev/null); then
            echo "$json" | sed -E \
                -e 's/.*"moisture_pct":([^,]*),"light_pct":([^,]*),"temperature_c":([^,]*),.*"stale":([a-z]*).*/moisture \1%   light \2%   temp \3 C   stale=\4/' \
                -e 's/null%?/--/g; s/   stale=false//; s/stale=true/(stale: the board stopped reporting)/' \
                -e "s/^/$(date +%H:%M:%S)  /"
        else
            echo "$(date +%H:%M:%S)  no reading yet (or the server stopped)"
        fi
        sleep 1
    done
}

board_ip() {
    adb shell "ip -4 -o addr show wlan0 | awk '{print \$4}' | cut -d/ -f1" | tr -d '\r'
}

case "${1:-server}" in
    server)   server ;;
    firmware) firmware ;;
    all)      firmware; server ;;
    install)  install ;;
    logs)     exec adb shell -t "tail -n 50 -f $REMOTE_DIR/groot.log" ;;
    watch)    watch ;;
    *) sed -n '2,12p' "$0"; exit 1 ;;
esac
