package main

import (
	"sync"
	"time"
)

// staleAfter: the MCU reports every second, so no report for this long means
// the board or the router has stopped.
const staleAfter = 10 * time.Second

// Reading is one sensor report. A nil value means that sensor had no valid
// samples (unplugged, still booting, ...).
type Reading struct {
	Seq          uint32    `json:"seq"`
	UptimeMS     uint32    `json:"uptime_ms"`
	MoisturePct  *float64  `json:"moisture_pct"`
	LightPct     *float64  `json:"light_pct"`
	TemperatureC *float64  `json:"temperature_c"`
	ReceivedAt   time.Time `json:"received_at"`
}

func (r Reading) Stale() bool { return time.Since(r.ReceivedAt) > staleAfter }

// LatestReading holds the most recent reading. Safe for concurrent use.
type LatestReading struct {
	mu      sync.RWMutex
	reading Reading
	ok      bool
}

func (l *LatestReading) Set(r Reading) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reading, l.ok = r, true
}

// Get returns the latest reading, or false if none has arrived yet.
func (l *LatestReading) Get() (Reading, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.reading, l.ok
}
