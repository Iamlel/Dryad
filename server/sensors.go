package main

import (
	"math"
	"sync"
	"time"
)

// Reading is one report from the plant's sensors (one every ~1 s from the MCU).
// Light and moisture are calibrated fractions 0.0-1.0. A nil field means the
// sensor gave no valid samples in that window (unplugged, still booting, ...).
type Reading struct {
	Seq          uint32    `json:"seq"`
	UptimeMS     uint32    `json:"uptime_ms"`
	Light        *float64  `json:"light"`
	Moisture     *float64  `json:"moisture"`
	TemperatureC *float64  `json:"temperature_c"`
	ReceivedAt   time.Time `json:"received_at"`
	Source       string    `json:"source"` // "bridge" or "http"
}

// optional turns NaN (the MCU's "no reading") into nil.
func optional(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// SensorStore keeps the latest reading and fans new ones out to subscribers.
// Safe for concurrent use.
type SensorStore struct {
	mu     sync.RWMutex
	latest *Reading
	subs   []chan Reading
}

// Sensors is the process-wide store fed by the Bridge listener and HTTP API.
var Sensors = &SensorStore{}

// Latest returns the most recent reading, or false if none has arrived yet.
func (s *SensorStore) Latest() (Reading, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.latest == nil {
		return Reading{}, false
	}
	return *s.latest, true
}

// Subscribe returns a channel that receives every new reading. Slow readers
// miss readings rather than blocking the sensor feed.
func (s *SensorStore) Subscribe() <-chan Reading {
	ch := make(chan Reading, 16)
	s.mu.Lock()
	s.subs = append(s.subs, ch)
	s.mu.Unlock()
	return ch
}

func (s *SensorStore) Publish(r Reading) {
	if r.ReceivedAt.IsZero() {
		r.ReceivedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = &r
	for _, ch := range s.subs {
		select {
		case ch <- r:
		default:
		}
	}
}
