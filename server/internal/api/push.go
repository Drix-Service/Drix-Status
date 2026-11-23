package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"drix/server/internal/pipeline"
	"drix/server/internal/rkeys"
	"drix/shared/protocol"
)

func newPushToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// push records a heartbeat: GET or POST /api/push/{token}
//
//	?status=up|down   (default up)
//	&msg=…            (optional message, shown in the admin panel)
//	&ping=123         (optional duration in ms, e.g. how long the job took)
func (a *API) push(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	m := a.Catalog.Get().MonitorByPushToken(tok)
	if m == nil {
		writeErr(w, http.StatusNotFound, "unknown heartbeat URL")
		return
	}
	if !a.throttle(r.Context(), "drix:pushrl:"+tok, 120, time.Minute) {
		writeErr(w, http.StatusTooManyRequests, "too many pings; at most 2 per second")
		return
	}
	if !m.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "paused": true})
		return
	}
	q := r.URL.Query()
	ok := !strings.EqualFold(q.Get("status"), "down")
	lat, _ := strconv.ParseFloat(q.Get("ping"), 64)
	msg := truncateStr(q.Get("msg"), 300)
	if msg == "" {
		msg = "heartbeat received"
		if !ok {
			msg = "job reported failure"
		}
	}
	now := time.Now().UTC()
	a.Redis.Set(r.Context(), rkeys.PushLast(m.ID), now.UnixMilli(), 0)
	a.Pipeline.IngestServer(r.Context(), pipeline.FromPush, []protocol.Result{{
		MonitorID: m.ID, At: now, OK: ok, LatencyMS: max(lat, 0), Message: msg,
	}})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) rotatePushToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	m, err := a.Store.RotatePushToken(r.Context(), id, newPushToken())
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.changed(r.Context())
	a.audit(r, "monitor.push_rotate", "monitor", id, "New heartbeat URL for "+m.Name)
	writeJSON(w, http.StatusOK, m)
}
