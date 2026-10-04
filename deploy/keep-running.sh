#!/bin/sh
# Keeps the Groot server running on the board: restarts it 2 s after it
# exits, which is also how deploy.sh swaps in a new build (it kills the old
# process). Started at boot from the arduino user's crontab, so no sudo is
# needed; set up by `./deploy.sh install`.
cd "$(dirname "$0")" || exit 1
while true; do
    ./groot -headless
    sleep 2
done >> groot.log 2>&1
