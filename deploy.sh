#!/usr/bin/env bash
# Build Dryad on this machine and run it on the Arduino UNO Q (via adb): the
# Go server (sensors + API, port 8080), the Flask website (port 5000) and,
# once server/.env has a CLOUDFLARE_TUNNEL_TOKEN, the Cloudflare tunnel that
# puts the website on your domain.
#
#   ./deploy.sh             build them, push them, restart them; from then on
#                           they also start on every boot
#   ./deploy.sh firmware    compile + flash the MCU sketch
#   ./deploy.sh all         firmware, then the above
#   ./deploy.sh stop        stop them, and don't start them at boot (until ./deploy.sh)
#   ./deploy.sh logs        follow their logs on the board
#   ./deploy.sh watch       print the latest sensor reading every second
#
# With several adb devices attached, pick one with ANDROID_SERIAL=<serial>.
set -euo pipefail
cd "$(dirname "$0")"

REMOTE_DIR=/home/arduino/dryad
FQBN=arduino:zephyr:unoq
LOOP=$REMOTE_DIR/keep-running.sh
# How keep-running.sh runs each process. Anchored, so pgrep and pkill never
# match their own command line.
SERVER_CMD='^./dryad -headless'
WEB_CMD='^python3 frontend/serve.py'
TUNNEL_CMD='^./cloudflared tunnel'

deploy() {
    echo "==> building the server (linux/arm64)"
    mkdir -p build
    (cd server && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
        go build -trimpath -ldflags="-s -w -X main.version=$(git describe --always --dirty)" -o ../build/dryad .)

    # The board's Python (3.13) has no pip, so the website's packages are
    # installed here, built for the board, and pushed. Redone only when
    # requirements.txt changes.
    local new_packages=
    if [[ ! build/pydeps -nt frontend/requirements.txt ]]; then
        echo "==> installing the website's Python packages for the board"
        rm -rf build/pydeps build/pydeps.tmp
        python3 -m pip install --quiet --disable-pip-version-check --target build/pydeps.tmp \
            --platform manylinux_2_28_aarch64 --platform manylinux2014_aarch64 \
            --python-version 3.13 --implementation cp --only-binary=:all: \
            -r frontend/requirements.txt
        mv build/pydeps.tmp build/pydeps
        new_packages=1
    fi

    # The page's QR decoder and fonts aren't in git: fetched from npm into
    # frontend/static/ (gitignored) when missing.
    fetch_npm jsqr 1.4.0 \
        sha512-dxLob7q65Xg2DvstYkRpkYtmKm2sPJ9oFhrhmudT1dZvNFFTlroai3AWSpLey/w5vMcLBXRgOJsbXpdN9HzU/A== \
        dist/jsQR.js=vendor/jsQR.js LICENSE=vendor/jsQR-LICENSE.txt
    fetch_npm @fontsource/chewy 5.3.0 \
        sha512-9p1c7sTqudOEP3RFbcI0h4r3Mq5+Atr6VBF70LGFegU0FAOt63asVt5ufBlojPF5+YFbSqq5iXjRdNt+k2l+2A== \
        files/chewy-latin-400-normal.woff2=fonts/chewy-latin-400-normal.woff2 LICENSE=fonts/Chewy-LICENSE.txt
    fetch_npm @fontsource/nunito 5.3.0 \
        sha512-vw9TaTQJ/zpEpKrsODuPmOvaVjXgdda6B+xXA4YqjrdeJ62MLgVoDerdRXFQHyBnSCt2yQ2nHKCHPP/Pv7Xq4Q== \
        files/nunito-latin-400-normal.woff2=fonts/nunito-latin-400-normal.woff2 \
        files/nunito-latin-800-normal.woff2=fonts/nunito-latin-800-normal.woff2 LICENSE=fonts/Nunito-LICENSE.txt

    # Cloudflare's tunnel connector: its official ARM64 build, checked
    # against the SHA-256 published with the release.
    local cf_version=2026.9.3
    local cf_sha256=aaeb2d7d0da3614634c7e03ab13487a1522c2e79165ed2929cfe23d5e95b326d
    local cloudflared=build/cloudflared-$cf_version
    if [[ ! -f "$cloudflared" ]]; then
        echo "==> downloading cloudflared $cf_version"
        curl -fsSL -o "$cloudflared.tmp" \
            "https://github.com/cloudflare/cloudflared/releases/download/$cf_version/cloudflared-linux-arm64"
        if [[ "$(openssl dgst -sha256 -r "$cloudflared.tmp" | cut -d' ' -f1)" != "$cf_sha256" ]]; then
            echo "cloudflared doesn't match its published checksum, not using it" >&2
            exit 1
        fi
        mv "$cloudflared.tmp" "$cloudflared"
    fi

    echo "==> pushing to $REMOTE_DIR"
    adb shell "mkdir -p $REMOTE_DIR/frontend"
    adb push -q build/dryad "$REMOTE_DIR/dryad.new"
    adb push -q deploy/keep-running.sh "$LOOP.new"
    if [[ -n "$new_packages" ]]; then
        adb shell "rm -rf $REMOTE_DIR/pydeps"
    fi
    adb push -q --sync build/pydeps "$REMOTE_DIR/"
    adb push -q --sync frontend/app.py frontend/serve.py frontend/templates frontend/static \
        "$REMOTE_DIR/frontend/"
    # cloudflared is 38 MB, so it's only pushed when the board has another version.
    if [[ "$(adb shell "$REMOTE_DIR/cloudflared --version 2>/dev/null")" != *"$cf_version"* ]]; then
        adb push -q "$cloudflared" "$REMOTE_DIR/cloudflared.new"
        adb shell "chmod +x $REMOTE_DIR/cloudflared.new &&
            mv $REMOTE_DIR/cloudflared.new $REMOTE_DIR/cloudflared"
    fi
    # personalities.json holds the plants' memories on the board: never overwrite it.
    adb shell "[ -e $REMOTE_DIR/personalities.json ]" ||
        adb push -q server/personalities.json "$REMOTE_DIR/"
    # .env holds the keys (Solana treasury, Tiger Data, Cloudflare tunnel):
    # only readable by its owner.
    if [[ -f server/.env ]]; then
        adb push -q server/.env "$REMOTE_DIR/.env"
        adb shell "chmod 600 $REMOTE_DIR/.env"
    fi
    # Rename over the running files (safe on Linux); restart() runs the new ones.
    adb shell "chmod +x $REMOTE_DIR/dryad.new $LOOP.new &&
        mv $REMOTE_DIR/dryad.new $REMOTE_DIR/dryad && mv $LOOP.new $LOOP"

    restart
}

