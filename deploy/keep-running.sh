#!/bin/sh
# Keeps Groot running on the board: the Go server (sensors + API, port 8080),
# the Flask website (port 5000) and, when .env has a CLOUDFLARE_TUNNEL_TOKEN,
# the Cloudflare tunnel that puts the website on your domain. Each one is
# restarted 2 s after it exits. Started at boot from the arduino user's
# crontab, so no sudo is needed; set up and restarted by deploy.sh.
cd "$(dirname "$0")" || exit 1

# The website reads the API on this board and serves every network
# interface, so phones on the same WiFi can open it.
while true; do
    HOST=0.0.0.0 PORT=5000 BACKEND_URL=http://127.0.0.1:8080 PYTHONPATH="$PWD/pydeps" \
        python3 frontend/serve.py
    sleep 2
done >> web.log 2>&1 &

# The tunnel connects out to Cloudflare, which sends your domain's visitors
# back through it to the website. The token goes in the environment, not on
# the command line, where anyone on the board could read it with ps.
token=$(sed -n 's/^CLOUDFLARE_TUNNEL_TOKEN=//p' .env 2>/dev/null | tr -d "\"'\r")
if [ -n "$token" ]; then
    while true; do
        TUNNEL_TOKEN=$token ./cloudflared tunnel --no-autoupdate --metrics 127.0.0.1:20241 run
        sleep 2
    done >> tunnel.log 2>&1 &
fi

while true; do
    ./groot -headless
    sleep 2
done >> groot.log 2>&1
