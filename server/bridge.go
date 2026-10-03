package main

// Receives sensor reports from the MCU through arduino-router.
//
// On the UNO Q the MCU talks MessagePack-RPC over a UART to arduino-router,
// which exposes the same protocol on a unix socket. We register the method
// the firmware notifies ("groot.sample", see firmware/groot_config.h) and the
// router forwards every Bridge.notify() from the MCU to us.
//
// Message formats (MessagePack arrays):
//   request      [0, msgid, method, params]
//   response     [1, msgid, error, result]
//   notification [2, method, params]

import (
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

const (
	bridgeMethod          = "groot.sample" // GROOT_RPC_METHOD
	bridgeProtocolVersion = 2              // GROOT_PROTOCOL_VERSION
	defaultRouterAddr     = "/var/run/arduino-router.sock"

	rpcRequest      = 0
	rpcResponse     = 1
	rpcNotification = 2
)

// RunBridge connects to arduino-router and publishes readings into Sensors,
// reconnecting forever. addr is a unix socket path, "unix:<path>" or
// "tcp:<host:port>" (e.g. through `adb forward` when testing on a laptop).
func RunBridge(addr string) {
	lastErr := ""
	for {
		err := bridgeSession(addr)
		if msg := err.Error(); msg != lastErr { // don't spam the same error every retry
			log.Printf("bridge: %v (retrying every 2s)", err)
			lastErr = msg
		}
		time.Sleep(2 * time.Second)
	}
}

func dialRouter(addr string) (net.Conn, error) {
	switch {
	case strings.HasPrefix(addr, "tcp:"):
		return net.DialTimeout("tcp", strings.TrimPrefix(addr, "tcp:"), 5*time.Second)
	default:
		return net.DialTimeout("unix", strings.TrimPrefix(addr, "unix:"), 5*time.Second)
	}
}

func bridgeSession(addr string) error {
	conn, err := dialRouter(addr)
	if err != nil {
		return fmt.Errorf("connect %s: %w", addr, err)
	}
	defer conn.Close()

	var wmu sync.Mutex
	enc := msgpack.NewEncoder(conn)
	send := func(msg ...interface{}) error {
		wmu.Lock()
		defer wmu.Unlock()
		return enc.Encode(msg)
	}

	const registerID = 1
	if err := send(rpcRequest, registerID, "$/register", []interface{}{bridgeMethod}); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	dec := msgpack.NewDecoder(conn)
	for {
		msg, err := dec.DecodeSlice()
		if err == io.EOF {
			return fmt.Errorf("router closed the connection")
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if len(msg) < 3 {
			continue
		}
		kind, _ := toInt(msg[0])

		switch kind {
		case rpcResponse: // only ever the $/register reply
			if len(msg) < 4 {
				continue
			}
			if id, _ := toInt(msg[1]); id == registerID {
				if msg[2] != nil {
					return fmt.Errorf("register %q: %v", bridgeMethod, msg[2])
				}
				log.Printf("bridge: connected to arduino-router, listening for %q", bridgeMethod)
			}

		case rpcNotification:
			if method, _ := msg[1].(string); method == bridgeMethod {
				handleSample(msg[2])
			}

		case rpcRequest: // the MCU used Bridge.call() instead of notify()
			if len(msg) < 4 {
				continue
			}
			method, _ := msg[2].(string)
			if method == bridgeMethod {
				handleSample(msg[3])
				_ = send(rpcResponse, msg[1], nil, true)
			} else {
				_ = send(rpcResponse, msg[1], fmt.Sprintf("method not found: %s", method), nil)
			}
		}
	}
}

// handleSample decodes [version, seq, uptime_ms, light, moisture, temperature_c].
func handleSample(raw interface{}) {
	params, ok := raw.([]interface{})
	if !ok || len(params) < 6 {
		log.Printf("bridge: malformed %s params: %v", bridgeMethod, raw)
		return
	}
	if v, _ := toInt(params[0]); v != bridgeProtocolVersion {
		log.Printf("bridge: protocol version %d, expected %d - reflash the firmware or update bridge.go",
			v, bridgeProtocolVersion)
		return
	}
	seq, _ := toInt(params[1])
	uptime, _ := toInt(params[2])
	light, _ := toFloat(params[3])
	moisture, _ := toFloat(params[4])
	temp, _ := toFloat(params[5])

	Sensors.Publish(Reading{
		Seq:          uint32(seq),
		UptimeMS:     uint32(uptime),
		Light:        optional(light),
		Moisture:     optional(moisture),
		TemperatureC: optional(temp),
		Source:       "bridge",
	})
}

// MessagePack picks the smallest integer encoding, so accept every width.
func toInt(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case int:
		return int64(n), true
	}
	return 0, false
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	if i, ok := toInt(v); ok {
		return float64(i), true
	}
	return 0, false
}
