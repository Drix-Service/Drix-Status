// Package snapshot precomputes the public status page payload. The cluster
// leader rebuilds it at most once per second when something changed, stores
// both JSON and gzip bytes in Redis, and every server instance serves it with
// a single GET — no per-request database work at all.
package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"hash/fnv"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/catalog"
	"drix/server/internal/rkeys"
	"drix/server/internal/store"
)

const Days = 90

// LiveStatus is a monitor's current state from Redis.
type LiveStatus struct {
	Status      string     `json:"status"`
	LatencyMS   float64    `json:"latency_ms"`
	Message     string     `json:"message,omitempty"`
	Code        int        `json:"code,omitempty"`
	LastChecked *time.Time `json:"last_checked,omitempty"`
	Since       *time.Time `json:"since,omitempty"`
	CertExpiry  *time.Time `json:"cert_expiry,omitempty"`
	Agent       string     `json:"agent,omitempty"`
}

// ReadStatuses fetches live state for many monitors in one pipeline.
// If withLatency is set it also returns recent latency samples (newest last, -1 = failed).
func ReadStatuses(ctx context.Context, rdb *redis.Client, ids []int64, withLatency bool) (map[int64]LiveStatus, map[int64][]float64, error) {
	st := make([]*redis.MapStringStringCmd, len(ids))
	lat := make([]*redis.StringSliceCmd, len(ids))
	_, err := rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, id := range ids {
			st[i] = p.HGetAll(ctx, rkeys.Status(id))
			if withLatency {
				lat[i] = p.LRange(ctx, rkeys.Latency(id), 0, 29)
			}
		}
		return nil
	})
	if err != nil && err != redis.Nil {
		return nil, nil, err
	}
	out := make(map[int64]LiveStatus, len(ids))
	var spark map[int64][]float64
	if withLatency {
		spark = make(map[int64][]float64, len(ids))
	}
	for i, id := range ids {
		m := st[i].Val()
		ls := LiveStatus{Status: "pending"}
		if s := m["status"]; s != "" {
			ls.Status = s
		}
		ls.LatencyMS, _ = strconv.ParseFloat(m["lat"], 64)
		ls.Message = m["msg"]
		ls.Code, _ = strconv.Atoi(m["code"])
		ls.LastChecked = msPtr(m["at"])
		ls.Since = msPtr(m["since"])
		ls.CertExpiry = msPtr(m["cert"])
		ls.Agent = m["agent"]
		out[id] = ls
		if withLatency {
			vals := lat[i].Val()
			series := make([]float64, len(vals))
			for j, v := range vals { // LPUSH order is newest first; reverse it
				f, _ := strconv.ParseFloat(v, 64)
				series[len(vals)-1-j] = f
			}
			spark[id] = series
		}
	}
	return out, spark, nil
}

func msPtr(s string) *time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n == 0 {
		return nil
	}
	t := time.UnixMilli(n).UTC()
	return &t
}

// ---------- Public payload ----------

// PageInfo is the branding and identity of a status page.
type PageInfo struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	LogoURL     string `json:"logo_url"`
	WebsiteURL  string `json:"website_url"`
	AccentColor string `json:"accent_color"`
	ShowLatency bool   `json:"show_latency"`
	FooterText  string `json:"footer_text"`
	Language    string `json:"language"`
	IsDefault   bool   `json:"is_default"`
}

type Day struct {
	Date      string        `json:"d"`
	Uptime    float64       `json:"u"` // percent; -1 when no data
	Down      int           `json:"x,omitempty"`
	Degraded  int           `json:"g,omitempty"`
	Total     int           `json:"n,omitempty"`
	Incidents []DayIncident `json:"i,omitempty"`
}

// DayIncident marks an incident or maintenance window on an uptime bar.
type DayIncident struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Impact string `json:"impact"`
	Kind   string `json:"kind"`
}

// PublicUpdate is an incident update without internal author details.
type PublicUpdate struct {
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type AffectedMonitor struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Impact string `json:"impact"`
}

