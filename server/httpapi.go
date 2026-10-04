package main

// The HTTP API, on port 8080 of the board's WiFi address:
//
//	GET /api/plant/{id}   a plant's readings, status and dialog (for the UI)
//	GET /api/plant        the same with default houseplant thresholds
//	GET /api/plants       every plant
//	GET /api/sensors      the latest raw reading
//	GET /api/caretaker    the caretaker's wallet and the rewards paid
//	POST /api/caretaker   set the caretaker's wallet
//	GET /healthz          liveness and the deployed version
//
// Apart from the caretaker, it only reads; plants are added with
// plants.sql. Errors are JSON: {"error": "..."}.

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// version is set at build time by deploy.sh.
var version = "dev"

type server struct {
	sensors *LatestReading
	plants  *PlantStore
	rewards *Rewards
}

// startPlantServer starts the sensor bridge and the HTTP API in the
// background. The returned channel receives the HTTP server's error if it
// ever stops.
func startPlantServer() <-chan error {
	plants, err := openPlantStore(envOr("TIGER_DATABASE_URL", loadKeyFromEnvFile(".env", "TIGER_DATABASE_URL")))
	if err != nil {
		log.Fatalf("plants: bad TIGER_DATABASE_URL: %v", err)
	}
	rewards, err := newRewards(envOr("SOLANA_TREASURY_KEY", loadKeyFromEnvFile(".env", "SOLANA_TREASURY_KEY")))
	if err != nil {
		log.Fatalf("rewards: bad SOLANA_TREASURY_KEY: %v", err)
	}
	s := &server{sensors: new(LatestReading), plants: plants, rewards: rewards}
	go runBridge(envOr("GROOT_ROUTER", defaultRouterAddr), s.sensors)
	go rewards.watch(s.sensors)

	httpServer := &http.Server{
		Addr:              envOr("GROOT_HTTP_ADDR", ":8080"),
		Handler:           allowCORS(s.routes()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Printf("http: listening on %s (version %s)", httpServer.Addr, version)
		err := httpServer.ListenAndServe()
		log.Printf("http: %v", err)
		errc <- err
	}()
	return errc
}

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plant", s.getPlantState)
	mux.HandleFunc("GET /api/plant/{id}", s.getPlantState)
	mux.HandleFunc("GET /api/plants", s.listPlants)
	mux.HandleFunc("GET /api/sensors", s.getSensors)
	mux.HandleFunc("GET /api/caretaker", s.getCaretaker)
	mux.HandleFunc("POST /api/caretaker", s.setCaretaker)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version})
	})
	return mux
}

// ---- Plants ----------------------------------------------------------------

// plantState is what GET /api/plant/{id} returns. The UI reads these names.
type plantState struct {
	MoisturePct  *float64   `json:"moisture_pct"`
	TemperatureC *float64   `json:"temperature_c"`
	LightPct     *float64   `json:"light_pct"`
	Status       string     `json:"status"`
	Dialog       string     `json:"dialog"`
	Stale        bool       `json:"stale"`
	UpdatedAt    *time.Time `json:"updated_at"`
}

func (s *server) getPlantState(w http.ResponseWriter, r *http.Request) {
	p := defaultPlant
	if id := r.PathValue("id"); id != "" {
		var err error
		if p, err = s.plants.Get(r.Context(), strings.ToLower(id)); err != nil {
			writePlantError(w, err)
			return
		}
	}

	reading, ok := s.sensors.Get()
	state := plantState{Status: p.Status(reading, ok)}
	state.Dialog = dialog(p, state.Status)
	if ok {
		state.MoisturePct, state.TemperatureC, state.LightPct = reading.MoisturePct, reading.TemperatureC, reading.LightPct
		state.Stale, state.UpdatedAt = reading.Stale(), &reading.ReceivedAt
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *server) listPlants(w http.ResponseWriter, r *http.Request) {
	plants, err := s.plants.List(r.Context())
	if err != nil {
		writePlantError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plants)
}

func writePlantError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errPlantNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errNoDatabase), errors.Is(err, errNoTable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, errDBUnavailable):
		log.Printf("plants: %v", err)
		writeError(w, http.StatusServiceUnavailable, errDBUnavailable.Error())
	default:
		log.Printf("plants: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// ---- Sensors ---------------------------------------------------------------

func (s *server) getSensors(w http.ResponseWriter, r *http.Request) {
	reading, ok := s.sensors.Get()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "no sensor reading yet")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Reading
		Stale bool `json:"stale"`
	}{reading, reading.Stale()})
}

// ---- Caretaker rewards -----------------------------------------------------

func (s *server) getCaretaker(w http.ResponseWriter, r *http.Request) {
	if !s.rewards.On() {
		writeError(w, http.StatusServiceUnavailable, errRewardsOff.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.rewards.State())
}

// setCaretaker takes {"wallet": "<Solana address>", "plant_id": "fern"}.
// Without a plant_id, the watering is judged by the default thresholds.
func (s *server) setCaretaker(w http.ResponseWriter, r *http.Request) {
	if !s.rewards.On() {
		writeError(w, http.StatusServiceUnavailable, errRewardsOff.Error())
		return
	}
	var req struct {
		Wallet  string `json:"wallet"`
		PlantID string `json:"plant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, `send JSON: {"wallet": "<Solana address>", "plant_id": "fern"}`)
		return
	}
	p := defaultPlant
	if req.PlantID != "" {
		var err error
		if p, err = s.plants.Get(r.Context(), strings.ToLower(req.PlantID)); err != nil {
			writePlantError(w, err)
			return
		}
	}
	if err := s.rewards.SetCaretaker(req.Wallet, p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.rewards.State())
}

// ---- Helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// allowCORS lets a page served from elsewhere (e.g. the Flask UI) call the
// API from the browser.
func allowCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions { // preflight
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
