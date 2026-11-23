// Package autoinc posts incidents on the status page by itself when a public
// monitor has been down for a while, and resolves them when it's back, so
// visitors see what's happening even when nobody is awake to write it.
package autoinc

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/catalog"
	"drix/server/internal/snapshot"
	"drix/server/internal/store"
)

// Settings are stored under the "auto_incidents" key.
type Settings struct {
	Enabled           bool `json:"enabled"`
	AfterMin          int  `json:"after_min"`
	NotifySubscribers bool `json:"notify_subscribers"`
}

func Defaults() Settings { return Settings{AfterMin: 5} }

// A monitor must be back for this long before the incident is resolved, so a
// flapping service doesn't open and close incidents.
const recoverFor = 2 * time.Minute

type Runner struct {
	st  *store.Store
	rdb *redis.Client
	cat *catalog.Catalog
	log *slog.Logger

	// OnIncident emails subscribers (when the incident asks for it).
	OnIncident func(ctx context.Context, inc store.Incident, event string, u *store.IncidentUpdate)

	now func() time.Time
}

func New(st *store.Store, rdb *redis.Client, cat *catalog.Catalog, log *slog.Logger) *Runner {
	return &Runner{st: st, rdb: rdb, cat: cat, log: log.With("component", "autoinc"), now: time.Now}
}

func (r *Runner) Settings(ctx context.Context) (Settings, error) {
	s := Defaults()
	_, err := r.st.GetJSONSetting(ctx, "auto_incidents", &s)
	return s, err
}

func (r *Runner) SaveSettings(ctx context.Context, s Settings) error {
	return r.st.SaveJSONSetting(ctx, "auto_incidents", s)
}

// Run checks every 30 seconds on the leader.
func (r *Runner) Run(ctx context.Context, isLeader func() bool) {
	t := time.NewTicker(30 * time.Second)
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
		if err := r.Tick(ctx); err != nil {
			r.log.Warn("automatic incidents", "err", err)
		}
	}
}

// publicMonitors lists the page's monitors in display order with their
// public names.
func publicMonitors(data *catalog.Data, pageID int64) ([]int64, map[int64]string) {
	var ids []int64
	names := map[int64]string{}
	for _, sec := range data.Sections[pageID] {
		for _, pm := range sec.Monitors {
			m := data.ByID[pm.MonitorID]
			if m == nil {
				continue
			}
			if _, seen := names[m.ID]; seen {
				continue
			}
			name := m.Name
			if pm.DisplayName != nil && *pm.DisplayName != "" {
				name = *pm.DisplayName
			}
			names[m.ID] = name
			ids = append(ids, m.ID)
		}
	}
	return ids, names
}

