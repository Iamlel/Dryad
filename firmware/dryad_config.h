/* Firmware settings: wiring, calibration and timing, so tuning for a new room
 * doesn't touch firmware.ino. */
#ifndef DRYAD_CONFIG_H
#define DRYAD_CONFIG_H

/* Wiring: a Grove Base Shield with its switch at 3.3 V. */
#define DRYAD_PIN_LIGHT        A0
#define DRYAD_PIN_MOISTURE     A1

/* Any "I2C" port on the shield. Used for temperature. */
#define DRYAD_BARO_WIRE        Wire

/* Calibration in raw ADC counts, measured with ./baseline.sh. Readings map
 * linearly onto 0-100 % between these points and are clamped. */
#define DRYAD_LIGHT_RAW_DARK      16    /* sensor covered by hand   -> 0 %   */
#define DRYAD_LIGHT_RAW_BRIGHT    4095  /* normal room light        -> 100 % */
/* One-point calibration: soil at ~50 % moisture read 1481, and a dry probe
 * reads ~0, so 50 % lands at 1481. Re-measure the extremes for more accuracy. */
#define DRYAD_MOISTURE_RAW_DRY    0     /* dry soil                 -> 0 %   */
#define DRYAD_MOISTURE_RAW_WET    4000  /* saturated soil           -> 100 % */

/* Each report averages REPORT/SAMPLE samples (10 by default) to smooth out noise. */
#define DRYAD_ADC_BITS             12
#define DRYAD_SAMPLE_PERIOD_MS     100u
#define DRYAD_REPORT_PERIOD_MS     1000u
#define DRYAD_BARO_RETRY_PERIOD_MS 5000u /* re-probe a missing barometer */

/* Reports go to Linux as a RouterBridge notification with positional params
 *   [version, seq, uptime_ms, light, moisture, temperature_c]
 * light and moisture are fractions (0.0-1.0); a channel without samples is NaN.
 * Bump DRYAD_PROTOCOL_VERSION when the params change, and update the server. */
#define DRYAD_RPC_METHOD        "dryad.sample"
#define DRYAD_PROTOCOL_VERSION  2

/* Print each report as a readable line on the monitor. */
#define DRYAD_PRINT_READINGS 1
/* Send each report to the Go server (server/sensors/bridge.go). */
#define DRYAD_SEND_TO_LINUX  1

#endif /* DRYAD_CONFIG_H */
