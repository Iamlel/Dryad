#include "spa06.h"

/* Registers */
#define REG_PSR_B2   0x00 /* 6 bytes: pressure[23:0], temperature[23:0], MSB first */
#define REG_PRS_CFG  0x06
#define REG_TMP_CFG  0x07
#define REG_MEAS_CFG 0x08
#define REG_CFG_REG  0x09
#define REG_ID       0x0D
#define REG_COEF     0x10 /* 21 bytes */

#define CHIP_ID   0x11
#define COEF_LEN  21
#define DATA_LEN  6

/* MEAS_CFG bits */
#define COEF_RDY        (1u << 7)
#define SENSOR_RDY      (1u << 6)
#define TMP_RDY         (1u << 5)
#define PRS_RDY         (1u << 4)
#define MODE_IDLE       0x00
#define MODE_CONT_BOTH  0x07
#define MODE_MASK       0x07

/* PRS_CFG / TMP_CFG: rate[7:4] | oversampling[3:0] */
#define RATE_8HZ  (0x3 << 4)
#define OSR_1X    0x0
#define OSR_16X   0x4

/* CFG_REG: result bit-shift must be enabled when oversampling > 8x. */
#define P_SHIFT (1u << 2)

/* Scale factors for the chosen oversampling rates (datasheet table). */
#define KP_16X 253952.0f
#define KT_1X  524288.0f

static int32_t sign_extend(uint32_t value, unsigned bits)
{
    const uint32_t sign = 1u << (bits - 1);
    return (int32_t)((value ^ sign) - sign);
}

static uint32_t u24_be(const uint8_t *p)
{
    return ((uint32_t)p[0] << 16) | ((uint32_t)p[1] << 8) | p[2];
}

static int32_t s16_be(const uint8_t *p)
{
    return sign_extend(((uint32_t)p[0] << 8) | p[1], 16);
}

void spa06_parse_calib(spa06_calib_t *c, const uint8_t raw[COEF_LEN])
{
    c->c0  = sign_extend(((uint32_t)raw[0] << 4) | (raw[1] >> 4), 12);
    c->c1  = sign_extend(((uint32_t)(raw[1] & 0x0F) << 8) | raw[2], 12);
    c->c00 = sign_extend(((uint32_t)raw[3] << 12) | ((uint32_t)raw[4] << 4) | (raw[5] >> 4), 20);
    c->c10 = sign_extend(((uint32_t)(raw[5] & 0x0F) << 16) | ((uint32_t)raw[6] << 8) | raw[7], 20);
    c->c01 = s16_be(&raw[8]);
    c->c11 = s16_be(&raw[10]);
    c->c20 = s16_be(&raw[12]);
    c->c21 = s16_be(&raw[14]);
    c->c30 = s16_be(&raw[16]);
    c->c31 = sign_extend(((uint32_t)raw[18] << 4) | (raw[19] >> 4), 12);
    c->c40 = sign_extend(((uint32_t)(raw[19] & 0x0F) << 8) | raw[20], 12);
}

void spa06_compensate(const spa06_calib_t *c, int32_t raw_p, int32_t raw_t, spa06_sample_t *out)
{
    const float t  = (float)raw_t / KT_1X;
    const float p  = (float)raw_p / KP_16X;
    const float p2 = p * p;
    const float p3 = p2 * p;
    const float p4 = p3 * p;

    const float pa = (float)c->c00 + (float)c->c10 * p + (float)c->c20 * p2 +
                     (float)c->c30 * p3 + (float)c->c40 * p4 +
                     t * ((float)c->c01 + (float)c->c11 * p + (float)c->c21 * p2 +
                          (float)c->c31 * p3);

    out->temperature_c = (float)c->c0 * 0.5f + (float)c->c1 * t;
    out->pressure_hpa  = pa / 100.0f;
}

spa06_status_t spa06_init(spa06_t *dev, uint8_t addr,
                          spa06_read_fn read, spa06_write_fn write, void *bus_ctx)
{
    uint8_t id, status, raw[COEF_LEN];

    dev->read    = read;
    dev->write   = write;
    dev->bus_ctx = bus_ctx;
    dev->addr    = addr;

    if (read(bus_ctx, addr, REG_ID, &id, 1) != 0)
        return SPA06_ERR_BUS;
    if (id != CHIP_ID)
        return SPA06_ERR_NOT_FOUND;

    if (read(bus_ctx, addr, REG_MEAS_CFG, &status, 1) != 0)
        return SPA06_ERR_BUS;
    if ((status & (COEF_RDY | SENSOR_RDY)) != (COEF_RDY | SENSOR_RDY))
        return SPA06_ERR_NOT_READY;

    if (read(bus_ctx, addr, REG_COEF, raw, sizeof raw) != 0)
        return SPA06_ERR_BUS;
    spa06_parse_calib(&dev->calib, raw);

    /* Configure in idle: the sensor may still be running if only the MCU reset. */
    if (write(bus_ctx, addr, REG_MEAS_CFG, MODE_IDLE) != 0 ||
        write(bus_ctx, addr, REG_PRS_CFG, RATE_8HZ | OSR_16X) != 0 ||
        write(bus_ctx, addr, REG_TMP_CFG, RATE_8HZ | OSR_1X) != 0 ||
        write(bus_ctx, addr, REG_CFG_REG, P_SHIFT) != 0 ||
        write(bus_ctx, addr, REG_MEAS_CFG, MODE_CONT_BOTH) != 0)
        return SPA06_ERR_BUS;

    return SPA06_OK;
}

spa06_status_t spa06_read(const spa06_t *dev, spa06_sample_t *out)
{
    uint8_t status, raw[DATA_LEN];

    if (dev->read(dev->bus_ctx, dev->addr, REG_MEAS_CFG, &status, 1) != 0)
        return SPA06_ERR_BUS;
    /* A power blip resets the sensor to idle; it would never report data again. */
    if ((status & MODE_MASK) != MODE_CONT_BOTH)
        return SPA06_ERR_STOPPED;
    /* Pressure compensation needs a temperature result, so wait for both. */
    if ((status & (TMP_RDY | PRS_RDY)) != (TMP_RDY | PRS_RDY))
        return SPA06_ERR_NOT_READY;

    if (dev->read(dev->bus_ctx, dev->addr, REG_PSR_B2, raw, sizeof raw) != 0)
        return SPA06_ERR_BUS;

    spa06_compensate(&dev->calib, sign_extend(u24_be(&raw[0]), 24),
                     sign_extend(u24_be(&raw[3]), 24), out);
    return SPA06_OK;
}