// Incident is the public view of an incident or maintenance window.
type Incident struct {
	ID             int64             `json:"id"`
	Kind           string            `json:"kind"`
	Title          string            `json:"title"`
	Status         string            `json:"status"`
	Impact         string            `json:"impact"`
	ScheduledStart *time.Time        `json:"scheduled_start,omitempty"`
	ScheduledEnd   *time.Time        `json:"scheduled_end,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	ResolvedAt     *time.Time        `json:"resolved_at,omitempty"`
	Updates        []PublicUpdate    `json:"updates"`
	Affected       []AffectedMonitor `json:"affected"`
}

// PublicIncident converts a stored incident for one page: only monitors on
// that page are listed, and author details are dropped.
func PublicIncident(in store.Incident, data *catalog.Data, pageID int64, latestOnly bool) Incident {
	out := Incident{ID: in.ID, Kind: in.Kind, Title: in.Title, Status: in.Status, Impact: in.Impact,
		ScheduledStart: in.ScheduledStart, ScheduledEnd: in.ScheduledEnd, CreatedAt: in.CreatedAt,
		ResolvedAt: in.ResolvedAt, Updates: []PublicUpdate{}, Affected: []AffectedMonitor{}}
	for i, u := range in.Updates {
		if latestOnly && i > 0 {
			break
		}
		out.Updates = append(out.Updates, PublicUpdate{ID: u.ID, Status: u.Status, Body: u.Body, CreatedAt: u.CreatedAt})
	}
	for _, m := range in.Monitors {
		if mon, ok := data.ByID[m.MonitorID]; ok && (pageID == 0 || data.OnPage(pageID, m.MonitorID)) {
			out.Affected = append(out.Affected, AffectedMonitor{ID: mon.ID, Name: displayName(data, pageID, mon), Impact: m.Impact})
		}
	}
	return out
}

func displayName(data *catalog.Data, pageID int64, m *store.Monitor) string {
	for _, sec := range data.Sections[pageID] {
		for _, pm := range sec.Monitors {
			if pm.MonitorID == m.ID && pm.DisplayName != nil && *pm.DisplayName != "" {
				return *pm.DisplayName
			}
		}
	}
	return m.Name
}

type Monitor struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	LatencyMS   float64    `json:"latency_ms"`
	Since       *time.Time `json:"since,omitempty"`
	LastChecked *time.Time `json:"last_checked,omitempty"`
	Uptime30    float64    `json:"uptime_30"`
	Uptime90    float64    `json:"uptime_90"`
	Days        []Day      `json:"days"`
	// Locations is set for monitors checked from every location that
	// show them on the page (fetched from /monitors/{id}/locations).
	Locations bool `json:"locations,omitempty"`
}

type Section struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Collapsed   bool      `json:"collapsed"`
	Status      string    `json:"status"`
	Monitors    []Monitor `json:"monitors"`
}

type Summary struct {
	Total       int `json:"total"`
	Up          int `json:"up"`
	Degraded    int `json:"degraded"`
	Down        int `json:"down"`
	Maintenance int `json:"maintenance"`
	Pending     int `json:"pending"`
	Paused      int `json:"paused"`
}

type Payload struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Page        PageInfo      `json:"page"`
	Overall     string        `json:"overall"` // operational | degraded | partial_outage | major_outage | maintenance | unknown
	Summary     Summary       `json:"summary"`
	Sections    []Section     `json:"sections"`
	Events      []store.Event `json:"events"`
	// Incidents are open incidents; Maintenance is in progress or upcoming
	// (next 30 days); History is everything closed in the last 14 days.
	Incidents   []Incident `json:"incidents"`
	Maintenance []Incident `json:"maintenance"`
	History     []Incident `json:"history"`
}

// Builder produces and stores per-page snapshots.
type Builder struct {
	rdb *redis.Client
	st  *store.Store
	cat *catalog.Catalog
	log *slog.Logger

	mu       sync.Mutex
	stats    map[int64]map[string]store.DayStat
	statsAt  time.Time
	lastFull time.Time
}

func NewBuilder(rdb *redis.Client, st *store.Store, cat *catalog.Catalog, log *slog.Logger) *Builder {
	return &Builder{rdb: rdb, st: st, cat: cat, log: log}
}

func (b *Builder) dayStats(ctx context.Context) (map[int64]map[string]store.DayStat, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stats != nil && time.Since(b.statsAt) < 20*time.Second {
		return b.stats, nil
	}
	since := time.Now().UTC().AddDate(0, 0, -Days)
	rows, err := b.st.DailyStatsSince(ctx, since)
	if err != nil {
		return nil, err
	}
	m := map[int64]map[string]store.DayStat{}
	for _, r := range rows {
		if m[r.MonitorID] == nil {
			m[r.MonitorID] = map[string]store.DayStat{}
		}
		m[r.MonitorID][r.Day.Format("2006-01-02")] = r
	}
	b.stats, b.statsAt = m, time.Now()
	return m, nil
}

// Build computes the payload for one page.
func (b *Builder) Build(ctx context.Context, pageID int64) (*Payload, error) {
	data := b.cat.Get()
	page, ok := data.PageByID[pageID]
	if !ok {
		return nil, store.ErrNotFound
	}
	stats, err := b.dayStats(ctx)
	if err != nil {
		return nil, err
	}
	ids := data.PageMonitorIDs[pageID]
	live, _, err := ReadStatuses(ctx, b.rdb, ids, false)
	if err != nil {
		return nil, err
	}
	events, err := b.st.ListEvents(ctx, 0, append([]int64{}, ids...), 15)
	if err != nil {
		return nil, err
	}
	for i := range events {
		events[i].Message = "" // internal error details stay private
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	dates := make([]string, Days)
	for i := range Days {
		dates[i] = today.AddDate(0, 0, -(Days - 1 - i)).Format("2006-01-02")
	}

	incidents, err := b.st.ListIncidents(ctx, store.IncidentFilter{PageID: pageID, Since: today.AddDate(0, 0, -Days), Limit: 500})
	if err != nil {
		return nil, err
	}
	ov := buildOverlay(incidents, today, dates)

	p := &Payload{
		GeneratedAt: time.Now().UTC(),
		Page: PageInfo{ID: page.ID, Slug: page.Slug, Name: page.Name, Description: page.Description,
			LogoURL: page.LogoURL, WebsiteURL: page.WebsiteURL, AccentColor: page.AccentColor,
			ShowLatency: page.ShowLatency, FooterText: page.FooterText, Language: page.Language, IsDefault: page.IsDefault},
		Events:      events,
		Sections:    []Section{},
		Incidents:   []Incident{},
		Maintenance: []Incident{},
		History:     []Incident{},
	}
	now := time.Now()
	for _, in := range incidents {
		switch {
		case in.Kind == store.KindIncident && in.ResolvedAt == nil:
			p.Incidents = append(p.Incidents, PublicIncident(in, data, pageID, false))
		case in.Kind == store.KindMaintenance && (in.Status == "in_progress" ||
			(in.Status == "scheduled" && in.ScheduledStart != nil && in.ScheduledStart.Before(now.AddDate(0, 0, 30)))):
			p.Maintenance = append(p.Maintenance, PublicIncident(in, data, pageID, false))
		case in.ResolvedAt != nil && in.ResolvedAt.After(now.AddDate(0, 0, -14)) && in.Status != "cancelled":
			p.History = append(p.History, PublicIncident(in, data, pageID, true))
		}
	}
	// Upcoming maintenance soonest first.
	slices.SortFunc(p.Maintenance, func(a, b Incident) int { return cmpTime(a.ScheduledStart, b.ScheduledStart) })

	counted := map[int64]bool{}
	for _, sec := range data.Sections[pageID] {
		out := Section{ID: sec.ID, Name: sec.Name, Description: sec.Description, Collapsed: sec.Collapsed, Monitors: []Monitor{}}
		for _, pm := range sec.Monitors {
			m, ok := data.ByID[pm.MonitorID]
			if !ok {
				continue
			}
			mon := b.monitor(m, live[m.ID], stats[m.ID], dates)
			if pm.DisplayName != nil && *pm.DisplayName != "" {
				mon.Name = *pm.DisplayName
			}
			ov.apply(&mon, data.InMaintenance[m.ID])
			out.Monitors = append(out.Monitors, mon)
			if !counted[m.ID] { // a monitor listed twice counts once
				counted[m.ID] = true
				p.Summary.add(mon.Status)
			}
		}
		if len(out.Monitors) == 0 {
			continue
		}
		out.Status = worst(out.Monitors)
		p.Sections = append(p.Sections, out)
	}
	p.Overall = overall(p.Summary)
	return p, nil
}

func (b *Builder) monitor(m *store.Monitor, ls LiveStatus, ms map[string]store.DayStat, dates []string) Monitor {
	status := ls.Status
	if !m.Enabled {
		status = "paused"
	}
	mon := Monitor{
		ID: m.ID, Name: m.Name, Description: m.Description, Type: string(m.Type),
		Status: status, LatencyMS: ls.LatencyMS, Since: ls.Since, LastChecked: ls.LastChecked,
		Days:      make([]Day, len(dates)),
		Locations: m.MultiLocation && m.ShowLocations,
	}
	var up30, tot30, up90, tot90 int
	for i, d := range dates {
		ds, ok := ms[d]
		day := Day{Date: d, Uptime: -1}
		if ok && ds.Total > 0 {
			good := ds.Up + ds.Degraded
			day.Uptime = round2(float64(good) * 100 / float64(ds.Total))
			day.Down, day.Degraded, day.Total = ds.Down, ds.Degraded, ds.Total
			up90 += good
			tot90 += ds.Total
			if i >= len(dates)-30 {
				up30 += good
				tot30 += ds.Total
			}
		}
		mon.Days[i] = day
	}
	mon.Uptime30, mon.Uptime90 = pct(up30, tot30), pct(up90, tot90)
	return mon
}

func (s *Summary) add(status string) {
	s.Total++
	switch status {
	case "up":
		s.Up++
	case "degraded":
		s.Degraded++
	case "down":
		s.Down++
	case "maintenance":
		s.Maintenance++
	case "paused":
		s.Paused++
	default:
		s.Pending++
	}
}

// Publish builds and stores one page's snapshot in Redis.
func (b *Builder) Publish(ctx context.Context, pageID int64) error {
	p, err := b.Build(ctx, pageID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, gzip.BestSpeed)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	h := fnv.New64a()
	_, _ = h.Write(raw)
	etag := `"` + strconv.FormatUint(h.Sum64(), 36) + `"`

	_, err = b.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, rkeys.SnapshotJSON(pageID), raw, 0)
		pipe.Set(ctx, rkeys.SnapshotGzip(pageID), gz.Bytes(), 0)
		pipe.Set(ctx, rkeys.SnapshotETag(pageID), etag, 0)
		return nil
	})
	return err
}

// Drop removes a deleted page's snapshot.
func (b *Builder) Drop(ctx context.Context, pageID int64) {
	b.rdb.Del(ctx, rkeys.SnapshotJSON(pageID), rkeys.SnapshotGzip(pageID), rkeys.SnapshotETag(pageID))
}

// Run rebuilds dirty pages (max 1/s) and every page every 15s, on the leader only.
func (b *Builder) Run(ctx context.Context, isLeader func() bool) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !isLeader() {
			continue
		}
		// Read and clear the dirty set atomically (MULTI/EXEC).
		var dirtyCmd *redis.StringSliceCmd
		_, err := b.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
			dirtyCmd = p.SMembers(ctx, rkeys.SnapshotDirty)
			p.Del(ctx, rkeys.SnapshotDirty)
			return nil
		})
		if err != nil && ctx.Err() == nil {
			b.log.Error("read dirty pages", "err", err)
			continue
		}
		members := dirtyCmd.Val()
		data := b.cat.Get()
		pages := map[int64]bool{}
		all := time.Since(b.lastFull) > 15*time.Second
		for _, m := range members {
			if m == rkeys.AllPages {
				all = true
				continue
			}
			if id, err := strconv.ParseInt(m, 10, 64); err == nil {
				pages[id] = true
			}
		}
		if all {
			b.lastFull = time.Now()
			for _, pg := range data.Pages {
				pages[pg.ID] = true
			}
		}
		for id := range pages {
			if _, ok := data.PageByID[id]; !ok {
				continue
			}
			if err := b.Publish(ctx, id); err != nil && ctx.Err() == nil {
				b.log.Error("snapshot build failed", "page", id, "err", err)
			}
		}
	}
}

// InvalidateStats forces the next build to reload per-day stats.
func (b *Builder) InvalidateStats() {
	b.mu.Lock()
	b.stats = nil
	b.mu.Unlock()
}

var severity = map[string]int{"up": 0, "paused": 0, "pending": 1, "maintenance": 1, "degraded": 2, "down": 3}

func worst(ms []Monitor) string {
	w := "up"
	for _, m := range ms {
		if severity[m.Status] > severity[w] {
			w = m.Status
		}
	}
	if w == "pending" {
		return "up"
	}
	return w
}

func overall(s Summary) string {
	active := s.Total - s.Paused - s.Pending - s.Maintenance
	switch {
	case s.Total == 0 || (active == 0 && s.Maintenance == 0):
		return "unknown"
	case s.Down == 0 && s.Degraded == 0 && s.Maintenance > 0:
		return "maintenance"
	case s.Down == 0 && s.Degraded == 0:
		return "operational"
	case s.Down == 0:
		return "degraded"
	case s.Down*2 >= max(active, 1):
		return "major_outage"
	default:
		return "partial_outage"
	}
}

func pct(ok, total int) float64 {
	if total == 0 {
		return -1
	}
	return round2(float64(ok) * 100 / float64(total))
}

// round2 floors to two decimals so 99.996% never displays as 100%.
func round2(f float64) float64 { return math.Floor(f*100) / 100 }

// overlay applies incidents to monitor statuses and uptime-bar markers.
type overlay struct {
	impact  map[int64]string                // worst open-incident impact per monitor
	markers map[int64]map[int][]DayIncident // monitor -> day index -> incidents
}

func buildOverlay(incidents []store.Incident, today time.Time, dates []string) overlay {
	ov := overlay{impact: map[int64]string{}, markers: map[int64]map[int][]DayIncident{}}
	first := today.AddDate(0, 0, -(len(dates) - 1))
	now := time.Now().UTC()
	for _, in := range incidents {
		if in.Status == "cancelled" || (in.Kind == store.KindMaintenance && in.Status == "scheduled") {
			continue
		}
		start := in.CreatedAt
		if in.ScheduledStart != nil {
			start = *in.ScheduledStart
		}
		end := now
		switch {
		case in.ResolvedAt != nil:
			end = *in.ResolvedAt
		case in.Kind == store.KindMaintenance && in.ScheduledEnd != nil && in.ScheduledEnd.Before(now):
			end = *in.ScheduledEnd
		}
		mark := DayIncident{ID: in.ID, Title: in.Title, Impact: in.Impact, Kind: in.Kind}
		for _, m := range in.Monitors {
			if in.Kind == store.KindIncident && in.ResolvedAt == nil && severity[m.Impact] > severity[ov.impact[m.MonitorID]] {
				ov.impact[m.MonitorID] = m.Impact
			}
			for d := start.UTC().Truncate(24 * time.Hour); !d.After(end); d = d.AddDate(0, 0, 1) {
				idx := int(d.Sub(first).Hours() / 24)
				if idx < 0 || idx >= len(dates) {
					continue
				}
				if ov.markers[m.MonitorID] == nil {
					ov.markers[m.MonitorID] = map[int][]DayIncident{}
				}
				ov.markers[m.MonitorID][idx] = append(ov.markers[m.MonitorID][idx], mark)
			}
		}
	}
	return ov
}

func (ov overlay) apply(mon *Monitor, inMaintenance bool) {
	for idx, list := range ov.markers[mon.ID] {
		mon.Days[idx].Incidents = list
	}
	if mon.Status == "paused" {
		return
	}
	if inMaintenance {
		mon.Status = "maintenance"
		return
	}
	// A declared incident can make a monitor look worse, never better.
	if imp := ov.impact[mon.ID]; severity[imp] > severity[mon.Status] {
		mon.Status = imp
	}
}

func cmpTime(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return a.Compare(*b)
}
