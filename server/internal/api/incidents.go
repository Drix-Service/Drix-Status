package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"drix/server/internal/snapshot"
	"drix/server/internal/store"
)

// IncidentNotifier is told about incident activity (subscriber emails,
// alert channels). Implemented by the notify package; may be nil.
type IncidentNotifier interface {
	IncidentEvent(ctx context.Context, inc store.Incident, event string, update *store.IncidentUpdate)
}

func (a *API) notifyIncident(ctx context.Context, inc store.Incident, event string) {
	if a.Notifier == nil {
		return
	}
	var u *store.IncidentUpdate
	if len(inc.Updates) > 0 {
		u = &inc.Updates[0]
	}
	a.Notifier.IncidentEvent(context.WithoutCancel(ctx), inc, event, u)
}

type incidentInput struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Impact   string `json:"impact"`
	Message  string `json:"message"`
	Monitors []struct {
		ID     int64  `json:"id"`
		Impact string `json:"impact"`
	} `json:"monitors"`
	ScheduledStart    *time.Time `json:"scheduled_start"`
	ScheduledEnd      *time.Time `json:"scheduled_end"`
	AutoTransition    *bool      `json:"auto_transition"`
	NotifySubscribers *bool      `json:"notify_subscribers"`
}

// toIncident validates input. For updates, kind and status come from `cur`.
func (a *API) toIncident(in incidentInput, cur *store.Incident) (store.Incident, error) {
	data := a.Catalog.Get()
	out := store.Incident{
		Kind: in.Kind, Title: strings.TrimSpace(in.Title), Status: in.Status, Impact: in.Impact,
		ScheduledStart: in.ScheduledStart, ScheduledEnd: in.ScheduledEnd,
		AutoTransition: true, NotifySubscribers: true,
	}
	if cur != nil {
		out.ID, out.Kind, out.Status = cur.ID, cur.Kind, cur.Status
	}
	if in.AutoTransition != nil {
		out.AutoTransition = *in.AutoTransition
	}
	if in.NotifySubscribers != nil {
		out.NotifySubscribers = *in.NotifySubscribers
	}
	if out.Kind != store.KindIncident && out.Kind != store.KindMaintenance {
		return out, fieldErr{"kind", "Choose incident or maintenance"}
	}
	if out.Title == "" || len(out.Title) > 200 {
		return out, fieldErr{"title", "Give it a title (max 200 characters)"}
	}
	if out.Impact == "" {
		out.Impact = map[string]string{store.KindIncident: "minor", store.KindMaintenance: "none"}[out.Kind]
	}
	if !slices.Contains(store.Impacts, out.Impact) {
		return out, fieldErr{"impact", "Choose an impact"}
	}
	if len(in.Message) > 5000 {
		return out, fieldErr{"message", "Keep the message under 5000 characters"}
	}

	if out.Kind == store.KindMaintenance {
		if out.ScheduledStart == nil || out.ScheduledEnd == nil {
			return out, fieldErr{"scheduled_start", "Set a start and end time"}
		}
		if !out.ScheduledEnd.After(*out.ScheduledStart) {
			return out, fieldErr{"scheduled_end", "The end must be after the start"}
		}
		if out.ScheduledEnd.Sub(*out.ScheduledStart) > 31*24*time.Hour {
			return out, fieldErr{"scheduled_end", "Maintenance can last at most 31 days"}
		}
		if cur == nil {
			if out.Status == "" {
				out.Status = "scheduled"
				if !out.ScheduledStart.After(time.Now()) {
					out.Status = "in_progress"
				}
			}
			if !slices.Contains(store.MaintenanceStatuses, out.Status) {
				return out, fieldErr{"status", "Invalid maintenance status"}
			}
		}
	} else {
		out.ScheduledStart, out.ScheduledEnd = nil, nil
		if cur == nil {
			if out.Status == "" {
				out.Status = "investigating"
			}
			if !slices.Contains(store.IncidentStatuses, out.Status) {
				return out, fieldErr{"status", "Invalid incident status"}
			}
			if strings.TrimSpace(in.Message) == "" {
				return out, fieldErr{"message", "Tell people what's happening"}
			}
		}
	}

	for _, m := range in.Monitors {
		if _, ok := data.ByID[m.ID]; !ok {
			return out, fieldErr{"monitors", fmt.Sprintf("Monitor %d doesn't exist", m.ID)}
		}
		imp := m.Impact
		if out.Kind == store.KindMaintenance {
			imp = "maintenance"
		} else if imp != "degraded" && imp != "down" {
			imp = "degraded"
		}
		out.Monitors = append(out.Monitors, store.IncidentMonitor{MonitorID: m.ID, Impact: imp})
	}
	// There is one status page; every incident is shown on it.
	if def := data.DefaultPage(); def != nil {
		out.PageIDs = []int64{def.ID}
	}
	return out, nil
}

