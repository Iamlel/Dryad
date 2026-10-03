#!/usr/bin/env bash
# Record a sensor for a few seconds and print its average (plus min/max).
# Reads the lines the firmware already prints, so no reflash is needed.
#
# Usage: ./baseline.sh <light|moisture|temp|all> [seconds]
# e.g.   ./baseline.sh light        # 3 s average of the light sensor
#        ./baseline.sh moisture 10  # 10 s average of soil moisture
set -euo pipefail

SECONDS_TO_RECORD=${2:-3}

# Grab the monitor output for N seconds into a temp file, print its path.
record() {
    local out
    out=$(mktemp)
    adb forward tcp:7500 tcp:7500 >/dev/null
    # sleep keeps nc's stdin open (EOF would end the session early).
    sleep $((SECONDS_TO_RECORD + 1)) | nc 127.0.0.1 7500 >"$out" &
    local nc_pid=$!
    sleep "$SECONDS_TO_RECORD"
    kill "$nc_pid" 2>/dev/null || true
    echo "$out"
}

# summarize <name> <column> <unit>  (reads the recording in $DATA)
# Column is the |-separated field in the firmware's output line:
#   1 light | 2 moisture | 3 temp
summarize() {
    local name=$1 column=$2 unit=$3
    awk -F'|' -v col="$column" -v name="$name" -v unit="$unit" -v secs="$SECONDS_TO_RECORD" '
        /^#[0-9]/ {
            field = $col
            gsub(/\r/, "", field)
            if (!match(field, /-?[0-9]+\.[0-9]+/)) next        # "--" = no reading
            v = substr(field, RSTART, RLENGTH) + 0
            sum += v; n++
            if (n == 1 || v < min) min = v
            if (n == 1 || v > max) max = v
            if (match(field, /\( *[0-9]+\)/)) {                 # raw ADC counts
                raw = substr(field, RSTART, RLENGTH); gsub(/[^0-9]/, "", raw)
                raw_sum += raw; raw_n++
            }
        }
        END {
            if (n == 0) { printf "%-9s no readings (sensor missing or board not running?)\n", name; exit 0 }
            printf "%-9s avg %8.2f%s", name, sum / n, unit
            if (raw_n) printf "  (raw %4.0f)", raw_sum / raw_n
            printf "   min %.2f%s  max %.2f%s   [%d readings, %ss]\n", min, unit, max, unit, n, secs
        }' "$DATA"
}

light()    { summarize light    1 "%"; }
moisture() { summarize moisture 2 "%"; }
temp()     { summarize temp     3 " C"; }
all()      { light; moisture; temp; }

case "${1:-}" in
    light|moisture|temp|all) ;;
    *) echo "usage: $0 <light|moisture|temp|all> [seconds]" >&2; exit 1 ;;
esac

echo "recording $1 for ${SECONDS_TO_RECORD}s..." >&2
DATA=$(record)
trap 'rm -f "$DATA"' EXIT
"$1"