// Tick looks at public monitors once: opens or extends the automatic
// incident, or resolves it.
func (r *Runner) Tick(ctx context.Context) error {
	set, err := r.Settings(ctx)
	if err != nil {
		return err
	}
	data := r.cat.Get()
	page := data.DefaultPage()
	if page == nil {
		return nil
	}
	ids, names := publicMonitors(data, page.ID)
	open := true
	incs, err := r.st.ListIncidents(ctx, store.IncidentFilter{Kind: store.KindIncident, Open: &open, Limit: 200})
	if err != nil {
		return err
	}
	var auto *store.Incident
	covered := map[int64]bool{} // already in an incident someone wrote
	for i := range incs {
		if incs[i].Auto {
			if auto == nil {
				auto = &incs[i]
			}
			continue
		}
		for _, m := range incs[i].Monitors {
			covered[m.MonitorID] = true
		}
	}
	if !set.Enabled && auto == nil {
		return nil
	}

	watch := append([]int64(nil), ids...)
	if auto != nil {
		for _, m := range auto.Monitors {
			if _, ok := names[m.MonitorID]; !ok {
				watch = append(watch, m.MonitorID)
			}
		}
	}
	live, _, err := snapshot.ReadStatuses(ctx, r.rdb, watch, false)
	if err != nil {
		return err
	}
	now := r.now()
	t := textsFor(page.Language)
	changed := false

	if set.Enabled {
		after := time.Duration(max(1, set.AfterMin)) * time.Minute
		inAuto := map[int64]bool{}
		if auto != nil {
			for _, m := range auto.Monitors {
				inAuto[m.MonitorID] = true
			}
		}
		var newly []int64
		for _, id := range ids {
			m := data.ByID[id]
			if m == nil || !m.Enabled || !m.AutoIncident || data.InMaintenance[id] || covered[id] || inAuto[id] {
				continue
			}
			ls := live[id]
			if ls.Status != "down" || ls.Since == nil || now.Sub(*ls.Since) < after {
				continue
			}
			newly = append(newly, id)
		}
		if len(newly) > 0 {
			var list []string
			for _, id := range newly {
				list = append(list, names[id])
			}
			if auto == nil {
				inc := store.Incident{Kind: store.KindIncident, Title: fill(t.Title, list), Status: "investigating", Impact: "major",
					PageIDs: []int64{page.ID}, NotifySubscribers: set.NotifySubscribers, Auto: true}
				for _, id := range newly {
					inc.Monitors = append(inc.Monitors, store.IncidentMonitor{MonitorID: id, Impact: "down"})
				}
				created, err := r.st.SaveIncident(ctx, inc, fill(t.Investigating, list), 0)
				if err != nil {
					return err
				}
				r.log.Info("posted an automatic incident", "incident", created.ID, "monitors", newly)
				r.announce(ctx, created, "created")
				auto, changed = &created, true
			} else {
				ext := *auto
				for _, id := range newly {
					ext.Monitors = append(ext.Monitors, store.IncidentMonitor{MonitorID: id, Impact: "down"})
				}
				if _, err := r.st.SaveIncident(ctx, ext, "", 0); err != nil {
					return err
				}
				// A person's status change stays: the update keeps the current status.
				upd, err := r.st.AddIncidentUpdate(ctx, auto.ID, auto.Status, fill(t.AlsoAffected, list), 0)
				if err != nil {
					return err
				}
				r.log.Info("extended the automatic incident", "incident", auto.ID, "monitors", newly)
				r.announce(ctx, upd, "updated")
				auto, changed = &upd, true
			}
		}
	}

	// Resolve once everything on it has been back for a while, unless a
	// person has taken over (changed the status or posted an update).
	if auto != nil && !changed && auto.Status == "investigating" && !touchedByPerson(*auto) {
		back := true
		for _, m := range auto.Monitors {
			mon := data.ByID[m.MonitorID]
			if mon == nil || !mon.Enabled {
				continue // deleted or paused: not holding the incident open
			}
			ls := live[m.MonitorID]
			if ls.Status == "down" || ls.Status == "pending" || ls.Status == "" || ls.Since == nil || now.Sub(*ls.Since) < recoverFor {
				back = false
				break
			}
		}
		if back {
			upd, err := r.st.AddIncidentUpdate(ctx, auto.ID, "resolved", t.Resolved, 0)
			if err != nil {
				return err
			}
			r.log.Info("resolved the automatic incident", "incident", auto.ID)
			r.announce(ctx, upd, "resolved")
			changed = true
		}
	}

	if changed {
		_ = r.cat.Reload(ctx)
		catalog.NotifyChanged(ctx, r.rdb)
	}
	return nil
}

func touchedByPerson(inc store.Incident) bool {
	for _, u := range inc.Updates {
		if u.CreatedBy != nil {
			return true
		}
	}
	return false
}

// announce emails subscribers when the incident asks for it. The team's
// alert channels already got the monitor's own down alert, so they're skipped.
func (r *Runner) announce(ctx context.Context, inc store.Incident, event string) {
	if r.OnIncident == nil || !inc.NotifySubscribers || len(inc.Updates) == 0 {
		return
	}
	u := inc.Updates[0] // newest first
	r.OnIncident(context.WithoutCancel(ctx), inc, event, &u)
}
