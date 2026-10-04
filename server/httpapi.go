package main

// The HTTP API, on port 8080 of the board's WiFi address:
//
//	GET /api/plant/{id}   a plant's readings, status and dialog (for the UI)
//	GET /api/plant        the same with default houseplant thresholds
//	GET /api/plants       every plant
//	GET /api/sensors      the latest raw reading
//	GET /api/caretaker    the caretaker's wallet and the rewards paid
//	POST /api/caretaker   set the caretaker's wallet
//	POST /api/talk/{id}   a voice recording for the plant; returns a job
//	GET /api/talk/jobs/{job}         the job: queued, working, or the answer
//	GET /api/talk/jobs/{job}/voice   the answer in the plant's voice (MP3)
//	GET /healthz          liveness and the deployed version
//
// Errors are JSON: {"error": "..."}.

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"dryad/ai"
	"dryad/plants"
	"dryad/rewards"
	"dryad/sensors"
)

// version is set at build time by deploy.sh.
var version = "dev"

type server struct {
	sensors *sensors.LatestReading
	plants  *plants.PlantStore
	rewards *rewards.Rewards
	talker  *Talker
}

// startPlantServer starts the sensor bridge and the HTTP API. The channel gets
// the HTTP server's error if it stops.
func startPlantServer() <-chan error {
	plants, err := plants.OpenPlantStore(envOr("TIGER_DATABASE_URL", ai.LoadKeyFromEnvFile(".env", "TIGER_DATABASE_URL")))
	if err != nil {
		log.Fatalf("plants: bad TIGER_DATABASE_URL: %v", err)
	}
	rewards, err := rewards.NewRewards(envOr("SOLANA_TREASURY_KEY", ai.LoadKeyFromEnvFile(".env", "SOLANA_TREASURY_KEY")))
	if err != nil {
		log.Fatalf("rewards: bad SOLANA_TREASURY_KEY: %v", err)
	}
	latest := new(sensors.LatestReading)
	s := &server{sensors: latest, plants: plants, rewards: rewards, talker: newTalker(latest)}
	go sensors.RunBridge(envOr("DRYAD_ROUTER", sensors.DefaultRouterAddr), s.sensors)
	go rewards.Watch(s.sensors)

	httpServer := &http.Server{
		Addr:              envOr("DRYAD_HTTP_ADDR", ":8080"),
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
	mux.HandleFunc("POST /api/talk/{id}", s.talk)
	mux.HandleFunc("GET /api/talk/jobs/{job}", s.talkJob)
	mux.HandleFunc("GET /api/talk/jobs/{job}/voice", s.talkVoice)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version})
	})
	return mux
}

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

// getPlantState judges the latest reading against the plant's ranges.
func (s *server) getPlantState(w http.ResponseWriter, r *http.Request) {
	p := plants.DefaultPlant
	if id := r.PathValue("id"); id != "" {
		var err error
		if p, err = s.plants.Get(r.Context(), strings.ToLower(id)); err != nil {
			writePlantError(w, err)
			return
		}
	}

	reading, ok := s.sensors.Get()
	state := plantState{Status: p.Status(reading, ok)}
	state.Dialog = plants.Dialog(p, state.Status)
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

// writePlantError picks the status code for a plants error.
func writePlantError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plants.ErrPlantNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, plants.ErrNoDatabase), errors.Is(err, plants.ErrNoTable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, plants.ErrDBUnavailable):
		log.Printf("plants: %v", err)
		writeError(w, http.StatusServiceUnavailable, plants.ErrDBUnavailable.Error())
	default:
		log.Printf("plants: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func (s *server) getSensors(w http.ResponseWriter, r *http.Request) {
	reading, ok := s.sensors.Get()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "no sensor reading yet")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		sensors.Reading
		Stale bool `json:"stale"`
	}{reading, reading.Stale()})
}

func (s *server) getCaretaker(w http.ResponseWriter, r *http.Request) {
	if !s.rewards.On() {
		writeError(w, http.StatusServiceUnavailable, rewards.ErrRewardsOff.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.rewards.State())
}

// setCaretaker takes {"wallet": "<Solana address>", "plant_id": "fern"}.
// Without a plant_id, the watering is judged by the default thresholds.
func (s *server) setCaretaker(w http.ResponseWriter, r *http.Request) {
	if !s.rewards.On() {
		writeError(w, http.StatusServiceUnavailable, rewards.ErrRewardsOff.Error())
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
	p := plants.DefaultPlant
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

// talk queues a voice recording (the body, as the browser recorded it) and
// answers 202 {"job": "..."}; the page then asks talkJob for the answer.
func (s *server) talk(w http.ResponseWriter, r *http.Request) {
	if !s.talker.On() {
		writeError(w, http.StatusServiceUnavailable, errTalkOff.Error())
		return
	}
	p, err := s.plants.Get(r.Context(), strings.ToLower(r.PathValue("id")))
	if err != nil {
		writePlantError(w, err)
		return
	}
	audio, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, "that recording is too long")
		return
	case err != nil || len(audio) == 0:
		writeError(w, http.StatusBadRequest, "the recording didn't arrive, try again")
		return
	}

	job, err := s.talker.Submit(p, audio, r.Header.Get("Content-Type"))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job": job})
}

// talkJob is the job's state: {"status": "queued", "position": 2},
// {"status": "working"}, {"status": "done", "heard": "...", "answer": "...",
// "voice": true} or {"status": "failed", "error": "..."}.
func (s *server) talkJob(w http.ResponseWriter, r *http.Request) {
	state, _, ok := s.talker.Job(r.PathValue("job"))
	if !ok {
		writeError(w, http.StatusNotFound, errNoJob.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *server) talkVoice(w http.ResponseWriter, r *http.Request) {
	_, voice, ok := s.talker.Job(r.PathValue("job"))
	if !ok || voice == nil {
		writeError(w, http.StatusNotFound, "no voice for that answer (yet)")
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Write(voice)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// allowCORS lets pages from other origins call the API.
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

// envOr is the environment variable, or fallback when it's unset.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