# fetch_npm <package> <version> <sha512 integrity> <file in package>=<path under frontend/static>...
# downloads the package's tarball (unless all the files are already there),
# checks it against the integrity hash npm publishes, and copies the files out.
fetch_npm() {
    local pkg=$1 version=$2 integrity=$3 file missing='' tgz
    shift 3
    for file; do [[ -f frontend/static/${file#*=} ]] || missing=1; done
    [[ -n "$missing" ]] || return 0

    echo "==> downloading $pkg $version from npm"
    tgz=build/npm/${pkg##*/}-$version.tgz
    mkdir -p build/npm
    curl -fsSL -o "$tgz" "https://registry.npmjs.org/$pkg/-/${pkg##*/}-$version.tgz"
    if [[ "sha512-$(openssl dgst -sha512 -binary "$tgz" | openssl base64 -A)" != "$integrity" ]]; then
        echo "$tgz doesn't match npm's checksum, not using it" >&2
        exit 1
    fi
    for file; do
        mkdir -p "$(dirname "frontend/static/${file#*=}")"
        tar -xzOf "$tgz" "package/${file%%=*}" > "frontend/static/${file#*=}"
    done
}

# restart: start keep-running.sh afresh and add it to the arduino user's crontab
# for boot (no sudo needed). cloudflared is killed outright because a normal stop
# waits up to 30 s for open requests.
restart() {
    local old_server old_web
    old_server=$(pid "$SERVER_CMD")
    old_web=$(pid "$WEB_CMD")
    adb shell "(crontab -l 2>/dev/null | grep -v keep-running.sh; echo '@reboot $LOOP') | crontab -"
    adb shell "pkill -f '^/bin/sh $LOOP'; pkill -f '$SERVER_CMD'; pkill -f '$WEB_CMD';
        pkill -KILL -f '$TUNNEL_CMD'; (setsid $LOOP </dev/null >/dev/null 2>&1 &)"
    wait_healthy server "$SERVER_CMD" "$old_server" 8080
    wait_healthy website "$WEB_CMD" "$old_web" 5000
    local ip
    ip=$(board_ip)
    echo "==> website: http://$ip:5000"
    echo "==> API:     http://$ip:8080/api/plants"
    if grep -q '^CLOUDFLARE_TUNNEL_TOKEN=.' server/.env 2>/dev/null; then
        wait_tunnel
    else
        echo "==> no CLOUDFLARE_TUNNEL_TOKEN in server/.env, so the website isn't on your domain"
    fi
}

wait_tunnel() {
    echo "==> waiting for the tunnel to connect"
    for _ in $(seq 40); do
        if adb shell "curl -fsS -m 2 localhost:20241/ready" >/dev/null 2>&1; then
            echo "==> tunnel connected: the website is on your domain"
            return
        fi
        sleep 0.5
    done
    echo "the tunnel didn't connect, see ./deploy.sh logs" >&2
    exit 1
}

# stop: stop keep-running.sh and what it runs, and take it out of the
# crontab, so nothing comes back until the next ./deploy.sh.
stop() {
    adb shell "crontab -l 2>/dev/null | grep -v keep-running.sh | crontab -;
        pkill -f '^/bin/sh $LOOP'; pkill -f '$SERVER_CMD'; pkill -f '$WEB_CMD';
        pkill -KILL -f '$TUNNEL_CMD'; true"
    echo "==> stopped; './deploy.sh' starts it again"
}

# pid <pattern>: the matching process ids on the board (adb ends lines with \r).
pid() { adb shell "pgrep -f '$1' || true" | tr -d '\r'; }

# wait_healthy <name> <command> <old pid> <port>: wait until a new process
# answers /healthz on the port.
wait_healthy() {
    local pid health
    echo "==> waiting for the $1 to start"
    for _ in $(seq 30); do
        pid=$(pid "$2")
        if [[ -n "$pid" && "$pid" != "$3" ]] &&
            health=$(adb shell "curl -fsS -m 2 localhost:$4/healthz" 2>/dev/null); then
            echo "==> $1 running: $health"
            return
        fi
        sleep 0.5
    done
    echo "the $1 didn't start, see ./deploy.sh logs" >&2
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

watch() {
    if ! adb shell "curl -fsS -m 2 localhost:8080/healthz" >/dev/null 2>&1; then
        echo "The server isn't running on the board, so there's nothing to watch." >&2
        echo "Start it with './deploy.sh' (after that it starts on every boot)." >&2
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

case "${1:-deploy}" in
    deploy)   deploy ;;
    firmware) firmware ;;
    all)      firmware; deploy ;;
    stop)     stop ;;
    logs)     exec adb shell -t "cd $REMOTE_DIR && tail -n 50 -F dryad.log web.log tunnel.log 2>/dev/null" ;;
    watch)    watch ;;
    *) sed -n '2,15p' "$0"; exit 1 ;;
esac
