package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"drix/shared/protocol"
)

// Monitor is one check definition. Whether it is public is decided by the
// status pages it appears on (page_monitors), not by the monitor itself.
type Monitor struct {
	ID          int64                `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Type        protocol.MonitorType `json:"type"`
	Target      string               `json:"target"`
	Config      json.RawMessage      `json:"config"`
	IntervalS   int                  `json:"interval_s"`
	TimeoutMS   int                  `json:"timeout_ms"`
	Retries     int                  `json:"retries"`
	DegradedMS  int                  `json:"degraded_ms"`
	Enabled     bool                 `json:"enabled"`
	// MultiLocation checks from one worker in every location (region);
	// ShowLocations lists them on the status page.
	MultiLocation bool `json:"multi_location"`
	ShowLocations bool `json:"show_locations"`
	// AutoIncident: may post automatic incidents on the status page.
	AutoIncident bool      `json:"auto_incident"`
	Position     int       `json:"position"`
	Rev          int64     `json:"rev"`
	PushToken    *string   `json:"push_token,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Spec converts to the agent wire format.
func (m Monitor) Spec() protocol.MonitorSpec {
	return protocol.MonitorSpec{
		ID: m.ID, Type: m.Type, Target: m.Target, IntervalS: m.IntervalS,
		TimeoutMS: m.TimeoutMS, DegradedMS: m.DegradedMS, Config: m.Config, Rev: m.Rev,
	}
}

const monitorCols = `id, name, description, type, target, config, interval_s, timeout_ms,
	retries, degraded_ms, enabled, position, rev, push_token, created_at, updated_at, multi_location, show_locations, auto_incident`

func scanMonitor(row pgx.Row) (Monitor, error) {
	var m Monitor
	err := row.Scan(&m.ID, &m.Name, &m.Description, &m.Type, &m.Target, &m.Config,
		&m.IntervalS, &m.TimeoutMS, &m.Retries, &m.DegradedMS, &m.Enabled,
		&m.Position, &m.Rev, &m.PushToken, &m.CreatedAt, &m.UpdatedAt, &m.MultiLocation, &m.ShowLocations, &m.AutoIncident)
	return m, err
}

func (s *Store) ListMonitors(ctx context.Context) ([]Monitor, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+monitorCols+` FROM monitors ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Monitor{}
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMonitor(ctx context.Context, id int64) (Monitor, error) {
	m, err := scanMonitor(s.DB.QueryRow(ctx, `SELECT `+monitorCols+` FROM monitors WHERE id=$1`, id))
	return m, notFound(err)
}

func (s *Store) CreateMonitor(ctx context.Context, m Monitor) (Monitor, error) {
	if len(m.Config) == 0 {
		m.Config = json.RawMessage(`{}`)
	}
	return scanMonitor(s.DB.QueryRow(ctx, `
		INSERT INTO monitors (name, description, type, target, config, interval_s, timeout_ms,
			retries, degraded_ms, enabled, push_token, multi_location, show_locations, auto_incident, position)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14, COALESCE((SELECT MAX(position)+1 FROM monitors), 0))
		RETURNING `+monitorCols,
		m.Name, m.Description, m.Type, m.Target, m.Config, m.IntervalS, m.TimeoutMS,
		m.Retries, m.DegradedMS, m.Enabled, m.PushToken, m.MultiLocation, m.ShowLocations, m.AutoIncident))
}

func (s *Store) UpdateMonitor(ctx context.Context, m Monitor) (Monitor, error) {
	if len(m.Config) == 0 {
		m.Config = json.RawMessage(`{}`)
	}
	out, err := scanMonitor(s.DB.QueryRow(ctx, `
		UPDATE monitors SET name=$2, description=$3, type=$4, target=$5, config=$6,
			interval_s=$7, timeout_ms=$8, retries=$9, degraded_ms=$10, enabled=$11,
			push_token = CASE WHEN $4 = 'push' THEN COALESCE(push_token, $12) ELSE NULL END,
			multi_location=$13, show_locations=$14, auto_incident=$15,
			rev=rev+1, updated_at=now()
		WHERE id=$1 RETURNING `+monitorCols,
		m.ID, m.Name, m.Description, m.Type, m.Target, m.Config, m.IntervalS, m.TimeoutMS,
		m.Retries, m.DegradedMS, m.Enabled, m.PushToken, m.MultiLocation, m.ShowLocations, m.AutoIncident))
	return out, notFound(err)
}

func (s *Store) SetMonitorsEnabled(ctx context.Context, ids []int64, enabled bool) error {
	_, err := s.DB.Exec(ctx, `UPDATE monitors SET enabled=$2, rev=rev+1, updated_at=now() WHERE id = ANY($1)`, ids, enabled)
	return err
}

func (s *Store) DeleteMonitors(ctx context.Context, ids []int64) (int64, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM monitors WHERE id = ANY($1)`, ids)
	return tag.RowsAffected(), err
}

// RotatePushToken gives a heartbeat monitor a new URL (the old one stops working).
func (s *Store) RotatePushToken(ctx context.Context, id int64, token string) (Monitor, error) {
	m, err := scanMonitor(s.DB.QueryRow(ctx, `UPDATE monitors SET push_token=$2, updated_at=now()
		WHERE id=$1 AND type='push' RETURNING `+monitorCols, id, token))
	return m, notFound(err)
}

// ImportMonitors creates monitors in one transaction. groups[i] names the
// group monitor i goes in ("" = none); groups that don't exist on the page
// yet are created at the end, in the order they first appear.
func (s *Store) ImportMonitors(ctx context.Context, pageID int64, monitors []Monitor, groups []string) ([]Monitor, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	sectionID := map[string]int64{}
	rows, err := tx.Query(ctx, `SELECT id, name FROM page_sections WHERE page_id=$1 ORDER BY position, id`, pageID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		if _, ok := sectionID[strings.ToLower(name)]; !ok {
			sectionID[strings.ToLower(name)] = id
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Monitor, 0, len(monitors))
	for i, m := range monitors {
		if len(m.Config) == 0 {
			m.Config = json.RawMessage(`{}`)
		}
		created, err := scanMonitor(tx.QueryRow(ctx, `
			INSERT INTO monitors (name, description, type, target, config, interval_s, timeout_ms,
				retries, degraded_ms, enabled, push_token, multi_location, show_locations, auto_incident, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14, COALESCE((SELECT MAX(position)+1 FROM monitors), 0))
			RETURNING `+monitorCols,
			m.Name, m.Description, m.Type, m.Target, m.Config, m.IntervalS, m.TimeoutMS,
			m.Retries, m.DegradedMS, m.Enabled, m.PushToken, m.MultiLocation, m.ShowLocations, m.AutoIncident))
		if err != nil {
			return nil, err
		}
		out = append(out, created)

		g := strings.TrimSpace(groups[i])
		if g == "" {
			continue
		}
		id, ok := sectionID[strings.ToLower(g)]
		if !ok {
			if err := tx.QueryRow(ctx, `INSERT INTO page_sections (page_id, name, position)
				VALUES ($1, $2, COALESCE((SELECT MAX(position)+1 FROM page_sections WHERE page_id=$1), 0))
				RETURNING id`, pageID, g).Scan(&id); err != nil {
				return nil, err
			}
			sectionID[strings.ToLower(g)] = id
		}
		if _, err := tx.Exec(ctx, `INSERT INTO page_monitors (section_id, monitor_id, position)
			VALUES ($1, $2, COALESCE((SELECT MAX(position)+1 FROM page_monitors WHERE section_id=$1), 0))
			ON CONFLICT DO NOTHING`, id, created.ID); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}
