package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/catalog"
	"drix/server/internal/pipeline"
	"drix/server/internal/rkeys"
)

// Hub fans out live events from Redis to Server-Sent Events clients. Each
// server instance holds one Redis subscription regardless of client count.
type Hub struct {
	rdb *redis.Client
	cat *catalog.Catalog
	log *slog.Logger

	mu      sync.RWMutex
	clients map[*sseClient]struct{}
}

type sseClient struct {
	admin  bool
	pageID int64 // public clients only see this page's monitors
	ch     chan []byte
}

func NewHub(rdb *redis.Client, cat *catalog.Catalog, log *slog.Logger) *Hub {
	return &Hub{rdb: rdb, cat: cat, log: log, clients: map[*sseClient]struct{}{}}
}

// Run consumes Redis events until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	sub := h.rdb.Subscribe(ctx, rkeys.ChanEvents)
	defer sub.Close()
	ch := sub.Channel(redis.WithChannelSize(1024))
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-ch:
			h.dispatch([]byte(m.Payload))
		}
	}
}

func (h *Hub) dispatch(payload []byte) {
	h.mu.RLock()
	n := len(h.clients)
	h.mu.RUnlock()
	if n == 0 {
		return
	}
	var b pipeline.Broadcast
	if json.Unmarshal(payload, &b) != nil {
		return
	}

	// Public clients only see their page's monitors and no internal error
	// messages. Payloads are built once per page per event.
	data := h.cat.Get()
	perPage := map[int64][]byte{}
	publicFor := func(pageID int64) []byte {
		if msg, ok := perPage[pageID]; ok {
			return msg
		}
		var msg []byte
		if b.Type == "status" {
			items := make([]pipeline.LiveUpdate, 0, len(b.Items))
			for _, it := range b.Items {
				if data.OnPage(pageID, it.ID) {
					it.Message = ""
					items = append(items, it)
				}
			}
			if len(items) > 0 {
				msg, _ = json.Marshal(pipeline.Broadcast{Type: "status", Items: items})
			}
		}
		perPage[pageID] = msg
		return msg
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		msg := payload
		if !c.admin {
			if msg = publicFor(c.pageID); msg == nil {
				continue
			}
		}
		select {
		case c.ch <- msg:
		default: // slow client: drop; it will resync on the next snapshot fetch
		}
	}
}

// ServePublic streams live updates for one status page.
func (h *Hub) ServePublic(w http.ResponseWriter, r *http.Request, pageID int64) {
	h.serve(w, r, false, pageID)
}
func (h *Hub) ServeAdmin(w http.ResponseWriter, r *http.Request) { h.serve(w, r, true, 0) }

func (h *Hub) serve(w http.ResponseWriter, r *http.Request, admin bool, pageID int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache, no-transform")
	hdr.Set("Connection", "keep-alive")
	hdr.Set("X-Accel-Buffering", "no") // disable nginx buffering

	c := &sseClient{admin: admin, pageID: pageID, ch: make(chan []byte, 64)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
	}()

	fmt.Fprint(w, "retry: 3000\nevent: ready\ndata: {}\n\n")
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	rc := http.NewResponseController(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case msg := <-c.ch:
			_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// Clients returns the number of connected SSE clients.
func (h *Hub) Clients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
