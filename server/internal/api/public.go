package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/rkeys"
	"drix/server/internal/store"
)

// pageFromRequest returns the single status page.
func (a *API) pageFromRequest(r *http.Request) (*store.Page, bool) {
	p := a.page()
	return p, p != nil
}

// publicStatus serves a page's precomputed snapshot straight from Redis,
// already gzipped when the client accepts it: one Redis round trip.
func (a *API) publicStatus(w http.ResponseWriter, r *http.Request) {
	page, ok := a.pageFromRequest(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	ctx := r.Context()
	gz := strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
	key := rkeys.SnapshotJSON(page.ID)
	if gz {
		key = rkeys.SnapshotGzip(page.ID)
	}
	vals, err := a.Redis.MGet(ctx, rkeys.SnapshotETag(page.ID), key).Result()
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if vals[1] == nil {
		// No snapshot yet (new page / leader not elected): build one now.
		if err := a.Snapshot.Publish(ctx, page.ID); err != nil {
			a.serverErr(w, err)
			return
		}
		if vals, err = a.Redis.MGet(ctx, rkeys.SnapshotETag(page.ID), key).Result(); err != nil || vals[1] == nil {
			a.serverErr(w, errors.Join(err, errors.New("snapshot missing after build")))
			return
		}
	}
	etag, _ := vals[0].(string)
	body, _ := vals[1].(string)

	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=5, stale-while-revalidate=30")
	h.Set("Vary", "Accept-Encoding, Host")
	if etag != "" {
		h.Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if gz {
		h.Set("Content-Encoding", "gzip")
	}
	_, _ = w.Write([]byte(body))
}

// publicStream sends live status changes for one page over SSE.
func (a *API) publicStream(w http.ResponseWriter, r *http.Request) {
	page, ok := a.pageFromRequest(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	a.Hub.ServePublic(w, r, page.ID)
}

type latencyResponse struct {
	Range  string `json:"range"`
	Bucket int64  `json:"bucket_s"`
	Points any    `json:"points"`
}

// latency returns bucketed latency for charts, cached in Redis for 30s.
func (a *API) latency(w http.ResponseWriter, r *http.Request, id int64) {
	ctx := r.Context()
	span, bucket, rng := rangeParams(r.URL.Query().Get("range"))
	ck := rkeys.LatencyCache(id, rng)
	if cached, err := a.Redis.Get(ctx, ck).Bytes(); err == nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(cached)
		return
	} else if err != redis.Nil {
		a.Log.Warn("latency cache", "err", err)
	}
	pts, err := a.Store.LatencySeries(ctx, id, time.Now().Add(-span), bucket)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	b, _ := json.Marshal(latencyResponse{Range: rng, Bucket: int64(bucket.Seconds()), Points: pts})
	a.Redis.Set(ctx, ck, b, 30*time.Second)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(b)
}

// publicLatency serves chart data for monitors that appear on some status page.
func (a *API) publicLatency(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if !a.Catalog.Get().IsPublic(id) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	a.latency(w, r, id)
}
