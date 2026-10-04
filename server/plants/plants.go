// Package plants reads the plants' healthy ranges from Tiger Data (Postgres)
// and judges each plant's status. plants.sql adds the plants.
package plants

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"dryad/sensors"
)

// Plant is one plant profile. The fields are in the same order as the
// table's columns, because rows are read into it by position.
type Plant struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	MoistureMinPct float64   `json:"moisture_min_pct"` // below: thirsty
	MoistureMaxPct float64   `json:"moisture_max_pct"` // above: overwatered
	TempMinC       float64   `json:"temp_min_c"`       // below: cold
	TempMaxC       float64   `json:"temp_max_c"`       // above: hot
	LightMinPct    float64   `json:"light_min_pct"`    // below: dark
	CreatedAt      time.Time `json:"created_at"`
}

// DefaultPlant has typical houseplant thresholds, used by GET /api/plant.
var DefaultPlant = Plant{MoistureMinPct: 30, MoistureMaxPct: 85, TempMinC: 15, TempMaxC: 30, LightMinPct: 20}

// Status is the plant's most urgent need: water first, then temperature,
// then light. ok is false when there is no reading at all.
func (p Plant) Status(r sensors.Reading, ok bool) string {
	switch {
	case !ok || r.Stale():
		return "offline"
	case below(r.MoisturePct, p.MoistureMinPct):
		return "thirsty"
	case above(r.MoisturePct, p.MoistureMaxPct):
		return "overwatered"
	case below(r.TemperatureC, p.TempMinC):
		return "cold"
	case above(r.TemperatureC, p.TempMaxC):
		return "hot"
	case below(r.LightPct, p.LightMinPct):
		return "dark"
	}
	return "ok"
}

// A sensor without a reading (nil) never counts as a problem.
func below(v *float64, limit float64) bool { return v != nil && *v < limit }
func above(v *float64, limit float64) bool { return v != nil && *v > limit }

// Dialog is the line the plant says about its status on the website.
func Dialog(p Plant, status string) string {
	return map[string]string{
		"offline":     "I can't feel my roots right now... is my sensor board still plugged in?",
		"thirsty":     "I'm so thirsty! My soil is too dry. Could you give me some water, please?",
		"overwatered": "Glub glub... my roots are drowning! Please hold off on the water for a while.",
		"cold":        "Brrr, it's chilly in here! Could you move me somewhere warmer?",
		"hot":         "Phew, it's way too hot! I'd love some shade or a cooler spot.",
		"dark":        "It's so dark... I need more light!",
		"ok":          "I'm feeling great! Everything is just right.",
	}[status]
}

var (
	ErrPlantNotFound = errors.New("plant not found")
	ErrNoDatabase    = errors.New("no plant database: set TIGER_DATABASE_URL in server/.env")
	ErrNoTable       = errors.New("the plants table doesn't exist yet: run server/plants.sql on the database")
	ErrDBUnavailable = errors.New("the plant database is unavailable, try again shortly")
)

const plantColumns = `id, name, moisture_min_pct, moisture_max_pct, temp_min_c, temp_max_c, light_min_pct, created_at`

type PlantStore struct {
	db *pgxpool.Pool // nil when TIGER_DATABASE_URL isn't set
}

// OpenPlantStore connects on first use, so the server still starts (and serves
// readings) while the database is down.
func OpenPlantStore(url string) (*PlantStore, error) {
	if url == "" {
		log.Print("plants: TIGER_DATABASE_URL isn't set, so the plant endpoints are off")
		return &PlantStore{}, nil
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second // fail fast instead of leaving the UI hanging
	db, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	log.Print("plants: reading from Tiger Data")
	return &PlantStore{db: db}, nil
}

func (s *PlantStore) List(ctx context.Context) ([]Plant, error) {
	if s.db == nil {
		return nil, ErrNoDatabase
	}
	rows, _ := s.db.Query(ctx, `SELECT `+plantColumns+` FROM plants ORDER BY created_at, id`)
	plants, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Plant])
	return plants, dbError(err)
}

func (s *PlantStore) Get(ctx context.Context, id string) (Plant, error) {
	if s.db == nil {
		return Plant{}, ErrNoDatabase
	}
	rows, _ := s.db.Query(ctx, `SELECT `+plantColumns+` FROM plants WHERE id = $1`, id)
	p, err := pgx.CollectOneRow(rows, pgx.RowToStructByPos[Plant])
	return p, dbError(err)
}

// dbError turns a database error into one of the errors above, so the API
// can answer with the right status code.
func dbError(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return ErrPlantNotFound
	case errors.As(err, &pgErr) && pgErr.Code == "42P01": // undefined_table
		return ErrNoTable
	case errors.As(err, &pgErr):
		return err // a genuine SQL problem
	default: // the database didn't answer
		return fmt.Errorf("%w (%v)", ErrDBUnavailable, err)
	}
}
