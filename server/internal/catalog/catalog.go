// Package catalog keeps an in-memory copy of monitors, status pages and
// settings, reloaded instantly via Redis pub/sub whenever the admin changes
// something. Every hot path (assignment, result processing, snapshots, the
// public API) reads from here instead of hitting Postgres.
package catalog

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/rkeys"
	"drix/server/internal/store"
)

// Data is an immutable view of the catalog.
type Data struct {
	Monitors []store.Monitor
	ByID     map[int64]*store.Monitor
	Settings store.Settings

	Pages    []store.Page
	PageByID map[int64]*store.Page
	// Sections per page, in display order.
	Sections map[int64][]store.Section
	// PagesOf lists the pages each monitor appears on.
	PagesOf map[int64][]int64
	// PageMonitorIDs lists every monitor on a page (deduplicated).
	PageMonitorIDs map[int64][]int64
	// InMaintenance holds monitors covered by an in-progress maintenance window.
	InMaintenance map[int64]bool

	byPush   map[string]*store.Monitor
	bySlug   map[string]*store.Page
	byDomain map[string]*store.Page
	def      *store.Page
	Loaded   time.Time
}

// IsPublic reports whether a monitor appears on any status page.
func (d *Data) IsPublic(id int64) bool { return len(d.PagesOf[id]) > 0 }

// OnPage reports whether a monitor appears on the given page.
func (d *Data) OnPage(pageID, monitorID int64) bool {
	for _, p := range d.PagesOf[monitorID] {
		if p == pageID {
			return true
		}
	}
	return false
}

// MonitorByPushToken finds a heartbeat monitor by its URL token.
func (d *Data) MonitorByPushToken(tok string) *store.Monitor { return d.byPush[tok] }

// PageBySlug finds a page by its URL slug.
func (d *Data) PageBySlug(slug string) *store.Page { return d.bySlug[strings.ToLower(slug)] }

// DefaultPage is the page served at "/".
func (d *Data) DefaultPage() *store.Page { return d.def }

// PageForHost resolves a page by custom domain, falling back to the default page.
func (d *Data) PageForHost(host string) *store.Page {
	h := strings.ToLower(host)
	if i := strings.LastIndexByte(h, ':'); i > 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	if p, ok := d.byDomain[h]; ok {
		return p
	}
	return d.def
}

// Catalog provides lock-free reads of the latest Data.
type Catalog struct {
	st  *store.Store
	rdb *redis.Client
	log *slog.Logger
	cur atomic.Pointer[Data]

	mu   sync.Mutex
	subs []chan struct{}
}

func New(st *store.Store, rdb *redis.Client, log *slog.Logger) *Catalog {
	return &Catalog{st: st, rdb: rdb, log: log}
}

// Get returns the current snapshot. Never nil after Start.
func (c *Catalog) Get() *Data { return c.cur.Load() }

// Subscribe returns a channel signalled after every reload.
func (c *Catalog) Subscribe() <-chan struct{} {
	ch := make(chan struct{}, 1)
	c.mu.Lock()
	c.subs = append(c.subs, ch)
	c.mu.Unlock()
	return ch
}

// Reload pulls fresh data from Postgres.
func (c *Catalog) Reload(ctx context.Context) error {
	mons, err := c.st.ListMonitors(ctx)
	if err != nil {
		return err
	}
	settings, err := c.st.GetSettings(ctx)
	if err != nil {
		return err
	}
	pages, err := c.st.ListPages(ctx)
	if err != nil {
		return err
	}
	sections, err := c.st.ListSections(ctx)
	if err != nil {
		return err
	}
	maint, err := c.st.ActiveMaintenanceMonitors(ctx)
	if err != nil {
		return err
	}

	d := &Data{
		Monitors: mons, Settings: settings, Pages: pages, Loaded: time.Now(),
		ByID:           make(map[int64]*store.Monitor, len(mons)),
		PageByID:       make(map[int64]*store.Page, len(pages)),
		Sections:       map[int64][]store.Section{},
		PagesOf:        map[int64][]int64{},
		PageMonitorIDs: map[int64][]int64{},
		InMaintenance:  make(map[int64]bool, len(maint)),
		byPush:         map[string]*store.Monitor{},
		bySlug:         map[string]*store.Page{},
		byDomain:       map[string]*store.Page{},
	}
	for _, id := range maint {
		d.InMaintenance[id] = true
	}
	for i := range d.Monitors {
		m := &d.Monitors[i]
		d.ByID[m.ID] = m
		if m.PushToken != nil {
			d.byPush[*m.PushToken] = m
		}
	}
	for i := range d.Pages {
		p := &d.Pages[i]
		d.PageByID[p.ID] = p
		d.bySlug[strings.ToLower(p.Slug)] = p
		if p.CustomDomain != nil && *p.CustomDomain != "" {
			d.byDomain[strings.ToLower(*p.CustomDomain)] = p
		}
		if p.IsDefault {
			d.def = p
		}
	}
	if d.def == nil && len(d.Pages) > 0 {
		d.def = &d.Pages[0]
	}
	seen := map[[2]int64]bool{}
	for _, sec := range sections {
		if _, ok := d.PageByID[sec.PageID]; !ok {
			continue
		}
		d.Sections[sec.PageID] = append(d.Sections[sec.PageID], sec)
		for _, pm := range sec.Monitors {
			if _, ok := d.ByID[pm.MonitorID]; !ok {
				continue
			}
			k := [2]int64{sec.PageID, pm.MonitorID}
			if seen[k] {
				continue
			}
			seen[k] = true
			d.PagesOf[pm.MonitorID] = append(d.PagesOf[pm.MonitorID], sec.PageID)
			d.PageMonitorIDs[sec.PageID] = append(d.PageMonitorIDs[sec.PageID], pm.MonitorID)
		}
	}
	c.cur.Store(d)

	c.mu.Lock()
	for _, ch := range c.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	c.mu.Unlock()
	return nil
}

// Start loads initial data and keeps it fresh until ctx ends.
func (c *Catalog) Start(ctx context.Context) error {
	if err := c.Reload(ctx); err != nil {
		return err
	}
	go func() {
		sub := c.rdb.Subscribe(ctx, rkeys.ChanMonitorsChanged, rkeys.ChanSettingsChanged)
		defer sub.Close()
		msgs := sub.Channel()
		tick := time.NewTicker(60 * time.Second) // safety net
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-msgs:
				drain(msgs, 50*time.Millisecond) // coalesce bursts (e.g. bulk edits)
			case <-tick.C:
			}
			if err := c.Reload(ctx); err != nil && ctx.Err() == nil {
				c.log.Error("catalog reload failed", "err", err)
			}
		}
	}()
	return nil
}

func drain(ch <-chan *redis.Message, wait time.Duration) {
	t := time.NewTimer(wait)
	defer t.Stop()
	for {
		select {
		case <-ch:
		case <-t.C:
			return
		}
	}
}

// NotifyChanged tells every server instance to reload and every page to rebuild.
func NotifyChanged(ctx context.Context, rdb *redis.Client) {
	rdb.Publish(ctx, rkeys.ChanMonitorsChanged, "1")
	rdb.SAdd(ctx, rkeys.SnapshotDirty, rkeys.AllPages)
}
