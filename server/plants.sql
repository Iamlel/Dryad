-- The plants the Groot server reads. Run it once against Tiger Data:
--
--   psql "$TIGER_DATABASE_URL" -f server/plants.sql
--
-- or paste it into the SQL editor in the Tiger console. To add a plant or
-- change its ranges, edit the rows below and run it again.

CREATE TABLE IF NOT EXISTS plants (
    id               text PRIMARY KEY,           -- used in the URL: /api/plant/fern
    name             text NOT NULL,
    moisture_min_pct double precision NOT NULL,  -- below: thirsty
    moisture_max_pct double precision NOT NULL,  -- above: overwatered
    temp_min_c       double precision NOT NULL,  -- below: cold
    temp_max_c       double precision NOT NULL,  -- above: hot
    light_min_pct    double precision NOT NULL,  -- below: dark
    created_at       timestamptz NOT NULL DEFAULT now()
);

INSERT INTO plants (id, name, moisture_min_pct, moisture_max_pct, temp_min_c, temp_max_c, light_min_pct)
VALUES
    ('fern',  'Fern',  50, 90, 16, 27, 15),
    ('spike', 'Spike',  5, 40, 10, 35, 40)
ON CONFLICT (id) DO UPDATE SET
    name             = EXCLUDED.name,
    moisture_min_pct = EXCLUDED.moisture_min_pct,
    moisture_max_pct = EXCLUDED.moisture_max_pct,
    temp_min_c       = EXCLUDED.temp_min_c,
    temp_max_c       = EXCLUDED.temp_max_c,
    light_min_pct    = EXCLUDED.light_min_pct;
