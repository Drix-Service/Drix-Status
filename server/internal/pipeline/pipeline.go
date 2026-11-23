// Package pipeline turns raw agent results into monitor state. Each result
// is deduplicated and applied to the monitor's live state in one atomic Redis
// round trip; durable writes are batched to Postgres every second.
package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/catalog"
	"drix/server/internal/rkeys"
	"drix/server/internal/store"
	"drix/shared/protocol"
)

// ingestScript dedupes by schedule slot, applies retry logic and returns
// {verdict, previous status, new status}.
var ingestScript = redis.NewScript(`
if redis.call('SET', KEYS[1], '1', 'NX', 'EX', ARGV[1]) == false then return {'dup', '', ''} end
local prev = redis.call('HGET', KEYS[2], 'status') or 'pending'
local last = tonumber(redis.call('HGET', KEYS[2], 'at') or '0')
local at = tonumber(ARGV[5])
if at < last then return {'stale', prev, prev} end
local fails = tonumber(redis.call('HGET', KEYS[2], 'fails') or '0')
local oldsince = redis.call('HGET', KEYS[2], 'since') or ''
local new = prev
if ARGV[2] == '1' then
  fails = 0
  if ARGV[3] == '1' then new = 'degraded' else new = 'up' end
else
  fails = fails + 1
  if fails > tonumber(ARGV[4]) then new = 'down' end
end
redis.call('HSET', KEYS[2], 'status', new, 'fails', fails, 'at', ARGV[5], 'lat', ARGV[6],
  'msg', ARGV[7], 'code', ARGV[8], 'cert', ARGV[9], 'agent', ARGV[10])
if new ~= prev or redis.call('HEXISTS', KEYS[2], 'since') == 0 then
  redis.call('HSET', KEYS[2], 'since', ARGV[5])
end
if ARGV[2] == '1' then redis.call('LPUSH', KEYS[3], ARGV[6]) else redis.call('LPUSH', KEYS[3], '-1') end
redis.call('LTRIM', KEYS[3], 0, 29)
return {'ok', prev, new, oldsince}`)

// LiveUpdate is broadcast to SSE clients for every accepted result.
type LiveUpdate struct {
	ID        int64   `json:"id"`
	Status    string  `json:"status"`
	Prev      string  `json:"prev,omitempty"`
	LatencyMS float64 `json:"latency_ms"`
	At        int64   `json:"at"`
	Message   string  `json:"message,omitempty"`
	Changed   bool    `json:"changed,omitempty"`
}

// Broadcast is the payload published on rkeys.ChanEvents.
type Broadcast struct {
	Type  string       `json:"type"`
	Items []LiveUpdate `json:"items,omitempty"`
	Agent any          `json:"agent,omitempty"`
}

type dayKey struct {
	id  int64
	day string
}

// Pipeline is safe for concurrent use by many agent connections.
type Pipeline struct {
	rdb *redis.Client
	st  *store.Store
	cat *catalog.Catalog
	log *slog.Logger

	// OnChange is called (outside locks) with status transitions; it may be nil.
	OnChange func(ctx context.Context, changes []Change)

	mu     sync.Mutex
	rows   []store.ResultRow
	daily  map[dayKey]*store.DailyDelta
	events []store.Event
}

func New(rdb *redis.Client, st *store.Store, cat *catalog.Catalog, log *slog.Logger) *Pipeline {
	return &Pipeline{rdb: rdb, st: st, cat: cat, log: log, daily: map[dayKey]*store.DailyDelta{}}
}

// Change is a monitor status transition, reported to OnChange.
type Change struct {
	MonitorID int64
	From, To  string
	Message   string
	At        time.Time
	Since     time.Time // when the previous status began
}

// Source of a result batch.
type Source int

const (
	FromAgent     Source = iota // scheduled check: one result per schedule slot
	FromPush                    // heartbeat ping: every ping counts
	FromHeartbeat               // missed-heartbeat failure: one per interval
)

// Ingest processes a batch from one agent, checking from `location`.
func (p *Pipeline) Ingest(ctx context.Context, agentID, location string, items []protocol.Result) {
	p.ingest(ctx, agentID, location, FromAgent, items)
}

// IngestServer processes results produced by the server itself (heartbeats).
func (p *Pipeline) IngestServer(ctx context.Context, src Source, items []protocol.Result) {
	p.ingest(ctx, "", "", src, items)
}

