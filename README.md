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
./deploy.sh install    # once: start the server on every boot (asks for the board's password)
./deploy.sh            # after changing server code: rebuild, push, restart
./deploy.sh watch      # live sensor readings
./deploy.sh logs       # server log
```

After `install`, everything runs off the board. It needs nothing but power.

## Sensor API (port 8080)

- `GET /api/sensors`: latest reading. `light` and `moisture` are 0.0-1.0,
  `temperature_c` is in °C, `null` means no reading, and `stale` is true if the
  MCU stopped reporting.
- `POST /api/sensors`: push a reading as JSON (same fields), e.g. to test
  without hardware.

In Go, use `Sensors.Latest()` or `Sensors.Subscribe()` (`server/sensors.go`),
and add routes to `HTTPMux` (`server/httpapi.go`).
