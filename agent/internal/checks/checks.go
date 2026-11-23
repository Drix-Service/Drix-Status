// Package checks implements the probe types an agent can run.
package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"drix/shared/protocol"
)

// Run executes one check for the monitor and always returns a Result.
func Run(ctx context.Context, m protocol.MonitorSpec) protocol.Result {
	timeout := m.Timeout()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now().UTC()

	var r protocol.Result
	switch m.Type {
	case protocol.TypeHTTP:
		var c protocol.HTTPConfig
		if err := decode(m.Config, &c); err != nil {
			r = fail(err)
			break
		}
		r = HTTP(ctx, m.Target, c)
	case protocol.TypeTCP:
		var c protocol.TCPConfig
		if err := decode(m.Config, &c); err != nil {
			r = fail(err)
			break
		}
		r = TCP(ctx, m.Target, c)
	case protocol.TypePing:
		var c protocol.PingConfig
		if err := decode(m.Config, &c); err != nil {
			r = fail(err)
			break
		}
		r = Ping(ctx, m.Target, c)
	case protocol.TypeDNS:
		var c protocol.DNSConfig
		if err := decode(m.Config, &c); err != nil {
			r = fail(err)
			break
		}
		r = DNS(ctx, m.Target, c)
	case protocol.TypeSSL:
		var c protocol.SSLConfig
		if err := decode(m.Config, &c); err != nil {
			r = fail(err)
			break
		}
		r = SSL(ctx, m.Target, c)
	case protocol.TypeJSON:
		var c protocol.JSONConfig
		if err := decode(m.Config, &c); err != nil {
			r = fail(err)
			break
		}
		r = JSON(ctx, m.Target, c)
	case protocol.TypeSMTP:
		var c protocol.SMTPConfig
		if err := decode(m.Config, &c); err != nil {
			r = fail(err)
			break
		}
		r = SMTP(ctx, m.Target, c)
	case protocol.TypeGRPC:
		var c protocol.GRPCConfig
		if err := decode(m.Config, &c); err != nil {
			r = fail(err)
			break
		}
		r = GRPC(ctx, m.Target, c)
	default:
		r = fail(fmt.Errorf("unknown monitor type %q", m.Type))
	}
	r.MonitorID = m.ID
	r.At = start // start time: the server derives the schedule slot from it
	return r
}

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	return nil
}

func fail(err error) protocol.Result {
	return protocol.Result{At: time.Now().UTC(), OK: false, Message: err.Error()}
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// timeoutMessage normalizes context deadline errors into something readable.
func timeoutMessage(ctx context.Context, err error) string {
	if ctx.Err() == context.DeadlineExceeded {
		return "timed out"
	}
	return err.Error()
}
