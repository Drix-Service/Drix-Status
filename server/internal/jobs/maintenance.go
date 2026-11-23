package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/catalog"
	"drix/server/internal/store"
)

// MaintenanceNotifier is told when a window starts or completes.
type MaintenanceNotifier interface {
	IncidentEvent(ctx context.Context, inc store.Incident, event string, update *store.IncidentUpdate)
}

// Maintenance starts and completes scheduled maintenance windows on time.
func Maintenance(ctx context.Context, st *store.Store, rdb *redis.Client, isLeader func() bool, notify MaintenanceNotifier, log *slog.Logger) {
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
		changed, err := st.AdvanceMaintenance(ctx, time.Now().UTC())
		if err != nil {
			log.Error("advance maintenance", "err", err)
			continue
		}
		if len(changed) == 0 {
			continue
		}
		catalog.NotifyChanged(ctx, rdb)
		for _, c := range changed {
			log.Info("maintenance transition", "id", c.ID, "status", c.Status)
			if notify == nil {
				continue
			}
			if inc, err := st.GetIncident(ctx, c.ID); err == nil {
				event := "started"
				if c.Status == "completed" {
					event = "resolved"
				}
				var u *store.IncidentUpdate
				if len(inc.Updates) > 0 {
					u = &inc.Updates[0]
				}
				notify.IncidentEvent(ctx, inc, event, u)
			}
		}
	}
}
