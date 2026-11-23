package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Alert events a channel can subscribe to.
var AlertEvents = []string{"down", "recovered", "degraded", "incident", "cert_expiring"}

// Channel is an alert destination. ConfigEnc is encrypted JSON.
type Channel struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	ConfigEnc      string   `json:"-"`
	Events         []string `json:"events"`
	ApplyToAll     bool     `json:"apply_to_all"`
	RemindEveryMin int      `json:"remind_every_min"`
	// EscalateAfterMin > 0: down alerts only go out if nobody acknowledged the outage by then.
	EscalateAfterMin int       `json:"escalate_after_min"`
	Enabled          bool      `json:"enabled"`
	CreatedAt        time.Time `json:"created_at"`
	MonitorIDs       []int64   `json:"monitor_ids"`
}

const channelSelect = `SELECT c.id, c.name, c.type, c.config_enc, c.events, c.apply_to_all, c.remind_every_min, c.escalate_after_min, c.enabled, c.created_at,
	COALESCE((SELECT array_agg(mc.monitor_id ORDER BY mc.monitor_id) FROM monitor_channels mc WHERE mc.channel_id = c.id), '{}')
	FROM notification_channels c`

func scanChannel(row pgx.Row) (Channel, error) {
	var c Channel
	err := row.Scan(&c.ID, &c.Name, &c.Type, &c.ConfigEnc, &c.Events, &c.ApplyToAll, &c.RemindEveryMin, &c.EscalateAfterMin, &c.Enabled, &c.CreatedAt, &c.MonitorIDs)
	return c, err
}

func (s *Store) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := s.DB.Query(ctx, channelSelect+` ORDER BY c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetChannel(ctx context.Context, id int64) (Channel, error) {
	c, err := scanChannel(s.DB.QueryRow(ctx, channelSelect+` WHERE c.id=$1`, id))
	return c, notFound(err)
}

// SaveChannel creates (ID == 0) or updates a channel and its monitor list.
func (s *Store) SaveChannel(ctx context.Context, c Channel) (Channel, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Channel{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if c.ID == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO notification_channels (name, type, config_enc, events, apply_to_all, remind_every_min, escalate_after_min, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, c.Name, c.Type, c.ConfigEnc, c.Events, c.ApplyToAll, c.RemindEveryMin, c.EscalateAfterMin, c.Enabled).Scan(&c.ID)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = tx.Exec(ctx, `UPDATE notification_channels SET name=$2, config_enc=$3, events=$4, apply_to_all=$5,
			remind_every_min=$6, enabled=$7, escalate_after_min=$8 WHERE id=$1`, c.ID, c.Name, c.ConfigEnc, c.Events, c.ApplyToAll, c.RemindEveryMin, c.Enabled, c.EscalateAfterMin)
		if err == nil && tag.RowsAffected() == 0 {
			return Channel{}, ErrNotFound
		}
	}
	if err != nil {
		return Channel{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM monitor_channels WHERE channel_id=$1`, c.ID); err != nil {
		return Channel{}, err
	}
	if !c.ApplyToAll && len(c.MonitorIDs) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO monitor_channels (monitor_id, channel_id)
			SELECT m, $2 FROM unnest($1::bigint[]) m JOIN monitors ON monitors.id = m ON CONFLICT DO NOTHING`, c.MonitorIDs, c.ID); err != nil {
			return Channel{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Channel{}, err
	}
	return s.GetChannel(ctx, c.ID)
}

func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM notification_channels WHERE id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetMonitorChannels sets which specific channels alert for a monitor.
func (s *Store) SetMonitorChannels(ctx context.Context, monitorID int64, channelIDs []int64) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM monitor_channels WHERE monitor_id=$1`, monitorID); err != nil {
		return err
	}
	if len(channelIDs) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO monitor_channels (monitor_id, channel_id)
			SELECT $1, c FROM unnest($2::bigint[]) c JOIN notification_channels n ON n.id = c ON CONFLICT DO NOTHING`, monitorID, channelIDs); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Delivery is one attempt to notify a channel.
type Delivery struct {
	ID          int64     `json:"id"`
	ChannelID   *int64    `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	MonitorID   *int64    `json:"monitor_id"`
	Event       string    `json:"event"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	Error       string    `json:"error"`
	Attempts    int       `json:"attempts"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) LogDelivery(ctx context.Context, d Delivery) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO alert_deliveries (channel_id, monitor_id, event, title, status, error, attempts)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, d.ChannelID, d.MonitorID, d.Event, d.Title, d.Status, d.Error, d.Attempts)
	return err
}

func (s *Store) ListDeliveries(ctx context.Context, channelID int64, limit int) ([]Delivery, error) {
	rows, err := s.DB.Query(ctx, `SELECT d.id, d.channel_id, COALESCE(c.name, ''), d.monitor_id, d.event, d.title, d.status, d.error, d.attempts, d.created_at
		FROM alert_deliveries d LEFT JOIN notification_channels c ON c.id = d.channel_id
		WHERE ($1 = 0 OR d.channel_id = $1) ORDER BY d.id DESC LIMIT $2`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.ChannelID, &d.ChannelName, &d.MonitorID, &d.Event, &d.Title, &d.Status, &d.Error, &d.Attempts, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
