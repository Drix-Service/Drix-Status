package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"drix/server/internal/notify"
	"drix/server/internal/rkeys"
	"drix/server/internal/snapshot"
	"drix/server/internal/store"
)

func displayName(u store.User) string {
	if u.Name != "" {
		return u.Name
	}
	return strings.SplitN(u.Email, "@", 2)[0]
}

// ackMonitor: "I'm on it" from the admin or the phone app.
func (a *API) ackMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	u := userFrom(r.Context())
	by, via := displayName(u), ""
	if k := apiKeyFrom(r.Context()); k != "" {
		via = "API key " + k
	}
	ack, fresh, err := a.Notify.Acknowledge(r.Context(), id, 0, by, via)
	if errors.Is(err, notify.ErrNotDown) {
		writeErr(w, http.StatusConflict, "Only a monitor that's down can be acknowledged")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if fresh {
		a.audit(r, "monitor.ack", "monitor", id, "Acknowledged the outage")
	}
	writeJSON(w, http.StatusOK, ack)
}

// ---------- the page an alert's "Acknowledge" button opens ----------

type ackPageOut struct {
	Valid   bool        `json:"valid"`
	Monitor string      `json:"monitor,omitempty"`
	Status  string      `json:"status,omitempty"`
	Since   *time.Time  `json:"since,omitempty"`
	Over    bool        `json:"over"` // recovered, or a newer outage
	Ack     *notify.Ack `json:"ack,omitempty"`
	Who     string      `json:"who,omitempty"`
}

func (a *API) ackPageInfo(r *http.Request) (ackPageOut, int64, int64, int64) {
	id, since, ch, ok := a.Notify.ParseAckToken(r.PathValue("token"))
	if !ok {
		return ackPageOut{}, 0, 0, 0
	}
	m, ok := a.Catalog.Get().ByID[id]
	if !ok {
		return ackPageOut{}, 0, 0, 0
	}
	out := ackPageOut{Valid: true, Monitor: m.Name}
	live, _, err := snapshot.ReadStatuses(r.Context(), a.Redis, []int64{id}, false)
	if err == nil {
		ls := live[id]
		out.Status = ls.Status
		if ls.Status != "down" || ls.Since == nil || ls.Since.UnixMilli() != since {
			out.Over = true
		} else {
			out.Since = ls.Since
			if ack, ok := a.Notify.Acks(r.Context(), live)[id]; ok {
				out.Ack, out.Who = &ack, ack.Who()
			}
		}
	}
	return out, id, since, ch
}

func (a *API) getAckPage(w http.ResponseWriter, r *http.Request) {
	if !a.throttle(r.Context(), "drix:ackpage:"+remoteIP(r), 120, 10*time.Minute) {
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	out, _, _, _ := a.ackPageInfo(r)
	if !out.Valid {
		writeErr(w, http.StatusNotFound, "This link isn't valid")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) postAckPage(w http.ResponseWriter, r *http.Request) {
	if !a.throttle(r.Context(), "drix:ackpage:"+remoteIP(r), 120, 10*time.Minute) {
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	out, id, since, chID := a.ackPageInfo(r)
	if !out.Valid {
		writeErr(w, http.StatusNotFound, "This link isn't valid")
		return
	}
	// Signed in to the admin in this browser: record the name.
	by, via := "", "the alert link"
	var who store.User
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		if uid, err := a.Redis.Get(r.Context(), rkeys.Session(hashToken(c.Value))).Int64(); err == nil {
			if u, err := a.Store.UserByID(r.Context(), uid); err == nil {
				who, by, via = u, displayName(u), ""
			}
		}
	}
	if by == "" {
		if chID == 0 {
			via = "a phone notification"
		} else if ch, err := a.Store.GetChannel(r.Context(), chID); err == nil {
			via = ch.Name
		}
	}
	ack, fresh, err := a.Notify.Acknowledge(r.Context(), id, since, by, via)
	if errors.Is(err, notify.ErrOutageOver) || errors.Is(err, notify.ErrNotDown) {
		writeJSON(w, http.StatusConflict, apiError{Error: "This outage is already over.", Code: "over"})
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if fresh {
		if who.ID != 0 {
			a.auditAs(r, who, "monitor.ack", "monitor", id, "Acknowledged the outage from an alert")
		} else {
			a.auditAs(r, store.User{Email: "(alert link: " + via + ")"}, "monitor.ack", "monitor", id, "Acknowledged the outage from an alert")
		}
	}
	out.Ack, out.Who = &ack, ack.Who()
	writeJSON(w, http.StatusOK, out)
}
