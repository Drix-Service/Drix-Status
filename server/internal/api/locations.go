package api

import (
	"net/http"
	"time"

	"drix/server/internal/cluster"
	"drix/server/internal/pipeline"
)

// Per-location state of a monitor that is checked from every location.

type locationOut struct {
	Location    string     `json:"location"`
	Status      string     `json:"status"`
	LatencyMS   float64    `json:"latency_ms"`
	Message     string     `json:"message,omitempty"`
	LastChecked *time.Time `json:"last_checked,omitempty"`
	Stale       bool       `json:"stale,omitempty"` // no recent results: not counted
	Worker      string     `json:"worker,omitempty"`
	Avg24hMS    float64    `json:"avg_24h_ms"`
	Uptime24h   float64    `json:"uptime_24h"` // percent; -1 without checks
}

type locationsResponse struct {
	MultiLocation bool          `json:"multi_location"`
	Available     []string      `json:"available"` // locations with a live worker
	Locations     []locationOut `json:"locations"`
}

// monitorLocations builds the per-location view; admin adds messages and
// worker names.
func (a *API) monitorLocations(r *http.Request, id int64, admin bool) (locationsResponse, error) {
	ctx := r.Context()
	data := a.Catalog.Get()
	m := data.ByID[id]
	out := locationsResponse{Available: []string{}, Locations: []locationOut{}}
	agents, _ := a.Registry.List(ctx)
	seen := map[string]bool{}
	for _, ag := range agents {
		if l := cluster.Location(ag.Region, ag.Name); !seen[l] {
			seen[l] = true
			out.Available = append(out.Available, l)
		}
	}
	if m == nil || !m.MultiLocation {
		return out, nil
	}
	out.MultiLocation = true
	states, err := pipeline.ReadLocations(ctx, a.Redis, id)
	if err != nil {
		return out, err
	}
	stats, err := a.Store.LocationStats(ctx, id, time.Now().Add(-24*time.Hour))
	if err != nil {
		return out, err
	}
	byLoc := map[string]int{}
	for i, s := range stats {
		byLoc[s.Location] = i
	}
	cutoff := time.Now().Add(-(3*time.Duration(m.IntervalS)*time.Second + 30*time.Second)).UnixMilli()
	for _, s := range states {
		lo := locationOut{Location: s.Location, Status: s.Status, LatencyMS: s.Latency, Uptime24h: -1, Stale: s.At < cutoff}
		if s.At > 0 {
			t := time.UnixMilli(s.At).UTC()
			lo.LastChecked = &t
		}
		if i, ok := byLoc[s.Location]; ok {
			lo.Avg24hMS, lo.Uptime24h = stats[i].AvgMS, stats[i].Uptime
		}
		if admin {
			lo.Message = s.Message
		}
		if lo.Status == "down" {
			lo.LatencyMS = 0
		}
		out.Locations = append(out.Locations, lo)
	}
	return out, nil
}

func (a *API) adminLocations(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	out, err := a.monitorLocations(r, id, true)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// publicLocations serves locations for public monitors that show them.
func (a *API) publicLocations(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	data := a.Catalog.Get()
	m := data.ByID[id]
	if !ok || m == nil || !data.IsPublic(id) || !m.MultiLocation || !m.ShowLocations {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	out, err := a.monitorLocations(r, id, false)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	out.Available = nil // internal detail
	w.Header().Set("Cache-Control", "public, max-age=15")
	writeJSON(w, http.StatusOK, out)
}
