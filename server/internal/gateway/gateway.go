// Package gateway is the main server's side of the agent protocol. It
// authenticates agents, keeps their sessions, computes which monitors each
// agent owns (rendezvous hashing over all live agents cluster-wide) and pushes
// assignment changes the moment membership or configuration changes.
package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/redis/go-redis/v9"

	"drix/server/internal/catalog"
	"drix/server/internal/cluster"
	"drix/server/internal/pipeline"
	"drix/server/internal/rkeys"
	"drix/server/internal/store"
	"drix/shared/protocol"
)

const (
	heartbeatEvery = 5 * time.Second
	readIdle       = 30 * time.Second
)

// HashToken returns the stored form of an agent token.
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// NewToken generates a fresh agent token ("drx_" + 40 hex chars).
func NewToken() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return "drx_" + hex.EncodeToString(b)
}

type session struct {
	agent  store.Agent
	live   cluster.LiveAgent
	send   chan []byte
	cancel context.CancelFunc

	mu         sync.Mutex
	assignHash uint64
	assigned   int
}

// Gateway manages agent sessions on this server instance.
type Gateway struct {
	st       *store.Store
	rdb      *redis.Client
	cat      *catalog.Catalog
	reg      *cluster.Registry
	pipe     *pipeline.Pipeline
	log      *slog.Logger
	serverID string

	mu       sync.Mutex
	sessions map[string]*session // by agent ID
	wake     chan struct{}
}

func New(st *store.Store, rdb *redis.Client, cat *catalog.Catalog, reg *cluster.Registry,
	pipe *pipeline.Pipeline, serverID string, log *slog.Logger) *Gateway {
	return &Gateway{st: st, rdb: rdb, cat: cat, reg: reg, pipe: pipe, serverID: serverID,
		log: log.With("component", "gateway"), sessions: map[string]*session{}, wake: make(chan struct{}, 1)}
}

func (g *Gateway) poke() {
	select {
	case g.wake <- struct{}{}:
	default:
	}
}

// ServeHTTP upgrades an authenticated agent connection.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" || !strings.HasPrefix(tok, "drx_") {
		http.Error(w, "missing agent token", http.StatusUnauthorized)
		return
	}
	agent, err := g.st.AgentByTokenHash(r.Context(), HashToken(tok))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "invalid agent token", http.StatusUnauthorized)
			return
		}
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if agent.Disabled {
		http.Error(w, "agent disabled", http.StatusForbidden)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	ws.SetReadLimit(16 << 20)
	g.handle(r.Context(), ws, agent, clientIP(r))
}

