// Package protocol defines the wire format between the Drix main server and
// its check agents. Both sides import this package so the contract can never
// drift. Messages are JSON envelopes over a single WebSocket connection.
package protocol

import (
	"encoding/json"
	"time"
)

// Version is bumped whenever the protocol changes incompatibly.
const Version = 1

// Message types.
const (
	// agent -> server
	TypeHello      = "hello"
	TypeHeartbeat  = "heartbeat"
	TypeResults    = "results"
	TypeTestResult = "test_result"

	// server -> agent
	TypeWelcome = "welcome"
	TypeAssign  = "assign"
	TypeTest    = "test"
	TypeError   = "error"
)

// Envelope wraps every message on the wire.
type Envelope struct {
	Type string          `json:"t"`
	Data json.RawMessage `json:"d,omitempty"`
}

// Encode builds an envelope with a typed payload.
func Encode(t string, v any) ([]byte, error) {
	var raw json.RawMessage
	if v != nil {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return json.Marshal(Envelope{Type: t, Data: raw})
}

// MonitorType enumerates supported check kinds.
type MonitorType string

const (
	TypeHTTP MonitorType = "http"
	TypeTCP  MonitorType = "tcp"
	TypePing MonitorType = "ping"
	TypeDNS  MonitorType = "dns"
	TypeSSL  MonitorType = "ssl"  // certificate validity and expiry
	TypeJSON MonitorType = "json" // HTTP + assertions on a JSON body
	TypeSMTP MonitorType = "smtp" // mail server greeting (+ STARTTLS)
	TypeGRPC MonitorType = "grpc" // grpc.health.v1
	// TypePush monitors are fed by the monitored system calling a URL
	// (heartbeat). The server handles them; agents never run them.
	TypePush MonitorType = "push"
)

// AgentRuns reports whether agents execute this type.
func (t MonitorType) AgentRuns() bool { return t != TypePush }

// Status of a single check or a monitor.
type Status string

const (
	StatusUp       Status = "up"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
	StatusPending  Status = "pending"
	StatusPaused   Status = "paused"
)

// HTTPConfig configures HTTP(S) checks.
type HTTPConfig struct {
	Method          string            `json:"method,omitempty"`            // default GET
	ExpectStatusMin int               `json:"expect_status_min,omitempty"` // default 200
	ExpectStatusMax int               `json:"expect_status_max,omitempty"` // default 399
	Keyword         string            `json:"keyword,omitempty"`           // body must contain
	KeywordAbsent   string            `json:"keyword_absent,omitempty"`    // body must NOT contain
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	FollowRedirects *bool             `json:"follow_redirects,omitempty"` // default true
	IgnoreTLS       bool              `json:"ignore_tls,omitempty"`
}

// TCPConfig configures TCP port checks. Target may also be host:port.
type TCPConfig struct {
	Port int `json:"port,omitempty"`
}

// DNSConfig configures DNS checks.
type DNSConfig struct {
	RecordType string `json:"record_type,omitempty"` // A, AAAA, CNAME, MX, TXT, NS
	Resolver   string `json:"resolver,omitempty"`    // host[:port], default 1.1.1.1:53
	Expect     string `json:"expect,omitempty"`      // value that must appear in answers
}

// SSLConfig configures certificate checks. Target is host or host:port (443).
type SSLConfig struct {
	WarnDays   int    `json:"warn_days,omitempty"` // degraded below this, default 14
	CritDays   int    `json:"crit_days,omitempty"` // down below this, default 7
	ServerName string `json:"server_name,omitempty"`
}

// JSONAssertion checks one value in a JSON response.
type JSONAssertion struct {
	Path  string `json:"path"`            // dot path, e.g. data.items.0.status
	Op    string `json:"op"`              // equals | not_equals | contains | gt | lt | exists | not_exists
	Value string `json:"value,omitempty"` // compared as number when both sides are numeric
}

// JSONConfig is an HTTP check plus assertions on the JSON body.
type JSONConfig struct {
	HTTPConfig
	Assertions []JSONAssertion `json:"assertions,omitempty"`
}

// SMTPConfig configures mail server checks. Target is host or host:port (25).
type SMTPConfig struct {
	StartTLS  bool `json:"starttls,omitempty"`
	IgnoreTLS bool `json:"ignore_tls,omitempty"`
}

// GRPCConfig configures gRPC health checks. Target is host:port.
type GRPCConfig struct {
	Service   string `json:"service,omitempty"` // empty = whole server
	TLS       bool   `json:"tls,omitempty"`
	IgnoreTLS bool   `json:"ignore_tls,omitempty"`
}

// PushConfig configures heartbeat monitors.
type PushConfig struct {
	GraceS int `json:"grace_s,omitempty"` // extra time after the interval before going down
}

// PingConfig configures ICMP checks.
type PingConfig struct {
	Count int `json:"count,omitempty"` // packets per check, default 3
}

// MonitorSpec is everything an agent needs to run a check.
type MonitorSpec struct {
	ID         int64           `json:"id"`
	Type       MonitorType     `json:"type"`
	Target     string          `json:"target"`
	IntervalS  int             `json:"interval_s"`
	TimeoutMS  int             `json:"timeout_ms"`
	DegradedMS int             `json:"degraded_ms"`
	Config     json.RawMessage `json:"config,omitempty"`
	// Rev changes whenever the monitor is edited, letting agents restart timers.
	Rev int64 `json:"rev"`
	// Immediate asks the agent to run the first check right away instead of
	// waiting for the monitor's slot (set for just-created/edited monitors).
	Immediate bool `json:"now,omitempty"`
}

func (m MonitorSpec) Interval() time.Duration { return time.Duration(m.IntervalS) * time.Second }
func (m MonitorSpec) Timeout() time.Duration  { return time.Duration(m.TimeoutMS) * time.Millisecond }

// Result is a single check outcome produced by an agent.
type Result struct {
	MonitorID int64     `json:"m"`
	At        time.Time `json:"at"`
	OK        bool      `json:"ok"`
	// Degraded marks a passing check that still deserves attention, e.g. a
	// certificate close to expiry.
	Degraded  bool    `json:"deg,omitempty"`
	LatencyMS float64 `json:"lat"`
	Message   string  `json:"msg,omitempty"`
	// Optional metadata (e.g. HTTP status code, TLS expiry).
	Code       int        `json:"code,omitempty"`
	CertExpiry *time.Time `json:"cert,omitempty"`
}

// Hello is sent by the agent right after connecting.
type Hello struct {
	Version  int    `json:"version"`
	Agent    string `json:"agent"` // agent build version
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

// Welcome is the server's reply to Hello.
type Welcome struct {
	AgentID   string `json:"agent_id"`
	Name      string `json:"name"`
	ServerID  string `json:"server_id"`
	Heartbeat int    `json:"heartbeat_s"`
}

// Assign replaces the agent's full set of owned monitors.
type Assign struct {
	Monitors []MonitorSpec `json:"monitors"`
}

// Heartbeat reports liveness and load.
type Heartbeat struct {
	Running  int     `json:"running"`
	InFlight int     `json:"in_flight"`
	Buffered int     `json:"buffered"`
	Load     float64 `json:"load,omitempty"`
}

// Results carries a batch of results.
type Results struct {
	Items []Result `json:"items"`
}

// Test asks the agent to run a single ad-hoc check.
type Test struct {
	ReqID   string      `json:"req_id"`
	Monitor MonitorSpec `json:"monitor"`
}

// TestResult answers a Test.
type TestResult struct {
	ReqID  string `json:"req_id"`
	Result Result `json:"result"`
}

// Error is sent before the server closes a connection for a known reason.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
