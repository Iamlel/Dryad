/*
 * Groot MCU firmware (STM32U585 side of the Arduino UNO Q).
 *
 * Every GROOT_SAMPLE_PERIOD_MS: read all sensors and add them to running means.
 * Every GROOT_REPORT_PERIOD_MS: calibrate the means to 0-100 % and print /
 * send them (see "Outputs" in groot_config.h).
 *
 * The MCU has no network access of its own; WiFi lives on the Linux side.
 */
#include <Arduino.h>
#include <Arduino_RouterBridge.h>
#include <Wire.h>
#include <math.h>
#include <stdio.h>

#include "spa06.h"
#include "groot_config.h"

/* ---- Running means ---------------------------------------------------- */

typedef enum {
    CH_LIGHT,       /* raw ADC counts */
    CH_MOISTURE,    /* raw ADC counts */
    CH_TEMPERATURE, /* degrees C */
    CH_COUNT
} channel_t;

typedef struct {
    float    sum;
    uint32_t count;
} mean_t;

static void mean_add(mean_t *m, float value)
{
    m->sum += value;
    m->count++;
}

/* Returns the mean and resets the accumulator; NaN if there were no samples. */
static float mean_take(mean_t *m)
{
    float result = m->count ? m->sum / (float)m->count : NAN;
    m->sum   = 0.0f;
    m->count = 0;
    return result;
}

/* ---- State ------------------------------------------------------------ */

typedef struct {
    pin_size_t pin;
    channel_t  channel;
} analog_input_t;

static const analog_input_t k_analog_inputs[] = {
    { GROOT_PIN_LIGHT,    CH_LIGHT },
    { GROOT_PIN_MOISTURE, CH_MOISTURE },
};

#define ARRAY_LEN(a) (sizeof(a) / sizeof((a)[0]))

static mean_t   g_means[CH_COUNT];
static spa06_t  g_baro;
static bool     g_baro_ok;
static uint32_t g_seq;
static uint32_t g_last_report_ms;
static uint32_t g_last_baro_probe_ms;

/* ---- I2C glue for the SPA06 driver ------------------------------------ */

static int i2c_read(void *ctx, uint8_t addr, uint8_t reg, uint8_t *buf, size_t len)
{
    (void)ctx;
    GROOT_BARO_WIRE.beginTransmission(addr);
    GROOT_BARO_WIRE.write(reg);
    if (GROOT_BARO_WIRE.endTransmission() != 0)
        return -1;
    if (GROOT_BARO_WIRE.requestFrom(addr, len) != len)
        return -1;
    for (size_t i = 0; i < len; i++)
        buf[i] = (uint8_t)GROOT_BARO_WIRE.read();
    return 0;
}

static int i2c_write(void *ctx, uint8_t addr, uint8_t reg, uint8_t value)
{
    (void)ctx;
    GROOT_BARO_WIRE.beginTransmission(addr);
    GROOT_BARO_WIRE.write(reg);
    GROOT_BARO_WIRE.write(value);
    return GROOT_BARO_WIRE.endTransmission() == 0 ? 0 : -1;
}

/* ---- Sensors ---------------------------------------------------------- */

static const char *spa06_status_str(spa06_status_t st)
{
    switch (st) {
    case SPA06_OK:            return "ok";
    case SPA06_ERR_BUS:       return "no reply";
    case SPA06_ERR_NOT_FOUND: return "wrong chip ID";
    case SPA06_ERR_NOT_READY: return "still booting";
    case SPA06_ERR_STOPPED:   return "stopped";
    }
    return "?";
}

static bool baro_probe(void)
{
    static const uint8_t addrs[] = { SPA06_ADDR_PRIMARY, SPA06_ADDR_SECONDARY };
    spa06_status_t       st[ARRAY_LEN(addrs)];
    char                 line[96];

    for (size_t i = 0; i < ARRAY_LEN(addrs); i++) {
        st[i] = spa06_init(&g_baro, addrs[i], i2c_read, i2c_write, NULL);
        if (st[i] == SPA06_OK) {
            snprintf(line, sizeof line, "barometer: SPA06-003 found at 0x%02X", addrs[i]);
            Serial.println(line);
            return true;
        }
    }
    snprintf(line, sizeof line, "barometer: not found (0x%02X: %s, 0x%02X: %s), retrying in 5 s",
             addrs[0], spa06_status_str(st[0]), addrs[1], spa06_status_str(st[1]));
    Serial.println(line);
    return false;
}

static void sample_analog(void)
{
    for (size_t i = 0; i < ARRAY_LEN(k_analog_inputs); i++) {
        int raw = analogRead(k_analog_inputs[i].pin);
        if (raw < 0) /* Zephyr core returns -errno on ADC failure */
            continue;
        mean_add(&g_means[k_analog_inputs[i].channel], (float)raw);
    }
}

static void sample_baro(uint32_t now_ms)
{
    spa06_sample_t s;

    if (!g_baro_ok) {
        if (now_ms - g_last_baro_probe_ms < GROOT_BARO_RETRY_PERIOD_MS)
            return;
        g_last_baro_probe_ms = now_ms;
        g_baro_ok = baro_probe();
        return; /* first conversion is not ready yet anyway */
    }

    switch (spa06_read(&g_baro, &s)) {
    case SPA06_OK:
        mean_add(&g_means[CH_TEMPERATURE], s.temperature_c);
        break;
    case SPA06_ERR_NOT_READY:
        break;
    case SPA06_ERR_STOPPED: /* sensor power-cycled: set it up again right away */
        Serial.println("barometer: sensor restarted, reconfiguring");
        g_baro_ok = baro_probe();
        g_last_baro_probe_ms = now_ms;
        break;
    default: /* bus error: sensor unplugged or glitched, re-probe later */
        Serial.println("barometer: read failed, re-probing");
        g_baro_ok = false;
        g_last_baro_probe_ms = now_ms;
        break;
    }
}

