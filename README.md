# Dryad
Plant health detector on an Arduino UNO Q. The MCU reads light, soil moisture
and temperature (`firmware/`) and sends a report every second over the
RouterBridge to the Go server (`server/`), which runs on the board's Linux side
and serves it over WiFi.

## Running it on the board

Needs `go`, `arduino-cli` and `adb` on your machine, and the board on adb
(USB or WiFi).

```bash
./deploy.sh all        # flash the MCU + build/push the server
./deploy.sh install    # once: start the server now and on every boot (no password needed)
./deploy.sh            # after changing server code: rebuild, push, restart
./deploy.sh watch      # live sensor readings
./deploy.sh logs       # server log
```

After `install`, everything runs off the board. It needs nothing but power.

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

The server code is in `server/`: `bridge.go` (Arduino connection),
`sensors.go`, `plants.go` (plants, status, Tiger Data), `rewards.go`
(Solana caretaker rewards) and `httpapi.go`.
