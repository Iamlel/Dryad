package main

// HTTP API on the board's WiFi address, so anyone on the network can reach
// the plant without a laptop in between.
//
//   GET  /api/sensors   latest reading (503 until the first one arrives)
//   POST /api/sensors   push a reading in from somewhere other than the Bridge
//                       (testing without hardware, another device, ...)
//   GET  /healthz       "ok"

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

const defaultHTTPAddr = ":8080"

// staleAfter: no report for this long means the MCU or router has stopped.
const staleAfter = 10 * time.Second

// HTTPMux is exported so other handlers (e.g. chat) can be added to it.
var HTTPMux = http.NewServeMux()

func init() {
	HTTPMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	HTTPMux.HandleFunc("GET /api/sensors", getSensors)
	HTTPMux.HandleFunc("POST /api/sensors", postSensors)
}

// RunHTTP serves HTTPMux; it only returns if the listener fails.
func RunHTTP(addr string) {
	log.Printf("http: listening on %s", addr)
	if err := http.ListenAndServe(addr, HTTPMux); err != nil {
		log.Printf("http: %v", err)
	}
}

func getSensors(w http.ResponseWriter, r *http.Request) {
	reading, ok := Sensors.Latest()
	if !ok {
		http.Error(w, "no sensor reading yet", http.StatusServiceUnavailable)
		return
	}
	age := time.Since(reading.ReceivedAt)
	writeJSON(w, http.StatusOK, struct {
		Reading
		AgeSeconds float64 `json:"age_seconds"`
		Stale      bool    `json:"stale"`
	}{reading, age.Seconds(), age > staleAfter})
}

func postSensors(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Seq          uint32   `json:"seq"`
		UptimeMS     uint32   `json:"uptime_ms"`
		Light        *float64 `json:"light"`
		Moisture     *float64 `json:"moisture"`
		TemperatureC *float64 `json:"temperature_c"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	reading := Reading{
		Seq:          in.Seq,
		UptimeMS:     in.UptimeMS,
		Light:        in.Light,
		Moisture:     in.Moisture,
		TemperatureC: in.TemperatureC,
		ReceivedAt:   time.Now(),
		Source:       "http",
	}
	Sensors.Publish(reading)
	writeJSON(w, http.StatusOK, reading)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