func (a *API) listIncidents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.IncidentFilter{Kind: q.Get("kind"), Limit: queryInt(r, "limit", 50, 1, 200)}
	f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	switch q.Get("state") {
	case "open":
		t := true
		f.Open = &t
	case "closed":
		t := false
		f.Open = &t
	}
	list, err := a.Store.ListIncidents(r.Context(), f)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) getIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	in, err := a.Store.GetIncident(r.Context(), id)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (a *API) createIncident(w http.ResponseWriter, r *http.Request) {
	var in incidentInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	inc, err := a.toIncident(in, nil)
	if err != nil {
		writeValidation(w, err)
		return
	}
	msg := strings.TrimSpace(in.Message)
	if msg == "" && inc.Kind == store.KindMaintenance {
		msg = "Maintenance is scheduled."
	}
	inc, err = a.Store.SaveIncident(r.Context(), inc, msg, userFrom(r.Context()).ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.changed(r.Context())
	a.audit(r, inc.Kind+".create", inc.Kind, inc.ID, inc.Title)
	a.notifyIncident(r.Context(), inc, "created")
	writeJSON(w, http.StatusCreated, inc)
}

func (a *API) updateIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in incidentInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	cur, err := a.Store.GetIncident(r.Context(), id)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	inc, err := a.toIncident(in, &cur)
	if err != nil {
		writeValidation(w, err)
		return
	}
	inc, err = a.Store.SaveIncident(r.Context(), inc, "", userFrom(r.Context()).ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.changed(r.Context())
	a.audit(r, inc.Kind+".edit", inc.Kind, inc.ID, "Edited "+inc.Title)
	writeJSON(w, http.StatusOK, inc)
}

func (a *API) postIncidentUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	cur, err := a.Store.GetIncident(r.Context(), id)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	allowed := store.IncidentStatuses
	if cur.Kind == store.KindMaintenance {
		allowed = store.MaintenanceStatuses
	}
	if !slices.Contains(allowed, in.Status) {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Choose a status", Field: map[string]string{"status": "Choose a status"}})
		return
	}
	msg := strings.TrimSpace(in.Message)
	if msg == "" || len(msg) > 5000 {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Write an update", Field: map[string]string{"message": "Required (max 5000 characters)"}})
		return
	}
	inc, err := a.Store.AddIncidentUpdate(r.Context(), id, in.Status, msg, userFrom(r.Context()).ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.changed(r.Context())
	a.audit(r, inc.Kind+".update", inc.Kind, inc.ID, fmt.Sprintf("%s → %s", inc.Title, in.Status))
	event := "updated"
	if store.IsClosedStatus(in.Status) {
		event = "resolved"
	}
	a.notifyIncident(r.Context(), inc, event)
	writeJSON(w, http.StatusOK, inc)
}

func (a *API) deleteIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := a.Store.DeleteIncident(r.Context(), id); err != nil {
		a.serverErr(w, err)
		return
	}
	a.changed(r.Context())
	a.audit(r, "incident.delete", "incident", id, fmt.Sprintf("Deleted incident #%d", id))
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Public ----------

func (a *API) publicIncident(w http.ResponseWriter, r *http.Request) {
	page, ok := a.pageFromRequest(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("incident"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	inc, err := a.Store.GetIncident(r.Context(), id)
	if err != nil || !slices.Contains(inc.PageIDs, page.ID) {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=15")
	writeJSON(w, http.StatusOK, snapshot.PublicIncident(inc, a.Catalog.Get(), page.ID, false))
}

func (a *API) publicHistory(w http.ResponseWriter, r *http.Request) {
	page, ok := a.pageFromRequest(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	closed := false
	f := store.IncidentFilter{PageID: page.ID, Open: &closed, Limit: queryInt(r, "limit", 20, 1, 50)}
	f.Before, _ = strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	list, err := a.Store.ListIncidents(r.Context(), f)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	data := a.Catalog.Get()
	out := make([]snapshot.Incident, 0, len(list))
	for _, in := range list {
		if in.Status == "cancelled" {
			continue
		}
		out = append(out, snapshot.PublicIncident(in, data, page.ID, false))
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, map[string]any{"page": page.Slug, "incidents": out, "has_more": len(list) == f.Limit})
}
