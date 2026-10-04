package sensors

// The sensor bridge. The MCU sends its reports over UART to arduino-router, a
// Linux service that serves them on a unix socket as MessagePack-RPC. We
// register for "dryad.sample" when we connect and never send anything to the
// MCU. Messages are MessagePack arrays:
//
//	[0, id, method, params]   request: our "$/register" call
//	[1, id, error, result]    response: the router's answer to it
//	[2, method, params]       notification: a sensor report

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"strings"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

const (
	DefaultRouterAddr = "/var/run/arduino-router.sock"
	reportMethod      = "dryad.sample" // DRYAD_RPC_METHOD in firmware/dryad_config.h
	reportVersion     = 2              // DRYAD_PROTOCOL_VERSION in firmware/dryad_config.h
)

// report is the params of a dryad.sample notification, in firmware order.
type report struct {
	_msgpack     struct{} `msgpack:",as_array"`
	Version      int32
	Seq          uint32
	UptimeMS     uint32
	Light        float32 // 0-1, NaN when there is no reading
	Moisture     float32 // 0-1, NaN when there is no reading
	TemperatureC float32 // NaN when there is no reading
}

// RunBridge stores every report in latest and reconnects forever. addr is the
// router's unix socket or "tcp:host:port", e.g. after
// `adb forward tcp:7600 localfilesystem:/var/run/arduino-router.sock`.
func RunBridge(addr string, latest *LatestReading) {
	lastErr := ""
	for {
		err := receiveReports(addr, latest)
		if err.Error() != lastErr { // log each new problem once, not every retry
			log.Printf("bridge: %v (retrying every 2s)", err)
			lastErr = err.Error()
		}
		time.Sleep(2 * time.Second)
	}
}

// receiveReports handles one connection to the router until it fails.
func receiveReports(addr string, latest *LatestReading) error {
	network, address := "unix", addr
	if a, ok := strings.CutPrefix(addr, "tcp:"); ok {
		network, address = "tcp", a
	}
	conn, err := net.DialTimeout(network, address, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// The only message we send: ask the router to forward reports to us.
	register := []any{0, 1, "$/register", []any{reportMethod}}
	if err := msgpack.NewEncoder(conn).Encode(register); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	dec := msgpack.NewDecoder(conn)
	lastReportErr := ""
	for {
		var msg []msgpack.RawMessage
		if err := dec.Decode(&msg); errors.Is(err, io.EOF) {
			return errors.New("router closed the connection")
		} else if err != nil {
			return err
		}
		var kind int
		if len(msg) < 3 || msgpack.Unmarshal(msg[0], &kind) != nil {
			continue
		}

		switch kind {
		case 1: // the router's answer to $/register
			if !isNil(msg[2]) {
				var reason any
				msgpack.Unmarshal(msg[2], &reason)
				return fmt.Errorf("router refused to forward %s: %v", reportMethod, reason)
			}
			log.Printf("bridge: connected, receiving %s", reportMethod)

		case 2: // a notification
			var method string
			if msgpack.Unmarshal(msg[1], &method) != nil || method != reportMethod {
				continue
			}
			reading, err := decodeReport(msg[2])
			if err != nil {
				if err.Error() != lastReportErr {
					log.Printf("bridge: %v", err)
					lastReportErr = err.Error()
				}
				continue
			}
			lastReportErr = ""
			latest.Set(reading)
		}
	}
}

func decodeReport(params msgpack.RawMessage) (Reading, error) {
	var r report
	if err := msgpack.Unmarshal(params, &r); err != nil {
		return Reading{}, fmt.Errorf("unreadable report, firmware and server out of sync? (%w)", err)
	}
	if r.Version != reportVersion {
		return Reading{}, fmt.Errorf("firmware sends report version %d but the server reads %d: reflash or update",
			r.Version, reportVersion)
	}
	return Reading{
		Seq:          r.Seq,
		UptimeMS:     r.UptimeMS,
		MoisturePct:  value(r.Moisture * 100),
		LightPct:     value(r.Light * 100),
		TemperatureC: value(r.TemperatureC),
		ReceivedAt:   time.Now(),
	}, nil
}

// value rounds a sensor value to one decimal (the sensors aren't more
// precise than that), or returns nil for the firmware's NaN "no reading".
func value(v float32) *float64 {
	if math.IsNaN(float64(v)) {
		return nil
	}
	rounded := math.Round(float64(v)*10) / 10
	return &rounded
}

// isNil reports whether a decoded field is MessagePack nil, which the
// decoder turns into an empty RawMessage.
func isNil(raw msgpack.RawMessage) bool {
	return len(raw) == 0 || (len(raw) == 1 && raw[0] == 0xc0)
}