func (g *Gateway) handle(parent context.Context, ws *websocket.Conn, agent store.Agent, ip string) {
	defer ws.CloseNow()
	// Detach from the request context: shutdown is driven by our own cancel.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	defer cancel()
	log := g.log.With("agent", agent.Name, "agent_id", agent.ID)

	// Expect hello first.
	hctx, hcancel := context.WithTimeout(ctx, 10*time.Second)
	_, data, err := ws.Read(hctx)
	hcancel()
	if err != nil {
		return
	}
	var env protocol.Envelope
	var hello protocol.Hello
	if json.Unmarshal(data, &env) != nil || env.Type != protocol.TypeHello || json.Unmarshal(env.Data, &hello) != nil {
		ws.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	if hello.Version != protocol.Version {
		b, _ := protocol.Encode(protocol.TypeError, protocol.Error{Code: "version", Message: fmt.Sprintf("server speaks protocol v%d, agent v%d", protocol.Version, hello.Version)})
		_ = ws.Write(ctx, websocket.MessageText, b)
		ws.Close(websocket.StatusPolicyViolation, "protocol version mismatch")
		return
	}

	connID := randID()
	s := &session{
		agent:  agent,
		send:   make(chan []byte, 256),
		cancel: cancel,
		live: cluster.LiveAgent{
			ID: agent.ID, Name: agent.Name, Region: agent.Region, Server: g.serverID,
			Hostname: hello.Hostname, Version: hello.Agent, OS: hello.OS, Arch: hello.Arch,
			RemoteAddr: ip, ConnectedAt: time.Now(), ConnID: connID,
		},
	}

	// Only one session per agent: replace any previous one, wherever it lives.
	if prev, ok := g.reg.Get(ctx, agent.ID); ok && prev.ConnID != "" {
		g.kick(ctx, prev.Server, agent.ID, prev.ConnID)
	}
	g.mu.Lock()
	if old := g.sessions[agent.ID]; old != nil {
		old.cancel()
	}
	g.sessions[agent.ID] = s
	g.mu.Unlock()

	if err := g.reg.Touch(ctx, s.live); err != nil {
		log.Error("register agent", "err", err)
		return
	}
	_ = g.st.TouchAgent(ctx, agent.ID, hello.Hostname, hello.Agent)
	g.announce(ctx)
	log.Info("agent connected", "host", hello.Hostname, "version", hello.Agent, "ip", ip)

	defer func() {
		g.mu.Lock()
		if g.sessions[agent.ID] == s {
			delete(g.sessions, agent.ID)
		}
		g.mu.Unlock()
		dctx, dcancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer dcancel()
		if removed, _ := g.reg.Remove(dctx, agent.ID, connID); removed {
			g.announce(dctx)
		}
		log.Info("agent disconnected")
	}()

	welcome, _ := protocol.Encode(protocol.TypeWelcome, protocol.Welcome{
		AgentID: agent.ID, Name: agent.Name, ServerID: g.serverID, Heartbeat: int(heartbeatEvery.Seconds()),
	})
	s.send <- welcome
	g.poke()

	// Writer.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-s.send:
				wctx, wcancel := context.WithTimeout(ctx, 15*time.Second)
				err := ws.Write(wctx, websocket.MessageText, b)
				wcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Reader.
	for {
		rctx, rcancel := context.WithTimeout(ctx, readIdle)
		_, data, err := ws.Read(rctx)
		rcancel()
		if err != nil {
			return
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		switch env.Type {
		case protocol.TypeResults:
			var rs protocol.Results
			if json.Unmarshal(env.Data, &rs) == nil {
				g.pipe.Ingest(ctx, agent.ID, cluster.Location(agent.Region, agent.Name), rs.Items)
			}
		case protocol.TypeHeartbeat:
			var hb protocol.Heartbeat
			_ = json.Unmarshal(env.Data, &hb)
			s.mu.Lock()
			s.live.Running, s.live.InFlight, s.live.Buffered = hb.Running, hb.InFlight, hb.Buffered
			live := s.live
			s.mu.Unlock()
			// If another connection took over this agent, step aside.
			if cur, ok := g.reg.Get(ctx, agent.ID); ok && cur.ConnID != "" && cur.ConnID != connID {
				return
			}
			_ = g.reg.Touch(ctx, live)
		case protocol.TypeTestResult:
			var tr protocol.TestResult
			if json.Unmarshal(env.Data, &tr) == nil {
				if b, err := json.Marshal(testReply{Result: tr.Result, Agent: agent.Name}); err == nil {
					g.rdb.Publish(ctx, rkeys.TestReply(tr.ReqID), b)
				}
			}
		}
	}
}

// announce tells every server instance (and admin dashboards) that the agent set changed.
func (g *Gateway) announce(ctx context.Context) {
	g.rdb.Publish(ctx, rkeys.ChanCluster, g.serverID)
	b, _ := json.Marshal(pipeline.Broadcast{Type: "agents"})
	g.rdb.Publish(ctx, rkeys.ChanEvents, b)
}

// ---------- assignment ----------

// Run keeps assignments current until ctx ends.
func (g *Gateway) Run(ctx context.Context) {
	sub := g.rdb.Subscribe(ctx, rkeys.ChanCluster, rkeys.ServerChan(g.serverID))
	defer sub.Close()
	msgs := sub.Channel()
	catCh := g.cat.Subscribe()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			g.closeAll()
			return
		case m := <-msgs:
			if m.Channel == rkeys.ServerChan(g.serverID) {
				g.handleCommand(ctx, m.Payload)
				continue
			}
		case <-catCh:
		case <-g.wake:
		case <-tick.C:
		}
		g.rebalance(ctx)
	}
}

func (g *Gateway) rebalance(ctx context.Context) {
	g.mu.Lock()
	local := make([]*session, 0, len(g.sessions))
	for _, s := range g.sessions {
		local = append(local, s)
	}
	g.mu.Unlock()
	if len(local) == 0 {
		return
	}

	agents, err := g.reg.List(ctx)
	if err != nil {
		g.log.Error("list live agents", "err", err)
		return
	}
	live := make([]string, 0, len(agents))
	byLoc := map[string][]string{}
	for _, a := range agents {
		live = append(live, a.ID)
		l := cluster.Location(a.Region, a.Name)
		byLoc[l] = append(byLoc[l], a.ID)
	}
	data := g.cat.Get()
	ids := make([]int64, 0, len(data.Monitors))
	var multi []int64
	for i := range data.Monitors {
		m := &data.Monitors[i]
		if !m.Enabled || !m.Type.AgentRuns() {
			continue
		}
		if m.MultiLocation {
			multi = append(multi, m.ID) // checked once in every location
		} else {
			ids = append(ids, m.ID) // split evenly across all agents
		}
	}
	parts := cluster.Assign(ids, live)
	locParts := cluster.AssignLocations(multi, byLoc)

	for _, s := range local {
		mine := append(slices.Clone(parts[s.agent.ID]), locParts[s.agent.ID]...)
		specs := make([]protocol.MonitorSpec, 0, len(mine))
		h := fnv.New64a()
		for _, id := range mine {
			m := data.ByID[id]
			spec := m.Spec()
			spec.Immediate = time.Since(m.UpdatedAt) < 2*time.Minute
			specs = append(specs, spec)
			fmt.Fprintf(h, "%d:%d,", m.ID, m.Rev)
		}
		sum := h.Sum64()
		s.mu.Lock()
		same := s.assignHash == sum && s.assignHash != 0
		s.mu.Unlock()
		if same {
			continue
		}
		b, err := protocol.Encode(protocol.TypeAssign, protocol.Assign{Monitors: specs})
		if err != nil {
			continue
		}
		select {
		case s.send <- b:
			s.mu.Lock()
			s.assignHash, s.assigned = sum, len(specs)
			s.mu.Unlock()
			g.log.Info("assigned monitors", "agent", s.agent.Name, "count", len(specs), "live_agents", len(live))
		default:
			g.log.Warn("agent send queue full; will retry", "agent", s.agent.Name)
		}
	}
}

