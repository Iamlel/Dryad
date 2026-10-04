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

Read-only. Percentages are 0-100 and temperatures °C. A value is `null` when
that sensor has no reading. Errors are JSON, `{"error": "..."}`, with a
matching status code.

| Endpoint | What it does |
|---|---|
| `GET /api/plants` | Every plant, with its thresholds. |
| `GET /api/plant/{id}` | For the UI: `moisture_pct`, `temperature_c`, `light_pct`, a `status` (`ok`, `thirsty`, `overwatered`, `cold`, `hot`, `dark` or `offline`) judged against that plant's thresholds, and a `dialog` line. `GET /api/plant` uses default houseplant thresholds. |
| `GET /api/sensors` | The latest raw reading. |
| `GET /healthz` | Liveness and the deployed version. |

Plants live in Tiger Data. Set it up once:

1. Put `TIGER_DATABASE_URL=<connection string>` in `server/.env` and run `./deploy.sh`.
2. Create and fill the table: `psql "$TIGER_DATABASE_URL" -f server/plants.sql`
   (or paste the file into the Tiger console's SQL editor). To add plants or
   change their ranges, edit the file and run it again.

Until then the plant endpoints answer 503 and say what's missing;
`GET /api/plant` and the sensors still work.

The server code is in `server/`: `bridge.go` (Arduino connection),
`sensors.go`, `plants.go` (plants, status, Tiger Data) and `httpapi.go`.
