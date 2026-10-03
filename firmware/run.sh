#!/usr/bin/env bash
# Build + flash the firmware over USB, then stream the board's monitor output.
# Usage: ./run.sh            (build, upload, monitor)
#        ./run.sh monitor    (just watch output)
set -euo pipefail
cd "$(dirname "$0")"

FQBN=arduino:zephyr:unoq

if [[ "${1:-}" != "monitor" ]]; then
    PORT=$(arduino-cli board list | awk '/arduino:zephyr:unoq/ {print $1; exit}')
    [[ -n "$PORT" ]] || { echo "UNO Q not found - is the USB cable plugged in?" >&2; exit 1; }
    arduino-cli compile -b "$FQBN" --warnings all firmware
    arduino-cli upload  -b "$FQBN" -p "$PORT" firmware
fi

# The sketch's Serial output is served on 127.0.0.1:7500 on the board; tunnel it over USB.
adb forward tcp:7500 tcp:7500 >/dev/null
echo "--- monitor (Ctrl+C to quit) ---"
exec nc 127.0.0.1 7500
