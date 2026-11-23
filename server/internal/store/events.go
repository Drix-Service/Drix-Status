package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Event is an automatic status change of a monitor.
type Event struct {
	ID          int64     `json:"id"`
	MonitorID   int64     `json:"monitor_id"`
	MonitorName string    `json:"monitor_name"`
	FromStatus  string    `json:"from_status"`
	ToStatus    string    `json:"to_status"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) InsertEvents(ctx context.Context, evs []Event) error {
	if len(evs) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, e := range evs {
		b.Queue(`INSERT INTO monitor_events (monitor_id, from_status, to_status, message, created_at)
			VALUES ($1,$2,$3,$4,$5)`, e.MonitorID, e.FromStatus, e.ToStatus, e.Message, e.CreatedAt)
	}
	return s.DB.SendBatch(ctx, b).Close()
}

// ListEvents returns recent events. monitorID > 0 limits to one monitor;
// a non-nil onlyMonitors limits to that set (e.g. the monitors on a page).
func (s *Store) ListEvents(ctx context.Context, monitorID int64, onlyMonitors []int64, limit int) ([]Event, error) {
	if onlyMonitors != nil && len(onlyMonitors) == 0 {
		return []Event{}, nil
	}
	rows, err := s.DB.Query(ctx, `
		SELECT e.id, e.monitor_id, m.name, e.from_status, e.to_status, e.message, e.created_at
		FROM monitor_events e JOIN monitors m ON m.id = e.monitor_id
		WHERE ($1 = 0 OR e.monitor_id = $1)
		  AND ($2::bigint[] IS NULL OR e.monitor_id = ANY($2))
		ORDER BY e.created_at DESC LIMIT $3`, monitorID, onlyMonitors, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.MonitorID, &e.MonitorName, &e.FromStatus, &e.ToStatus, &e.Message, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