func (p *Pipeline) ingest(ctx context.Context, agentID, location string, src Source, items []protocol.Result) {
	data := p.cat.Get()
	type pending struct {
		r     protocol.Result
		m     *store.Monitor
		deg   bool
		multi bool // checked from every location: this is one location's result
		cmd   *redis.Cmd
		all   *redis.Cmd // the combined state, for multi-location monitors
	}
	work := make([]pending, 0, len(items))
	for _, r := range items {
		m, ok := data.ByID[r.MonitorID]
		if !ok || !m.Enabled {
			continue // deleted or paused since the agent scheduled it
		}
		deg := r.OK && (r.Degraded || (m.DegradedMS > 0 && r.LatencyMS > float64(m.DegradedMS)))
		work = append(work, pending{r: r, m: m, deg: deg, multi: m.MultiLocation && location != "" && src == FromAgent})
	}
	if len(work) == 0 {
		return
	}

	run := func(useSha bool) error {
		_, err := p.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for i := range work {
				w := &work[i]
				iv := w.m.Spec().Interval()
				slot := protocol.Slot(w.m.ID, iv, w.r.At)
				if src == FromPush {
					slot = w.r.At.UnixNano() // pings are never deduplicated
				}
				ttl := max(int(iv.Seconds())*2, 10)
				cert := ""
				if w.r.CertExpiry != nil {
					cert = strconv.FormatInt(w.r.CertExpiry.UnixMilli(), 10)
				}
				keys := []string{rkeys.Slot(w.m.ID, slot), rkeys.Status(w.m.ID), rkeys.Latency(w.m.ID)}
				if w.multi {
					keys[0], keys[1] = rkeys.SlotLoc(w.m.ID, slot, location), rkeys.StatusLoc(w.m.ID, location)
					pipe.SAdd(ctx, rkeys.MonLocations(w.m.ID), location)
					pipe.Expire(ctx, rkeys.MonLocations(w.m.ID), 7*24*time.Hour)
				}
				args := []any{ttl, b2s(w.r.OK), b2s(w.deg), w.m.Retries, w.r.At.UnixMilli(),
					strconv.FormatFloat(w.r.LatencyMS, 'f', 2, 64), truncate(w.r.Message, 300), w.r.Code, cert, agentID}
				if useSha {
					w.cmd = ingestScript.EvalSha(ctx, pipe, keys, args...)
				} else {
					w.cmd = ingestScript.Eval(ctx, pipe, keys, args...)
				}
				if w.multi {
					pipe.Expire(ctx, keys[1], 2*24*time.Hour)
				}
			}
			return nil
		})
		return err
	}
	if err := run(true); err != nil && strings.Contains(err.Error(), "NOSCRIPT") {
		err = run(false)
		if err != nil {
			p.log.Error("ingest failed", "err", err)
			return
		}
	} else if err != nil && err != redis.Nil {
		p.log.Error("ingest failed", "err", err)
		return
	}

	// Multi-location monitors: combine the locations into the overall state.
	combined := map[int64]Overall{}
	var multiIdx []int
	for i := range work {
		w := &work[i]
		if !w.multi {
			continue
		}
		if res, err := w.cmd.StringSlice(); err != nil || len(res) < 1 || res[0] != "ok" {
			continue
		}
		if _, done := combined[w.m.ID]; !done {
			states, err := ReadLocations(ctx, p.rdb, w.m.ID)
			if err != nil {
				p.log.Warn("read locations", "monitor", w.m.ID, "err", err)
				continue
			}
			cutoff := time.Now().Add(-staleAfter(w.m.Spec().Interval())).UnixMilli()
			o, ok := Combine(states, cutoff)
			if !ok {
				continue
			}
			combined[w.m.ID] = o
		}
		multiIdx = append(multiIdx, i)
	}
	if len(multiIdx) > 0 {
		_, err := p.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for _, i := range multiIdx {
				w := &work[i]
				o := combined[w.m.ID]
				w.all = setOverallScript.Eval(ctx, pipe, []string{rkeys.Status(w.m.ID)},
					o.Status, w.r.At.UnixMilli(), strconv.FormatFloat(o.Latency, 'f', 2, 64), o.Message, o.Code, o.Cert)
			}
			return nil
		})
		if err != nil && err != redis.Nil {
			p.log.Error("combine locations", "err", err)
		}
	}

	updates := make([]LiveUpdate, 0, len(work))
	var transitions []Change
	p.mu.Lock()
	for _, w := range work {
		res, err := w.cmd.StringSlice()
		if err != nil || len(res) < 3 || res[0] != "ok" {
			continue
		}
		msg := truncate(w.r.Message, 300)
		if w.multi {
			// The overall state, combined from every location.
			if w.all == nil {
				continue
			}
			if res, err = w.all.StringSlice(); err != nil || len(res) < 5 {
				continue
			}
			msg = res[4]
		}
		prev, cur := res[1], res[2]
		// A new monitor coming up isn't news; a new monitor that's down is.
		if prev != cur && !(isInitial(prev) && cur == "up") {
			ch := Change{MonitorID: w.m.ID, From: prev, To: cur, Message: msg, At: w.r.At}
			if len(res) > 3 {
				if ms, err := strconv.ParseInt(res[3], 10, 64); err == nil {
					ch.Since = time.UnixMilli(ms).UTC()
				}
			}
			transitions = append(transitions, ch)
		}
		checkStatus := "up"
		inMaint := data.InMaintenance[w.m.ID]
		switch {
		case !w.r.OK && inMaint:
			checkStatus = "maintenance" // expected during the window: not downtime
		case !w.r.OK:
			checkStatus = "down"
		case w.deg:
			checkStatus = "degraded"
		}
		var aid, loc *string
		if agentID != "" {
			a := agentID
			aid = &a
		}
		if w.multi {
			l := location
			loc = &l
		}
		p.rows = append(p.rows, store.ResultRow{
			MonitorID: w.m.ID, CheckedAt: w.r.At, Status: checkStatus,
			LatencyMS: float32(w.r.LatencyMS), Message: truncate(w.r.Message, 300), AgentID: aid, Location: loc,
		})
		if w.multi {
			// Uptime follows the overall state, not this one location.
			switch {
			case checkStatus == "maintenance":
			case cur == "down":
				checkStatus = "down"
			case cur == "degraded":
				checkStatus = "degraded"
			default:
				checkStatus = "up"
			}
		}
		day := w.r.At.UTC().Truncate(24 * time.Hour)
		k := dayKey{w.m.ID, day.Format("2006-01-02")}
		d := p.daily[k]
		if d == nil {
			d = &store.DailyDelta{MonitorID: w.m.ID, Day: day}
			p.daily[k] = d
		}
		if checkStatus != "maintenance" {
			d.Total++
		}
		switch checkStatus {
		case "maintenance":
			d.Maintenance++
		case "up":
			d.Up++
			d.LatencySum += w.r.LatencyMS
		case "degraded":
			d.Degraded++
			d.LatencySum += w.r.LatencyMS
		default:
			d.Down++
		}
		changed := prev != cur && !(isInitial(prev) && cur == "up")
		if changed {
			p.events = append(p.events, store.Event{
				MonitorID: w.m.ID, FromStatus: prev, ToStatus: cur,
				Message: msg, CreatedAt: w.r.At,
			})
		}
		updates = append(updates, LiveUpdate{
			ID: w.m.ID, Status: cur, Prev: prev, LatencyMS: w.r.LatencyMS,
			At: w.r.At.UnixMilli(), Message: truncate(msg, 160), Changed: prev != cur,
		})
	}
	p.mu.Unlock()

	if len(transitions) > 0 && p.OnChange != nil {
		p.OnChange(ctx, transitions)
	}
	if len(updates) > 0 {
		if b, err := json.Marshal(Broadcast{Type: "status", Items: updates}); err == nil {
			p.rdb.Publish(ctx, rkeys.ChanEvents, b)
		}
		// Only pages showing these monitors need a new public snapshot.
		dirty := map[int64]bool{}
		for _, u := range updates {
			for _, pid := range data.PagesOf[u.ID] {
				dirty[pid] = true
			}
		}
		if len(dirty) > 0 {
			members := make([]any, 0, len(dirty))
			for pid := range dirty {
				members = append(members, pid)
			}
			p.rdb.SAdd(ctx, rkeys.SnapshotDirty, members...)
		}
	}
}

func isInitial(s string) bool { return s == "pending" || s == "paused" }

// Run flushes buffered writes to Postgres every second until ctx ends.
func (p *Pipeline) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			p.flush(fctx)
			cancel()
			return
		case <-t.C:
			p.flush(ctx)
		}
	}
}

func (p *Pipeline) flush(ctx context.Context) {
	p.mu.Lock()
	rows, daily, events := p.rows, p.daily, p.events
	p.rows, p.daily, p.events = nil, map[dayKey]*store.DailyDelta{}, nil
	p.mu.Unlock()

	if len(rows) > 0 {
		if err := p.st.InsertResults(ctx, rows); err != nil {
			p.log.Error("insert results", "err", err, "rows", len(rows))
		}
	}
	if len(daily) > 0 {
		deltas := make([]store.DailyDelta, 0, len(daily))
		for _, d := range daily {
			deltas = append(deltas, *d)
		}
		if err := p.st.AddDailyStats(ctx, deltas); err != nil {
			p.log.Error("daily stats", "err", err)
		}
	}
	if len(events) > 0 {
		if err := p.st.InsertEvents(ctx, events); err != nil {
			p.log.Error("insert events", "err", err)
		}
	}
}

func b2s(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
