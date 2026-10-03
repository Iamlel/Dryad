/*
 * Groot MCU firmware configuration.
 *
 * Everything that depends on wiring, calibration or timing lives here, so
 * tuning for a new room never means touching logic in firmware.ino.
 */
#ifndef GROOT_CONFIG_H
#define GROOT_CONFIG_H

/* ---- Wiring (Grove Base Shield, switch at 3.3 V) ---------------------- */
#define GROOT_PIN_LIGHT        A0
#define GROOT_PIN_MOISTURE     A1

/* Any "I2C" port on the shield. Used for temperature. */
#define GROOT_BARO_WIRE        Wire

/* ---- Calibration (raw ADC counts, measured with ./baseline.sh) -------- *
 * Readings are mapped linearly onto 0 - 100 % between these points and
 * clamped, so anything darker/drier than the low point reads 0 %. */
#define GROOT_LIGHT_RAW_DARK      16    /* sensor covered by hand   -> 0 %   */
#define GROOT_LIGHT_RAW_BRIGHT    4095  /* normal room light        -> 100 % */
/* One-point calibration: soil at ~50 % moisture read 1481, and a dry probe
 * reads ~0, so 50 % lands at 1481. Re-measure the extremes for more accuracy. */
#define GROOT_MOISTURE_RAW_DRY    0     /* dry soil                 -> 0 %   */
#define GROOT_MOISTURE_RAW_WET    4000  /* saturated soil           -> 100 % */

/* ---- Sampling --------------------------------------------------------- */
/* Readings are oversampled and averaged, so each report is a smooth mean
 * of REPORT/SAMPLE samples (10 by default) rather than one noisy value. */
#define GROOT_ADC_BITS             12
#define GROOT_SAMPLE_PERIOD_MS     100u
#define GROOT_REPORT_PERIOD_MS     1000u
#define GROOT_BARO_RETRY_PERIOD_MS 5000u /* re-probe a missing barometer */

/* ---- MCU -> Linux protocol -------------------------------------------- *
 * Sent as a RouterBridge notification with positional params:
 *   [version, seq, uptime_ms, light, moisture, temperature_c]
 * light and moisture are calibrated fractions (0.0 - 1.0).
 * A channel with no valid samples in the window is sent as NaN.
 * Bump GROOT_PROTOCOL_VERSION whenever the param list changes, and keep
 * the Linux-side receiver in sync. */
#define GROOT_RPC_METHOD        "groot.sample"
#define GROOT_PROTOCOL_VERSION  2

/* ---- Outputs ---------------------------------------------------------- */
/* Print each report as a readable line on the monitor (sensor bring-up). */
#define GROOT_PRINT_READINGS 1
/* Send each report to the Linux side over RouterBridge, where the Go
 * server (server/bridge.go) registers GROOT_RPC_METHOD to receive it. */
#define GROOT_SEND_TO_LINUX  1

#endif /* GROOT_CONFIG_H */
