package api

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"drix/server/internal/notify"
	"drix/server/internal/store"
)

// Push notifications for the Drix app: each signed-in user manages the
// devices they turned notifications on for.

func (a *API) pushInfo(w http.ResponseWriter, r *http.Request) {
	out := struct {
		PublicKey string          `json:"public_key"`
		Events    []string        `json:"events"`
		Devices   []store.PushSub `json:"devices"`
		Error     string          `json:"error,omitempty"`
	}{Events: notify.PushEvents, Devices: []store.PushSub{}}
	keys, err := a.Notify.VAPID(r.Context())
	if err != nil {
		out.Error = err.Error()
	} else {
		out.PublicKey = keys.PublicKey()
	}
	subs, err := a.Store.ListPushSubs(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	out.Devices = subs
	writeJSON(w, http.StatusOK, out)
}

func cleanPushEvents(in []string) []string {
	out := []string{}
	for _, e := range in {
		if slices.Contains(notify.PushEvents, e) && !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out
}

// pushSubscribe saves this device's subscription (same device = update).
func (a *API) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Endpoint string   `json:"endpoint"`
		P256dh   string   `json:"p256dh"`
		Auth     string   `json:"auth"`
		Events   []string `json:"events"`
		Device   string   `json:"device"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !strings.HasPrefix(in.Endpoint, "https://") || len(in.Endpoint) > 1000 || in.P256dh == "" || in.Auth == "" ||
		len(in.P256dh) > 200 || len(in.Auth) > 100 {
		writeErr(w, http.StatusUnprocessableEntity, "invalid push subscription")
		return
	}
	events := cleanPushEvents(in.Events)
	if in.Events == nil {
		events = []string{"down", "recovered", "incident", "cert_expiring"}
	}
	sub, err := a.Store.UpsertPushSub(r.Context(), store.PushSub{
		UserID: userFrom(r.Context()).ID, Endpoint: in.Endpoint, P256dh: in.P256dh, Auth: in.Auth,
		Events: events, Device: truncateStr(strings.TrimSpace(in.Device), 80),
	})
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (a *API) pushUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Events []string `json:"events"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	sub, err := a.Store.SetPushEvents(r.Context(), id, userFrom(r.Context()).ID, cleanPushEvents(in.Events))
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (a *API) pushDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := a.Store.DeletePushSub(r.Context(), id, userFrom(r.Context()).ID); err != nil {
		a.serverErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pushTest sends a notification to one of the user's devices right away.
func (a *API) pushTest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	sub, err := a.Store.GetPushSub(r.Context(), id)
	if err != nil || sub.UserID != userFrom(r.Context()).ID {
		writeErr(w, http.StatusNotFound, "device not found")
		return
	}
	m := notify.Message{Event: "test", Title: "Notifications are working", Body: "You'll get Drix alerts on this device.",
		Status: "up", At: time.Now().UTC(), URL: strings.TrimRight(a.Cfg.AdminURL, "/") + "/settings"}
	if err := a.Notify.SendPushTo(r.Context(), sub, m); err != nil {
		writeErr(w, http.StatusBadGateway, "push failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
