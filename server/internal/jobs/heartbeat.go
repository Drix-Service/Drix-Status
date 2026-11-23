// Package jobs holds leader-only background jobs.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/catalog"
	"drix/server/internal/pipeline"
	"drix/server/internal/rkeys"
	"drix/shared/protocol"
)

// Heartbeats records a failure for every push monitor that hasn't been
// pinged within its interval plus grace period. The pipeline dedupes these
// per interval, so a silent job produces one failed check per interval.
func Heartbeats(ctx context.Context, rdb *redis.Client, cat *catalog.Catalog, pipe *pipeline.Pipeline, isLeader func() bool, log *slog.Logger) {
	t := time.NewTicker(5 * time.Second)
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
		data := cat.Get()
		now := time.Now().UTC()
		var missed []protocol.Result
		for i := range data.Monitors {
			m := &data.Monitors[i]
			if m.Type != protocol.TypePush || !m.Enabled {
				continue
			}
			var cfg protocol.PushConfig
			_ = jsonUnmarshal(m.Config, &cfg)
			deadline := time.Duration(m.IntervalS+cfg.GraceS) * time.Second

			last := m.UpdatedAt // never pinged: count from creation/last edit
			if v, err := rdb.Get(ctx, rkeys.PushLast(m.ID)).Result(); err == nil {
				if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
					if t := time.UnixMilli(ms); t.After(last) {
						last = t
					}
				}
			}
			if late := now.Sub(last); late > deadline {
				missed = append(missed, protocol.Result{
					MonitorID: m.ID, At: now, OK: false,
					Message: fmt.Sprintf("no heartbeat for %s (expected every %s)", round(late), round(time.Duration(m.IntervalS)*time.Second)),
				})
			}
		}
		if len(missed) > 0 {
			pipe.IngestServer(ctx, pipeline.FromHeartbeat, missed)
		}
	}
}

func round(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return d.Round(time.Minute).String()
	case d >= time.Minute:
		return d.Round(time.Second).String()
	default:
		return d.Round(time.Second).String()
	}
}
