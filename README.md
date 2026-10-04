# Dryad
Plant health detector on an Arduino UNO Q. The MCU reads light, soil moisture
and temperature (`firmware/`) and sends a report every second over the
RouterBridge to the Go server (`server/`), which runs on the board's Linux side
and serves it over WiFi. The Flask website (`frontend/`) runs next to it on
the board and shows it.

## Running it on the board

Needs `go`, `python3` with pip, `arduino-cli` and `adb` on your machine, and
the board on adb (USB or WiFi).

```bash
./deploy.sh all        # flash the MCU, then build, push and start the server and website
./deploy.sh            # after changing server or website code: rebuild, push, restart
./deploy.sh stop       # stop both; they stay off, even after a reboot, until ./deploy.sh
./deploy.sh watch      # live sensor readings
./deploy.sh logs       # server and website logs
```

After a deploy, everything runs off the board and starts again on every boot
(no password needed): the website on port 5000 and the API on port 8080. The
board's Python has no pip, so the first deploy installs the website's packages
on your machine (built for the board) and pushes them. It also downloads the
page's QR decoder and fonts from npm into `frontend/static/` (not in git).

## API (port 8080)

Read-only, apart from `POST /api/caretaker`. Percentages are 0-100 and temperatures °C. A value is `null` when
that sensor has no reading. Errors are JSON, `{"error": "..."}`, with a
matching status code.

| Endpoint | What it does |
|---|---|
| `GET /api/plants` | Every plant, with its thresholds. |
| `GET /api/plant/{id}` | For the UI: `moisture_pct`, `temperature_c`, `light_pct`, a `status` (`ok`, `thirsty`, `overwatered`, `cold`, `hot`, `dark` or `offline`) judged against that plant's thresholds, and a `dialog` line. `GET /api/plant` uses default houseplant thresholds. |
| `GET /api/sensors` | The latest raw reading. |
| `POST /api/caretaker` | Sets whose wallet gets the watering rewards: `{"wallet": "<Solana address>", "plant_id": "fern"}`. Without `plant_id`, the default thresholds judge the watering. It's kept in memory, so send it again after the server restarts. |
| `POST /api/talk/{id}` | A voice recording for the plant (the body, as the browser recorded it). Queues it and answers `202 {"job": "..."}`; `503` when 20 are already waiting. |
| `GET /api/talk/jobs/{job}` | `{"status": "queued", "position": 2}` (recordings ahead of it), `{"status": "working"}`, `{"status": "done", "heard": "...", "answer": "...", "voice": true}` or `{"status": "failed", "error": "..."}`. Answers are kept for 5 minutes. |
| `GET /api/talk/jobs/{job}/voice` | The answer in the plant's voice, as MP3. |
| `GET /api/caretaker` | The caretaker's `wallet` and `plant_id`, and the `payouts` sent so far (newest first), each with an `explorer_url`. |
| `GET /healthz` | Liveness and the deployed version. |

Plants live in Tiger Data. Set it up once:

1. Put `TIGER_DATABASE_URL=<connection string>` in `server/.env` and run `./deploy.sh`.
2. Create and fill the table: `psql "$TIGER_DATABASE_URL" -f server/plants.sql`
   (or paste the file into the Tiger console's SQL editor). To add plants or
   change their ranges, edit the file and run it again.

Until then the plant endpoints answer 503 and say what's missing;
`GET /api/plant` and the sensors still work.

## Caretaker rewards

When the caretaker waters the plant, taking its moisture from below the
plant's minimum to at least 5 points above it (without overwatering it), the
server sends their wallet 0.01 SOL, at most once a minute. It's always Solana
devnet: test SOL, no real money. Set it up once:

1. Make a new wallet just for paying out (a new account in Phantom is fine)
   and export its private key. Don't use a wallet that holds real SOL: its
   key sits in a file on the board.
2. Put `SOLANA_TREASURY_KEY=<that private key>` in `server/.env` and run
   `./deploy.sh`. `./deploy.sh logs` then shows the treasury's address.
3. Send that address some devnet SOL from https://faucet.solana.com (signing
   in with GitHub raises the limit). 1 SOL pays about 100 rewards.

Until then the caretaker endpoints answer 503. To watch rewards arrive in
Phantom, turn on Settings → Developer Settings → Testnet Mode (Solana Devnet).

## Talking to the plants

On the website, tap 🎙 under "How is my plant?", speak, tap again. The board
answers everyone's recordings in turn: it turns each into text (ElevenLabs),
the plant answers in its personality from `personalities.json`, knowing its
live readings (Gemini), and the answer is turned into the plant's voice
(ElevenLabs). The page asks for its answer every second (giving up after 90
seconds), then shows it and plays it on the phone or laptop that recorded it.
Needs `GEMINI_API_KEY` and `ELEVENLABS_API_KEY` in `server/.env`, and the site
on HTTPS (your domain) for the browser to allow the microphone.

## Your domain (Cloudflare Tunnel)

The board runs a Cloudflare tunnel, so phones anywhere can open the website on
your domain over HTTPS. The tunnel connects out from the board, so it needs no
IP or open ports, and keeps working when the board's address changes. Set it
up once:

1. In the Cloudflare dashboard (your domain has to be on Cloudflare), go to
   Networking → Tunnels → Create a tunnel, name it and create it. Copy the
   token out of the install command it shows.
2. In the tunnel, Routes → Add route → Published application: pick a
   subdomain and your domain, and set the Service URL to `http://localhost:5000`.
3. Put `CLOUDFLARE_TUNNEL_TOKEN=<token>` in `server/.env` and run `./deploy.sh`,
   which waits until the tunnel is connected.

The server code is in `server/`: `bridge.go` (Arduino connection),
`sensors.go`, `plants.go` (plants, status, Tiger Data), `rewards.go`
(Solana caretaker rewards) and `httpapi.go`.
