// Package conn maintains the agent's persistent WebSocket session with the
// main server: authenticating, receiving assignments, and streaming results.
// It reconnects forever with exponential backoff, and buffers results while
// disconnected so short outages lose no data.
package conn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"drix/agent/internal/runner"
	"drix/shared/protocol"
)

// Config for the connection.
type Config struct {
	ServerURL string // e.g. https://status.example.com
	Token     string
	Version   string
	BufferMax int
}

// Client is the agent's session manager.
type Client struct {
	cfg     Config
	log     *slog.Logger
	results chan protocol.Result
	runner  *runner.Runner

	mu     sync.Mutex
	buffer []protocol.Result // pending results not yet acknowledged by a send
}

// New creates a client. The returned results channel must be handed to the runner.
func New(cfg Config, log *slog.Logger) (*Client, chan protocol.Result) {
	if cfg.BufferMax <= 0 {
		cfg.BufferMax = 50_000
	}
	ch := make(chan protocol.Result, 4096)
	return &Client{cfg: cfg, log: log, results: ch}, ch
}

// SetRunner attaches the runner (circular construction).
func (c *Client) SetRunner(r *runner.Runner) { c.runner = r }

// wsURL converts the server base URL into the agent endpoint.
func wsURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	u.Path += "/api/agent/connect"
	return u.String(), nil
}

// Run collects results continuously and keeps a session alive until ctx ends.
func (c *Client) Run(ctx context.Context) error {
	endpoint, err := wsURL(c.cfg.ServerURL)
	if err != nil {
		return err
	}
	go c.collect(ctx)

	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := c.session(ctx, endpoint)
		if ctx.Err() != nil {
			return nil
		}
		var fatal *fatalError
		if errors.As(err, &fatal) {
			c.log.Error("server rejected agent", "reason", fatal.msg)
			backoff = time.Minute
		} else {
			c.log.Warn("disconnected from server", "err", err)
			if time.Since(start) > 30*time.Second {
				backoff = time.Second // was a healthy session; retry fast
			}
		}
		wait := backoff + time.Duration(rand.Int64N(int64(backoff/2)+1))
		c.log.Info("reconnecting", "in", wait.Round(time.Millisecond))
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return nil
}

type fatalError struct{ msg string }

func (e *fatalError) Error() string { return e.msg }

// collect moves results from the runner into the bounded buffer.
func (c *Client) collect(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-c.results:
			c.mu.Lock()
			c.buffer = append(c.buffer, r)
			if over := len(c.buffer) - c.cfg.BufferMax; over > 0 {
				c.buffer = c.buffer[over:] // drop oldest
			}
			c.mu.Unlock()
		}
	}
}

func (c *Client) take(max int) []protocol.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := min(len(c.buffer), max)
	if n == 0 {
		return nil
	}
	out := make([]protocol.Result, n)
	copy(out, c.buffer[:n])
	c.buffer = c.buffer[n:]
	return out
}

func (c *Client) requeue(items []protocol.Result) {
	c.mu.Lock()
	c.buffer = append(items, c.buffer...)
	c.mu.Unlock()
}

func (c *Client) buffered() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.buffer)
}

func (c *Client) session(ctx context.Context, endpoint string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	ws, resp, err := websocket.Dial(dialCtx, endpoint, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + c.cfg.Token}},
	})
	dialCancel()
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return &fatalError{msg: "invalid or revoked agent token"}
		}
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(64 << 20) // assignments for many monitors can be large

	var writeMu sync.Mutex
	send := func(t string, v any) error {
		b, err := protocol.Encode(t, v)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return ws.Write(wctx, websocket.MessageText, b)
	}

	host, _ := os.Hostname()
	if err := send(protocol.TypeHello, protocol.Hello{
		Version: protocol.Version, Agent: c.cfg.Version, Hostname: host,
		OS: runtime.GOOS, Arch: runtime.GOARCH,
	}); err != nil {
		return err
	}

	errc := make(chan error, 2)
	heartbeat := 5 * time.Second

	// Reader.
	go func() {
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				errc <- err
				return
			}
			var env protocol.Envelope
			if err := json.Unmarshal(data, &env); err != nil {
				c.log.Warn("bad message", "err", err)
				continue
			}
			switch env.Type {
			case protocol.TypeWelcome:
				var w protocol.Welcome
				_ = json.Unmarshal(env.Data, &w)
				if w.Heartbeat > 0 {
					heartbeat = time.Duration(w.Heartbeat) * time.Second
				}
				c.log.Info("connected", "agent_id", w.AgentID, "name", w.Name, "server", w.ServerID)
			case protocol.TypeAssign:
				var a protocol.Assign
				if err := json.Unmarshal(env.Data, &a); err != nil {
					c.log.Warn("bad assign", "err", err)
					continue
				}
				started, stopped := c.runner.Apply(a.Monitors)
				c.log.Info("assignment updated", "monitors", len(a.Monitors), "started", started, "stopped", stopped)
			case protocol.TypeTest:
				var t protocol.Test
				if err := json.Unmarshal(env.Data, &t); err != nil {
					continue
				}
				go func() {
					res := c.runner.RunOnce(ctx, t.Monitor)
					_ = send(protocol.TypeTestResult, protocol.TestResult{ReqID: t.ReqID, Result: res})
				}()
			case protocol.TypeError:
				var e protocol.Error
				_ = json.Unmarshal(env.Data, &e)
				if e.Code == "unauthorized" || e.Code == "revoked" {
					errc <- &fatalError{msg: e.Message}
					return
				}
				c.log.Warn("server error", "code", e.Code, "msg", e.Message)
			}
		}
	}()

	// Writer: flush results quickly, heartbeat periodically.
	go func() {
		flush := time.NewTicker(250 * time.Millisecond)
		defer flush.Stop()
		lastBeat := time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-flush.C:
			}
			for {
				items := c.take(1000)
				if items == nil {
					break
				}
				if err := send(protocol.TypeResults, protocol.Results{Items: items}); err != nil {
					c.requeue(items)
					errc <- err
					return
				}
			}
			if time.Since(lastBeat) >= heartbeat {
				lastBeat = time.Now()
				if err := send(protocol.TypeHeartbeat, protocol.Heartbeat{
					Running: c.runner.Running(), InFlight: c.runner.InFlight(), Buffered: c.buffered(),
				}); err != nil {
					errc <- err
					return
				}
			}
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		_ = ws.Close(websocket.StatusNormalClosure, "agent shutting down")
		return nil
	}
}