func (g *Gateway) closeAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.sessions {
		s.cancel()
	}
}

// ---------- cross-instance commands ----------

type command struct {
	Type    string               `json:"type"`
	Agent   string               `json:"agent"`
	ConnID  string               `json:"conn,omitempty"`
	ReqID   string               `json:"req_id,omitempty"`
	Monitor protocol.MonitorSpec `json:"monitor,omitempty"`
}

type testReply struct {
	Result protocol.Result `json:"result"`
	Agent  string          `json:"agent"`
}

func (g *Gateway) kick(ctx context.Context, server, agentID, connID string) {
	b, _ := json.Marshal(command{Type: "kick", Agent: agentID, ConnID: connID})
	g.rdb.Publish(ctx, rkeys.ServerChan(server), b)
}

// Disconnect forcibly closes an agent's session wherever it is (used on revoke/disable).
func (g *Gateway) Disconnect(ctx context.Context, agentID string) {
	if cur, ok := g.reg.Get(ctx, agentID); ok {
		g.kick(ctx, cur.Server, agentID, "")
	}
	g.mu.Lock()
	if s := g.sessions[agentID]; s != nil {
		s.cancel()
	}
	g.mu.Unlock()
}

func (g *Gateway) handleCommand(_ context.Context, payload string) {
	var c command
	if json.Unmarshal([]byte(payload), &c) != nil {
		return
	}
	g.mu.Lock()
	s := g.sessions[c.Agent]
	g.mu.Unlock()
	if s == nil {
		return
	}
	switch c.Type {
	case "kick":
		if c.ConnID == "" || c.ConnID == s.live.ConnID {
			s.cancel()
		}
	case "test":
		b, err := protocol.Encode(protocol.TypeTest, protocol.Test{ReqID: c.ReqID, Monitor: c.Monitor})
		if err == nil {
			select {
			case s.send <- b:
			default:
			}
		}
	}
}

// ErrNoAgents is returned when a test is requested but no agent is online.
var ErrNoAgents = errors.New("no agents are connected")

// Test runs a one-off check on a live agent (any server instance) and waits for the result.
func (g *Gateway) Test(ctx context.Context, spec protocol.MonitorSpec) (protocol.Result, string, error) {
	agents, err := g.reg.List(ctx)
	if err != nil {
		return protocol.Result{}, "", err
	}
	if len(agents) == 0 {
		return protocol.Result{}, "", ErrNoAgents
	}
	// Prefer the least busy agent.
	sort.Slice(agents, func(i, j int) bool { return agents[i].InFlight < agents[j].InFlight })
	target := agents[mrand.IntN(min(2, len(agents)))]

	reqID := randID()
	sub := g.rdb.Subscribe(ctx, rkeys.TestReply(reqID))
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil { // wait for subscription confirmation
		return protocol.Result{}, "", err
	}
	b, _ := json.Marshal(command{Type: "test", Agent: target.ID, ReqID: reqID, Monitor: spec})
	if err := g.rdb.Publish(ctx, rkeys.ServerChan(target.Server), b).Err(); err != nil {
		return protocol.Result{}, "", err
	}

	wait := spec.Timeout() + 5*time.Second
	select {
	case m := <-sub.Channel():
		var tr testReply
		if err := json.Unmarshal([]byte(m.Payload), &tr); err != nil {
			return protocol.Result{}, "", err
		}
		return tr.Result, tr.Agent, nil
	case <-time.After(wait):
		return protocol.Result{}, target.Name, errors.New("agent did not respond in time")
	case <-ctx.Done():
		return protocol.Result{}, "", ctx.Err()
	}
}

// LocalCount returns sessions on this instance.
func (g *Gateway) LocalCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sessions)
}

func randID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