/* ---- Calibration ------------------------------------------------------- */

/* Map a raw ADC count onto 0..1 between two measured points, clamped. */
static float calibrate(float raw, float raw_low, float raw_high)
{
    float fraction;

    if (isnan(raw))
        return NAN;
    fraction = (raw - raw_low) / (raw_high - raw_low);
    if (fraction < 0.0f) return 0.0f;
    if (fraction > 1.0f) return 1.0f;
    return fraction;
}

/* ---- Reporting -------------------------------------------------------- */

/*
 * Format `value` with `decimals` (1-3) fractional digits, or "--" for NaN.
 * Done with integers because %f support in the Zephyr libc is a build option
 * that may be off.
 */
static void fmt_fixed(char *out, size_t cap, float value, int decimals)
{
    static const long k_scale[] = { 1, 10, 100, 1000 };
    const long  scale     = k_scale[decimals];
    const float magnitude = value < 0.0f ? -value : value;
    long        scaled;

    if (isnan(value)) {
        snprintf(out, cap, "--");
        return;
    }
    scaled = (long)(magnitude * (float)scale + 0.5f);
    snprintf(out, cap, "%s%ld.%0*ld", value < 0.0f ? "-" : "",
             scaled / scale, decimals, scaled % scale);
}

/* " 63.4% (2597)": calibrated percent, then raw ADC counts. */
static void fmt_sensor(char *out, size_t cap, float fraction, float raw)
{
    char pct[24];

    if (isnan(fraction)) {
        snprintf(out, cap, "%-13s", "--"); /* same width as a real value */
        return;
    }
    fmt_fixed(pct, sizeof pct, fraction * 100.0f, 1);
    snprintf(out, cap, "%5s%% (%4ld)", pct, (long)(raw + 0.5f));
}

static void print_readings(uint32_t seq, float light, float light_raw, float moisture,
                           float moisture_raw, float temperature_c)
{
    char light_s[64], moisture_s[64], temp_s[24];
    char line[192];

    fmt_sensor(light_s, sizeof light_s, light, light_raw);
    fmt_sensor(moisture_s, sizeof moisture_s, moisture, moisture_raw);
    fmt_fixed(temp_s, sizeof temp_s, temperature_c, 2);

    snprintf(line, sizeof line, "#%-5lu light %s | moisture %s | temp %6s C",
             (unsigned long)seq, light_s, moisture_s, temp_s);
    Serial.println(line);
}

static void report(uint32_t now_ms)
{
    const float light_raw    = mean_take(&g_means[CH_LIGHT]);
    const float moisture_raw = mean_take(&g_means[CH_MOISTURE]);

    /* Fixed-width locals pin down the MessagePack encoding of each param. */
    const int32_t  version       = GROOT_PROTOCOL_VERSION;
    const uint32_t seq           = g_seq++;
    const uint32_t uptime_ms     = now_ms;
    const float    light         = calibrate(light_raw, GROOT_LIGHT_RAW_DARK,
                                             GROOT_LIGHT_RAW_BRIGHT);
    const float    moisture      = calibrate(moisture_raw, GROOT_MOISTURE_RAW_DRY,
                                             GROOT_MOISTURE_RAW_WET);
    const float    temperature_c = mean_take(&g_means[CH_TEMPERATURE]);

#if GROOT_PRINT_READINGS
    print_readings(seq, light, light_raw, moisture, moisture_raw, temperature_c);
#endif
#if GROOT_SEND_TO_LINUX
    /* Fire-and-forget: a notification never blocks waiting on Linux. */
    Bridge.notify(GROOT_RPC_METHOD, version, seq, uptime_ms, light, moisture, temperature_c);
#else
    (void)version;
    (void)uptime_ms;
#endif
}

/* ---- Arduino entry points --------------------------------------------- */

void setup(void)
{
    Bridge.begin();
    Serial.begin(); /* on the UNO Q, Serial is the RouterBridge monitor */
    Serial.println("Groot: light=A0 moisture=A1 temperature=I2C");
    Serial.println("values: calibrated % (raw ADC counts)");

    analogReadResolution(GROOT_ADC_BITS);
    GROOT_BARO_WIRE.begin();

    g_baro_ok = baro_probe();
    g_last_baro_probe_ms = (uint32_t)millis();
    g_last_report_ms     = (uint32_t)millis();
}

void loop(void)
{
    const uint32_t start_ms = (uint32_t)millis();

    sample_analog();
    sample_baro(start_ms);

    /* Unsigned subtraction stays correct when millis() wraps (~49 days). */
    if (start_ms - g_last_report_ms >= GROOT_REPORT_PERIOD_MS) {
        g_last_report_ms += GROOT_REPORT_PERIOD_MS;
        report(start_ms);
    }

    /* Sleep for the rest of the period. delay() also yields the CPU to the
     * RouterBridge thread, so never replace this with a busy-wait. */
    const uint32_t elapsed_ms = (uint32_t)millis() - start_ms;
    if (elapsed_ms < GROOT_SAMPLE_PERIOD_MS)
        delay(GROOT_SAMPLE_PERIOD_MS - elapsed_ms);
}
