package snapshot

import (
	"testing"
	"time"

	"drix/server/internal/store"
)

func TestOverall(t *testing.T) {
	cases := []struct {
		s    Summary
		want string
	}{
		{Summary{Total: 0}, "unknown"},
		{Summary{Total: 3, Up: 3}, "operational"},
		{Summary{Total: 3, Up: 2, Degraded: 1}, "degraded"},
		{Summary{Total: 4, Up: 3, Down: 1}, "partial_outage"},
		{Summary{Total: 4, Up: 2, Down: 2}, "major_outage"},
		{Summary{Total: 3, Up: 2, Maintenance: 1}, "maintenance"},
		{Summary{Total: 2, Maintenance: 2}, "maintenance"},
		{Summary{Total: 3, Up: 1, Down: 1, Maintenance: 1}, "major_outage"},
		{Summary{Total: 2, Pending: 2}, "unknown"},
	}
	for _, c := range cases {
		if got := overall(c.s); got != c.want {
			t.Errorf("overall(%+v) = %s, want %s", c.s, got, c.want)
		}
	}
}

func TestOverlay(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	dates := make([]string, Days)
	for i := range Days {
		dates[i] = today.AddDate(0, 0, -(Days - 1 - i)).Format("2006-01-02")
	}
	threeDaysAgo := today.AddDate(0, 0, -3).Add(10 * time.Hour)
	resolved := threeDaysAgo.Add(26 * time.Hour) // spans two days
	incidents := []store.Incident{
		{ID: 1, Kind: store.KindIncident, Title: "API errors", Impact: "major", Status: "resolved",
			CreatedAt: threeDaysAgo, ResolvedAt: &resolved, Monitors: []store.IncidentMonitor{{MonitorID: 10, Impact: "down"}}},
		{ID: 2, Kind: store.KindIncident, Title: "Slow DB", Impact: "minor", Status: "investigating",
			CreatedAt: time.Now().Add(-time.Hour), Monitors: []store.IncidentMonitor{{MonitorID: 20, Impact: "degraded"}}},
		{ID: 3, Kind: store.KindMaintenance, Title: "Upgrade", Status: "scheduled",
			Monitors: []store.IncidentMonitor{{MonitorID: 30, Impact: "maintenance"}}},
	}
	ov := buildOverlay(incidents, today, dates)

	mon := func(id int64, status string) Monitor { return Monitor{ID: id, Status: status, Days: make([]Day, Days)} }

	// Resolved incident marks both days it spanned and doesn't change status.
	m := mon(10, "up")
	ov.apply(&m, false)
	if m.Status != "up" {
		t.Fatalf("resolved incident changed status to %s", m.Status)
	}
	marked := 0
	for _, d := range m.Days {
		if len(d.Incidents) > 0 {
			marked++
		}
	}
	if marked != 2 || m.Days[Days-4].Incidents[0].Title != "API errors" {
		t.Fatalf("expected 2 marked days starting 3 days ago, got %d", marked)
	}

	// Open incident makes a healthy monitor look degraded...
	m = mon(20, "up")
	ov.apply(&m, false)
	if m.Status != "degraded" {
		t.Fatalf("open incident: got %s, want degraded", m.Status)
	}
	// ...but never better than reality.
	m = mon(20, "down")
	ov.apply(&m, false)
	if m.Status != "down" {
		t.Fatalf("incident must not improve status, got %s", m.Status)
	}
	// Maintenance overrides; scheduled (not started) windows don't mark bars.
	m = mon(30, "down")
	ov.apply(&m, true)
	if m.Status != "maintenance" {
		t.Fatalf("maintenance: got %s", m.Status)
	}
	for _, d := range m.Days {
		if len(d.Incidents) > 0 {
			t.Fatal("a scheduled window must not mark uptime bars yet")
		}
	}
	// Paused stays paused.
	m = mon(30, "paused")
	ov.apply(&m, true)
	if m.Status != "paused" {
		t.Fatalf("paused monitor became %s", m.Status)
	}
}

func TestUptimeIgnoresMaintenance(t *testing.T) {
	b := &Builder{}
	today := time.Now().UTC().Format("2006-01-02")
	// 10 counted checks (9 good), plus 50 failed checks during maintenance
	// which are stored outside Total.
	stats := map[string]store.DayStat{today: {Total: 10, Up: 9, Down: 1, Maintenance: 50}}
	m := b.monitor(&store.Monitor{ID: 1, Enabled: true}, LiveStatus{Status: "up"}, stats, []string{today})
	if m.Days[0].Uptime != 90 || m.Uptime90 != 90 {
		t.Fatalf("uptime = %v / %v, want 90", m.Days[0].Uptime, m.Uptime90)
	}
}
