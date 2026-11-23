package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"drix/server/internal/catalog"
	"drix/server/internal/cluster"
	"drix/server/internal/gateway"
	"drix/server/internal/notify"
	"drix/server/internal/rkeys"
	"drix/server/internal/snapshot"
	"drix/server/internal/store"
	"drix/shared/protocol"
)

func (a *API) changed(ctx context.Context) {
	// Reload our own catalog synchronously so the response reflects the
	// change, then tell the rest of the cluster.
	_ = a.Catalog.Reload(ctx)
	catalog.NotifyChanged(ctx, a.Redis)
}

// ---------- Overview ----------

func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := a.Catalog.Get()
	ids := make([]int64, 0, len(data.Monitors))
	checksPerMin := 0.0
	for _, m := range data.Monitors {
		if m.Enabled {
			ids = append(ids, m.ID)
			checksPerMin += 60 / float64(max(m.IntervalS, 1))
		}
	}
	live, _, err := snapshot.ReadStatuses(ctx, a.Redis, ids, false)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	counts := map[string]int{"up": 0, "degraded": 0, "down": 0, "pending": 0}
	var latSum float64
	var latN int
	for _, s := range live {
		counts[s.Status]++
		if s.Status == "up" || s.Status == "degraded" {
			latSum += s.LatencyMS
			latN++
		}
	}
	agents, err := a.Registry.List(ctx)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	registered, err := a.Store.ListAgents(ctx)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	open := true
	openIncidents, err := a.Store.ListIncidents(ctx, store.IncidentFilter{Open: &open, Limit: 20})
	if err != nil {
		a.serverErr(w, err)
		return
	}
	events, err := a.Store.ListEvents(ctx, 0, nil, 12)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	avg := 0.0
	if latN > 0 {
		avg = latSum / float64(latN)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"monitors": map[string]any{
			"total": len(data.Monitors), "enabled": len(ids), "paused": len(data.Monitors) - len(ids),
			"up": counts["up"], "degraded": counts["degraded"], "down": counts["down"], "pending": counts["pending"],
		},
		"groups":         len(data.Sections[pageIDOf(data.DefaultPage())]),
		"agents_online":  len(agents),
		"agents_total":   len(registered),
		"avg_latency_ms": avg,
		"checks_per_min": checksPerMin,
		"events":         events,
		"viewers":        a.Hub.Clients(),
		"incidents":      openIncidents,
	})
}

// ---------- Monitors ----------

type monitorInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Type        string          `json:"type"`
	Target      string          `json:"target"`
	Config      json.RawMessage `json:"config"`
	IntervalS   int             `json:"interval_s"`
	TimeoutMS   int             `json:"timeout_ms"`
	Retries     *int            `json:"retries"`
	DegradedMS  int             `json:"degraded_ms"`
	Enabled     *bool           `json:"enabled"`
	// MultiLocation checks from every location; ShowLocations lists them on
	// the status page (nil keeps the default: on).
	MultiLocation bool  `json:"multi_location"`
	ShowLocations *bool `json:"show_locations"`
	// AutoIncident (default on): may post automatic status page incidents.
	AutoIncident *bool `json:"auto_incident"`
	// SectionID optionally places a new monitor on a status page section.
	SectionID *int64 `json:"section_id"`
	// ChannelIDs selects alert channels that apply to specific monitors;
	// nil leaves them unchanged. Channels set to "all monitors" always apply.
	ChannelIDs *[]int64 `json:"channel_ids"`
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9\-\.]*[A-Za-z0-9])?$`)

func validHost(h string) bool {
	if net.ParseIP(h) != nil {
		return true
	}
	return len(h) <= 253 && hostRe.MatchString(h)
}

// strictConfig decodes config into T rejecting unknown fields, then re-encodes it.
func strictConfig[T any](raw json.RawMessage) (T, json.RawMessage, error) {
	var v T
	if len(raw) > 0 && string(raw) != "null" {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil {
			return v, nil, fmt.Errorf("invalid config: %w", err)
		}
	}
	out, err := json.Marshal(v)
	return v, out, err
}

type fieldErr struct{ field, msg string }

func (e fieldErr) Error() string { return e.msg }

func (a *API) validateMonitor(in monitorInput) (store.Monitor, error) {
	settings := a.Catalog.Get().Settings
	m := store.Monitor{
		Name: strings.TrimSpace(in.Name), Description: strings.TrimSpace(in.Description),
		Type: protocol.MonitorType(in.Type), Target: strings.TrimSpace(in.Target),
		IntervalS: in.IntervalS, TimeoutMS: in.TimeoutMS, DegradedMS: in.DegradedMS,
		Retries: 1, Enabled: true, MultiLocation: in.MultiLocation, ShowLocations: true, AutoIncident: true,
	}
	if in.AutoIncident != nil {
		m.AutoIncident = *in.AutoIncident
	}
	if in.ShowLocations != nil {
		m.ShowLocations = *in.ShowLocations
	}
	if in.Retries != nil {
		m.Retries = *in.Retries
	}
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	if m.IntervalS == 0 {
		m.IntervalS = max(settings.DefaultInterval, 5)
	}
	if m.TimeoutMS == 0 {
		m.TimeoutMS = min(max(settings.DefaultTimeout, 100), m.IntervalS*1000)
	}

	switch {
	case m.Name == "" || len(m.Name) > 100:
		return m, fieldErr{"name", "Name is required (max 100 characters)"}
	case len(m.Description) > 500:
		return m, fieldErr{"description", "Description is too long"}
	case m.IntervalS < 5 || m.IntervalS > 86400:
		return m, fieldErr{"interval_s", "Interval must be between 5 seconds and 24 hours"}
	case m.TimeoutMS < 100 || m.TimeoutMS > 120000:
		return m, fieldErr{"timeout_ms", "Timeout must be between 100 ms and 120 s"}
	case m.TimeoutMS > m.IntervalS*1000:
		return m, fieldErr{"timeout_ms", "Timeout must be shorter than the interval"}
	case m.Retries < 0 || m.Retries > 10:
		return m, fieldErr{"retries", "Retries must be between 0 and 10"}
	case m.DegradedMS < 0:
		return m, fieldErr{"degraded_ms", "Degraded threshold cannot be negative"}
	case m.MultiLocation && m.Type == protocol.TypePush:
		return m, fieldErr{"multi_location", "Heartbeat monitors are checked by the server, not from locations"}
	}
	if in.SectionID != nil && a.sectionByID(*in.SectionID) == nil {
		return m, fieldErr{"section_id", "That group no longer exists"}
	}

	var err error
	switch m.Type {
	case protocol.TypeHTTP:
		var c protocol.HTTPConfig
		c, m.Config, err = strictConfig[protocol.HTTPConfig](in.Config)
		if err != nil {
			return m, fieldErr{"config", err.Error()}
		}
		u, perr := url.Parse(m.Target)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return m, fieldErr{"target", "Enter a full URL starting with http:// or https://"}
		}
		if c.Method != "" && !slicesContains([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, strings.ToUpper(c.Method)) {
			return m, fieldErr{"config.method", "Unsupported HTTP method"}
		}
		if c.ExpectStatusMin != 0 && c.ExpectStatusMax != 0 && c.ExpectStatusMin > c.ExpectStatusMax {
			return m, fieldErr{"config.expect_status_min", "Status range is inverted"}
		}
	case protocol.TypeTCP:
		var c protocol.TCPConfig
		c, m.Config, err = strictConfig[protocol.TCPConfig](in.Config)
		if err != nil {
			return m, fieldErr{"config", err.Error()}
		}
		host := m.Target
		if h, p, serr := net.SplitHostPort(m.Target); serr == nil {
			host = h
			if n, _ := strconv.Atoi(p); n < 1 || n > 65535 {
				return m, fieldErr{"target", "Port must be 1-65535"}
			}
		} else if c.Port < 1 || c.Port > 65535 {
			return m, fieldErr{"config.port", "Set a port (1-65535) or use host:port"}
		}
		if !validHost(host) {
			return m, fieldErr{"target", "Enter a valid hostname or IP address"}
		}
	case protocol.TypePing:
		var c protocol.PingConfig
		c, m.Config, err = strictConfig[protocol.PingConfig](in.Config)
		if err != nil {
			return m, fieldErr{"config", err.Error()}
		}
		if c.Count < 0 || c.Count > 10 {
			return m, fieldErr{"config.count", "Packet count must be 1-10"}
		}
		if !validHost(m.Target) {
			return m, fieldErr{"target", "Enter a valid hostname or IP address"}
		}
	case protocol.TypeDNS:
		var c protocol.DNSConfig
		c, m.Config, err = strictConfig[protocol.DNSConfig](in.Config)
		if err != nil {
			return m, fieldErr{"config", err.Error()}
		}
		if !validHost(strings.TrimSuffix(m.Target, ".")) {
			return m, fieldErr{"target", "Enter a valid domain name"}
		}
		if rt := strings.ToUpper(c.RecordType); rt != "" && !slicesContains([]string{"A", "AAAA", "CNAME", "MX", "TXT", "NS"}, rt) {
			return m, fieldErr{"config.record_type", "Unsupported record type"}
		}
	case protocol.TypeSSL:
		var c protocol.SSLConfig
		c, m.Config, err = strictConfig[protocol.SSLConfig](in.Config)
		if err != nil {
			return m, fieldErr{"config", err.Error()}
		}
		if !validHostPort(m.Target, false) {
			return m, fieldErr{"target", "Enter a hostname, optionally with :port (default 443)"}
		}
		warn, crit := orDefault(c.WarnDays, 14), orDefault(c.CritDays, 7)
		if warn < 1 || warn > 365 || crit < 0 || crit > 365 || crit >= warn {
			return m, fieldErr{"config.warn_days", "Warning days must be 1-365 and more than the critical days"}
		}
	case protocol.TypeJSON:
		var c protocol.JSONConfig
		c, m.Config, err = strictConfig[protocol.JSONConfig](in.Config)
		if err != nil {
			return m, fieldErr{"config", err.Error()}
		}
		if u, perr := url.Parse(m.Target); perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return m, fieldErr{"target", "Enter a full URL starting with http:// or https://"}
		}
		if len(c.Assertions) == 0 || len(c.Assertions) > 20 {
			return m, fieldErr{"config.assertions", "Add between 1 and 20 assertions"}
		}
		ops := []string{"equals", "not_equals", "contains", "gt", "lt", "exists", "not_exists"}
		for _, as := range c.Assertions {
			if strings.TrimSpace(as.Path) == "" || !slicesContains(ops, as.Op) {
				return m, fieldErr{"config.assertions", "Every assertion needs a path and a valid comparison"}
			}
			if (as.Op == "gt" || as.Op == "lt") && !isNumber(as.Value) {
				return m, fieldErr{"config.assertions", "Greater/less than needs a number to compare with"}
			}
		}
	case protocol.TypeSMTP:
		if _, m.Config, err = strictConfig[protocol.SMTPConfig](in.Config); err != nil {
			return m, fieldErr{"config", err.Error()}
		}
		if !validHostPort(m.Target, false) {
			return m, fieldErr{"target", "Enter a mail server hostname, optionally with :port (default 25)"}
		}
	case protocol.TypeGRPC:
		if _, m.Config, err = strictConfig[protocol.GRPCConfig](in.Config); err != nil {
			return m, fieldErr{"config", err.Error()}
		}
		if !validHostPort(m.Target, true) {
			return m, fieldErr{"target", "Enter host:port, e.g. api.internal:50051"}
		}
	case protocol.TypePush:
		var c protocol.PushConfig
		c, m.Config, err = strictConfig[protocol.PushConfig](in.Config)
		if err != nil {
			return m, fieldErr{"config", err.Error()}
		}
		if c.GraceS < 0 || c.GraceS > 86400 {
			return m, fieldErr{"config.grace_s", "Grace period must be between 0 and 86400 seconds"}
		}
		m.Target = "" // the monitored system calls us
		tok := newPushToken()
		m.PushToken = &tok // used on create; updates keep the existing token
	default:
		return m, fieldErr{"type", "Choose a check type"}
	}
	return m, nil
}

func validHostPort(target string, requirePort bool) bool {
	host := target
	if h, p, err := net.SplitHostPort(target); err == nil {
		n, _ := strconv.Atoi(p)
		if n < 1 || n > 65535 {
			return false
		}
		host = h
	} else if requirePort {
		return false
	}
	return validHost(host)
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func writeValidation(w http.ResponseWriter, err error) {
	var fe fieldErr
	if errors.As(err, &fe) {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: fe.msg, Field: map[string]string{fe.field: fe.msg}})
		return
	}
	writeErr(w, http.StatusUnprocessableEntity, err.Error())
}

type adminMonitor struct {
	store.Monitor
	Live     snapshot.LiveStatus `json:"live"`
	Spark    []float64           `json:"spark"`
	Uptime30 float64             `json:"uptime_30"`
	Owner    string              `json:"owner,omitempty"`
	Groups   []groupRef          `json:"groups"`
	// ChannelIDs are the specific (not "all monitors") channels for this monitor.
	ChannelIDs []int64 `json:"channel_ids"`
	// Ack is who acknowledged the current outage (down monitors only).
	Ack *notify.Ack `json:"ack,omitempty"`
}

func (a *API) monitorChannels(r *http.Request) map[int64][]int64 {
	out := map[int64][]int64{}
	chs, err := a.Store.ListChannels(r.Context())
	if err != nil {
		return out
	}
	for _, c := range chs {
		if c.ApplyToAll {
			continue
		}
		for _, m := range c.MonitorIDs {
			out[m] = append(out[m], c.ID)
		}
	}
	return out
}

func (a *API) applyMonitorChannels(r *http.Request, id int64, in monitorInput) error {
	if in.ChannelIDs == nil {
		return nil
	}
	if err := a.Store.SetMonitorChannels(r.Context(), id, *in.ChannelIDs); err != nil {
		return err
	}
	if a.Notify != nil {
		a.Notify.InvalidateChannels()
	}
	return nil
}

// groupRef names a group (section of the status page) a monitor appears in.
type groupRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (a *API) groupsOf(id int64) []groupRef {
	data := a.Catalog.Get()
	out := []groupRef{}
	if p := data.DefaultPage(); p != nil {
		for _, sec := range data.Sections[p.ID] {
			for _, pm := range sec.Monitors {
				if pm.MonitorID == id {
					out = append(out, groupRef{ID: sec.ID, Name: sec.Name})
					break
				}
			}
		}
	}
	return out
}

func (a *API) sectionByID(id int64) *store.Section {
	for _, secs := range a.Catalog.Get().Sections {
		for i := range secs {
			if secs[i].ID == id {
				return &secs[i]
			}
		}
	}
	return nil
}

func (a *API) listMonitors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := a.Catalog.Get()
	ids := make([]int64, len(data.Monitors))
	for i, m := range data.Monitors {
		ids[i] = m.ID
	}
	live, spark, err := snapshot.ReadStatuses(ctx, a.Redis, ids, true)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	acks := a.Notify.Acks(ctx, live)
	uptime, err := a.uptimeSince(ctx, 30)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	liveAgents, _ := a.Registry.List(ctx)
	liveIDs := make([]string, 0, len(liveAgents))
	locations := map[string]bool{}
	for _, ag := range liveAgents {
		liveIDs = append(liveIDs, ag.ID)
		locations[cluster.Location(ag.Region, ag.Name)] = true
	}
	enabled := make([]int64, 0, len(data.Monitors))
	for _, m := range data.Monitors {
		if m.Enabled && m.Type.AgentRuns() && !m.MultiLocation {
			enabled = append(enabled, m.ID)
		}
	}
	owners := cluster.Owners(enabled, liveIDs)
	names := a.agentNames(ctx)
	mch := a.monitorChannels(r)

	out := make([]adminMonitor, len(data.Monitors))
	for i, m := range data.Monitors {
		ls := live[m.ID]
		if !m.Enabled {
			ls.Status = "paused"
		} else if data.InMaintenance[m.ID] {
			ls.Status = "maintenance"
		}
		u, ok := uptime[m.ID]
		if !ok {
			u = -1
		}
		owner := ""
		switch {
		case !m.Type.AgentRuns():
			owner = "Heartbeat (server)"
		case m.Enabled && m.MultiLocation:
			owner = fmt.Sprintf("All locations (%d)", len(locations))
		case m.Enabled:
			owner = names[owners[m.ID]]
		}
		out[i] = adminMonitor{Monitor: m, Live: ls, Spark: spark[m.ID], Uptime30: u, Owner: owner, Groups: a.groupsOf(m.ID), ChannelIDs: orEmpty(mch[m.ID])}
		if ack, ok := acks[m.ID]; ok {
			out[i].Ack = &ack
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) agentNames(ctx context.Context) map[string]string {
	agents, _ := a.Store.ListAgents(ctx)
	m := make(map[string]string, len(agents))
	for _, ag := range agents {
		m[ag.ID] = ag.Name
	}
	return m
}

func (a *API) uptimeSince(ctx context.Context, days int) (map[int64]float64, error) {
	stats, err := a.Store.DailyStatsSince(ctx, time.Now().UTC().AddDate(0, 0, -days+1).Truncate(24*time.Hour))
	if err != nil {
		return nil, err
	}
	ok, tot := map[int64]int{}, map[int64]int{}
	for _, s := range stats {
		ok[s.MonitorID] += s.Up + s.Degraded
		tot[s.MonitorID] += s.Total
	}
	out := make(map[int64]float64, len(tot))
	for id, t := range tot {
		if t > 0 {
			out[id] = float64(int64(float64(ok[id])*10000/float64(t))) / 100
		}
	}
	return out, nil
}

func (a *API) getMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	m, err := a.Store.GetMonitor(r.Context(), id)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	live, spark, err := snapshot.ReadStatuses(r.Context(), a.Redis, []int64{id}, true)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	ls := live[id]
	if !m.Enabled {
		ls.Status = "paused"
	} else if a.Catalog.Get().InMaintenance[id] {
		ls.Status = "maintenance"
	}
	uptime, _ := a.uptimeSince(r.Context(), 30)
	u, found := uptime[id]
	if !found {
		u = -1
	}
	out := adminMonitor{Monitor: m, Live: ls, Spark: spark[id], Uptime30: u, Groups: a.groupsOf(id), ChannelIDs: orEmpty(a.monitorChannels(r)[id])}
	if ack, ok := a.Notify.Acks(r.Context(), live)[id]; ok {
		out.Ack = &ack
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) createMonitor(w http.ResponseWriter, r *http.Request) {
	var in monitorInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	m, err := a.validateMonitor(in)
	if err != nil {
		writeValidation(w, err)
		return
	}
	m, err = a.Store.CreateMonitor(r.Context(), m)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if in.SectionID != nil {
		if err := a.Store.AddMonitorToSection(r.Context(), *in.SectionID, m.ID); err != nil {
			a.serverErr(w, err)
			return
		}
	}
	if err := a.applyMonitorChannels(r, m.ID, in); err != nil {
		a.serverErr(w, err)
		return
	}
	a.changed(r.Context())
	a.audit(r, "monitor.create", "monitor", m.ID, fmt.Sprintf("Created %s (%s %s)", m.Name, m.Type, m.Target))
	writeJSON(w, http.StatusCreated, m)
}

func (a *API) updateMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in monitorInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	m, err := a.validateMonitor(in)
	if err != nil {
		writeValidation(w, err)
		return
	}
	prev, err := a.Store.GetMonitor(r.Context(), id)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	m.ID = id
	m, err = a.Store.UpdateMonitor(r.Context(), m)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if prev.Enabled && !m.Enabled || prev.Type != m.Type || prev.Target != m.Target {
		a.resetLive(r.Context(), id)
	}
	if err := a.applyMonitorChannels(r, id, in); err != nil {
		a.serverErr(w, err)
		return
	}
	a.changed(r.Context())
	a.audit(r, "monitor.update", "monitor", m.ID, "Edited "+m.Name)
	writeJSON(w, http.StatusOK, m)
}

// resetLive clears live state so a paused/retargeted monitor starts fresh.
func (a *API) resetLive(ctx context.Context, ids ...int64) {
	keys := make([]string, 0, len(ids)*2)
	for _, id := range ids {
		keys = append(keys, rkeys.Status(id), rkeys.Latency(id))
	}
	a.Redis.Del(ctx, keys...)
	a.Notify.ClearOutage(ctx, ids...)
}

func (a *API) deleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	n, err := a.Store.DeleteMonitors(r.Context(), []int64{id})
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if n == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	a.resetLive(r.Context(), id)
	a.Snapshot.InvalidateStats()
	a.changed(r.Context())
	a.audit(r, "monitor.delete", "monitor", id, fmt.Sprintf("Deleted monitor #%d", id))
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) bulkMonitors(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string  `json:"action"`
		IDs    []int64 `json:"ids"`
	}
	if err := readJSON(r, &in); err != nil || len(in.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx := r.Context()
	var err error
	switch in.Action {
	case "pause":
		err = a.Store.SetMonitorsEnabled(ctx, in.IDs, false)
		a.resetLive(ctx, in.IDs...)
	case "resume":
		err = a.Store.SetMonitorsEnabled(ctx, in.IDs, true)
	case "delete":
		_, err = a.Store.DeleteMonitors(ctx, in.IDs)
		a.resetLive(ctx, in.IDs...)
		a.Snapshot.InvalidateStats()
	default:
		writeErr(w, http.StatusBadRequest, "unknown action")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.changed(ctx)
	a.audit(r, "monitor."+in.Action, "monitor", joinIDs(in.IDs), fmt.Sprintf("%s %d monitor(s)", in.Action, len(in.IDs)))
	w.WriteHeader(http.StatusNoContent)
}

func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return truncateStr(strings.Join(parts, ","), 200)
}

func (a *API) testMonitor(w http.ResponseWriter, r *http.Request) {
	var in monitorInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	m, err := a.validateMonitor(in)
	if err != nil {
		writeValidation(w, err)
		return
	}
	if !m.Type.AgentRuns() {
		writeErr(w, http.StatusBadRequest, "Heartbeat monitors are checked by pinging their URL, so there's nothing to test here.")
		return
	}
	res, agent, err := a.Gateway.Test(r.Context(), m.Spec())
	if err != nil {
		if errors.Is(err, gateway.ErrNoAgents) {
			writeErr(w, http.StatusServiceUnavailable, "No workers are online. Add a worker to run checks.")
			return
		}
		writeErr(w, http.StatusGatewayTimeout, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "agent": agent})
}

func (a *API) monitorResults(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := a.Store.RecentResults(r.Context(), id, queryInt(r, "limit", 50, 1, 500))
	if err != nil {
		a.serverErr(w, err)
		return
	}
	names := a.agentNames(r.Context())
	type row struct {
		store.ResultRow
		AgentName string `json:"agent_name"`
	}
	out := make([]row, len(rows))
	for i, rr := range rows {
		out[i] = row{ResultRow: rr}
		if rr.AgentID != nil {
			out[i].AgentName = names[*rr.AgentID]
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) adminLatency(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	a.latency(w, r, id)
}

// ---------- Agents ----------

type adminAgent struct {
	store.Agent
	Online bool               `json:"online"`
	Live   *cluster.LiveAgent `json:"live,omitempty"`
	Owned  int                `json:"owned"`
}

func (a *API) listAgents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agents, err := a.Store.ListAgents(ctx)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	live, err := a.Registry.List(ctx)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	liveBy := map[string]*cluster.LiveAgent{}
	liveIDs := make([]string, 0, len(live))
	for i := range live {
		liveBy[live[i].ID] = &live[i]
		liveIDs = append(liveIDs, live[i].ID)
	}
	sort.Strings(liveIDs)
	data := a.Catalog.Get()
	var enabled []int64
	for _, m := range data.Monitors {
		if m.Enabled && m.Type.AgentRuns() {
			enabled = append(enabled, m.ID)
		}
	}
	parts := cluster.Assign(enabled, liveIDs)

	out := make([]adminAgent, len(agents))
	for i, ag := range agents {
		la := liveBy[ag.ID]
		out[i] = adminAgent{Agent: ag, Online: la != nil, Live: la, Owned: len(parts[ag.ID])}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agents":         out,
		"total_monitors": len(enabled),
		"public_url":     a.Cfg.PublicURL,
	})
}

type agentInput struct {
	Name     string `json:"name"`
	Region   string `json:"region"`
	Disabled bool   `json:"disabled"`
}

func (in *agentInput) validate() error {
	in.Name, in.Region = strings.TrimSpace(in.Name), strings.TrimSpace(in.Region)
	if in.Name == "" || len(in.Name) > 60 {
		return errors.New("name is required (max 60 characters)")
	}
	if len(in.Region) > 40 {
		return errors.New("region is too long")
	}
	return nil
}

func (a *API) createAgent(w http.ResponseWriter, r *http.Request) {
	var in agentInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	tok := gateway.NewToken()
	ag, err := a.Store.CreateAgent(r.Context(), in.Name, in.Region, gateway.HashToken(tok), tok[:10])
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.audit(r, "worker.create", "worker", ag.ID, "Added worker "+ag.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"agent": ag, "token": tok})
}

func (a *API) updateAgent(w http.ResponseWriter, r *http.Request) {
	var in agentInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	ag, err := a.Store.UpdateAgent(r.Context(), r.PathValue("id"), in.Name, in.Region, in.Disabled)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if ag.Disabled {
		a.Gateway.Disconnect(r.Context(), ag.ID)
	}
	a.audit(r, "worker.update", "worker", ag.ID, fmt.Sprintf("Updated worker %s (disabled=%v)", ag.Name, ag.Disabled))
	writeJSON(w, http.StatusOK, ag)
}

func (a *API) rotateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tok := gateway.NewToken()
	_, err := a.Store.DB.Exec(r.Context(), `UPDATE agents SET token_hash=$2, token_prefix=$3 WHERE id=$1::uuid`,
		id, gateway.HashToken(tok), tok[:10])
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.Gateway.Disconnect(r.Context(), id)
	a.audit(r, "worker.rotate", "worker", id, "Issued a new worker token")
	writeJSON(w, http.StatusOK, map[string]any{"token": tok})
}

func (a *API) deleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Store.DeleteAgent(r.Context(), id); err != nil {
		a.serverErr(w, err)
		return
	}
	a.Gateway.Disconnect(r.Context(), id)
	a.audit(r, "worker.delete", "worker", id, "Removed a worker")
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Events & settings ----------

func (a *API) adminEvents(w http.ResponseWriter, r *http.Request) {
	mid, _ := strconv.ParseInt(r.URL.Query().Get("monitor_id"), 10, 64)
	evs, err := a.Store.ListEvents(r.Context(), mid, nil, queryInt(r, "limit", 50, 1, 500))
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, evs)
}

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := a.Store.GetSettings(r.Context())
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	var s store.Settings
	if err := readJSON(r, &s); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	switch {
	case s.RetentionDays < 1 || s.RetentionDays > 365:
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Invalid retention", Field: map[string]string{"retention_days": "Between 1 and 365 days"}})
		return
	case s.DefaultInterval < 5 || s.DefaultInterval > 86400:
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Invalid interval", Field: map[string]string{"default_interval_s": "Between 5 and 86400 seconds"}})
		return
	case s.DefaultTimeout < 100 || s.DefaultTimeout > 120000:
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Invalid timeout", Field: map[string]string{"default_timeout_ms": "Between 100 and 120000 ms"}})
		return
	}
	if err := a.Store.SaveSettings(r.Context(), s); err != nil {
		a.serverErr(w, err)
		return
	}
	a.changed(r.Context())
	a.Redis.Publish(r.Context(), rkeys.ChanSettingsChanged, "1")
	a.audit(r, "settings.update", "settings", nil, "Updated settings")
	writeJSON(w, http.StatusOK, s)
}

func orEmpty(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

func pageIDOf(p *store.Page) int64 {
	if p == nil {
		return 0
	}
	return p.ID
}
