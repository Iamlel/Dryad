/*
 * Minimal Goertek SPA06-003 (temperature + pressure) driver.
 *
 * Plain C, no Arduino dependencies: the caller supplies I2C read/write
 * callbacks. Register map and compensation formula follow the SPA06-003
 * datasheet (same family as the Infineon DPS310 / Goertek SPL06).
 */
#ifndef GROOT_SPA06_H
#define GROOT_SPA06_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define SPA06_ADDR_PRIMARY   0x77 /* SDO high (Seeed Grove default) */
#define SPA06_ADDR_SECONDARY 0x76 /* SDO low */

typedef enum {
    SPA06_OK = 0,
    SPA06_ERR_BUS,       /* I2C transaction failed */
    SPA06_ERR_NOT_FOUND, /* no device, or chip ID is not SPA06-003 */
    SPA06_ERR_NOT_READY, /* still booting, or no new conversion since last read */
    SPA06_ERR_STOPPED,   /* sensor reset (e.g. replugged) and is idle: call spa06_init again */
} spa06_status_t;

/* Bus callbacks. Return 0 on success, non-zero on failure. */
typedef int (*spa06_read_fn)(void *ctx, uint8_t addr, uint8_t reg, uint8_t *buf, size_t len);
typedef int (*spa06_write_fn)(void *ctx, uint8_t addr, uint8_t reg, uint8_t value);

/* Factory calibration coefficients (sign-extended). */
typedef struct {
    int32_t c0, c1;                         /* temperature */
    int32_t c00, c10, c01, c11, c20, c21,   /* pressure */
            c30, c31, c40;
} spa06_calib_t;

typedef struct {
    spa06_read_fn  read;
    spa06_write_fn write;
    void          *bus_ctx;
    uint8_t        addr;
    spa06_calib_t  calib;
} spa06_t;

typedef struct {
    float temperature_c;
    float pressure_hpa;
} spa06_sample_t;

/*
 * Probe `addr`, load calibration and start continuous temperature + pressure
 * measurements at 8 Hz. Non-blocking: returns SPA06_ERR_NOT_READY if the
 * sensor is still booting, so the caller can simply retry later.
 */
spa06_status_t spa06_init(spa06_t *dev, uint8_t addr,
                          spa06_read_fn read, spa06_write_fn write, void *bus_ctx);

/* Read the latest conversion. Non-blocking. */
spa06_status_t spa06_read(const spa06_t *dev, spa06_sample_t *out);

/* Exposed for unit tests. */
void  spa06_parse_calib(spa06_calib_t *c, const uint8_t raw[21]);
void  spa06_compensate(const spa06_calib_t *c, int32_t raw_p, int32_t raw_t, spa06_sample_t *out);

#ifdef __cplusplus
}
#endif

#endif /* GROOT_SPA06_H */
