package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Incident kinds and statuses.
const (
	KindIncident    = "incident"
	KindMaintenance = "maintenance"
)

var (
	IncidentStatuses    = []string{"investigating", "identified", "monitoring", "resolved"}
	MaintenanceStatuses = []string{"scheduled", "in_progress", "completed", "cancelled"}
	Impacts             = []string{"none", "minor", "major", "critical"}
)

// IsClosedStatus reports whether a status ends the incident/maintenance.
func IsClosedStatus(s string) bool { return s == "resolved" || s == "completed" || s == "cancelled" }

type IncidentUpdate struct {
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	Body      string    `json:"body"`
	CreatedBy *int64    `json:"created_by"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

type IncidentMonitor struct {
	MonitorID int64  `json:"monitor_id"`
	Impact    string `json:"impact"`
}

type Incident struct {
	ID                int64             `json:"id"`
	Kind              string            `json:"kind"`
	Title             string            `json:"title"`
	Status            string            `json:"status"`
	Impact            string            `json:"impact"`
	ScheduledStart    *time.Time        `json:"scheduled_start"`
	ScheduledEnd      *time.Time        `json:"scheduled_end"`
	AutoTransition    bool              `json:"auto_transition"`
	NotifySubscribers bool              `json:"notify_subscribers"`
	CreatedBy         *int64            `json:"created_by"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	ResolvedAt        *time.Time        `json:"resolved_at"`
	Updates           []IncidentUpdate  `json:"updates"`
	Monitors          []IncidentMonitor `json:"monitors"`
	PageIDs           []int64           `json:"page_ids"`
	// Auto: posted by Drix because a public monitor was down.
	Auto bool `json:"auto"`
}

const incidentSelect = `
	SELECT i.id, i.kind, i.title, i.status, i.impact, i.scheduled_start, i.scheduled_end,
	       i.auto_transition, i.notify_subscribers, i.created_by, i.created_at, i.updated_at, i.resolved_at, i.auto,
	       COALESCE((SELECT json_agg(json_build_object('id', u.id, 'status', u.status, 'body', u.body,
	                    'created_by', u.created_by, 'author', COALESCE(NULLIF(us.name, ''), us.email, ''),
	                    'created_at', u.created_at) ORDER BY u.created_at DESC, u.id DESC)
	                 FROM incident_updates u LEFT JOIN users us ON us.id = u.created_by
	                 WHERE u.incident_id = i.id), '[]'),
	       COALESCE((SELECT json_agg(json_build_object('monitor_id', m.monitor_id, 'impact', m.impact))
	                 FROM incident_monitors m WHERE m.incident_id = i.id), '[]'),
	       COALESCE((SELECT json_agg(p.page_id) FROM incident_pages p WHERE p.incident_id = i.id), '[]')
	FROM incidents i`

func scanIncident(row pgx.Row) (Incident, error) {
	var in Incident
	var updates, monitors, pages []byte
	err := row.Scan(&in.ID, &in.Kind, &in.Title, &in.Status, &in.Impact, &in.ScheduledStart, &in.ScheduledEnd,
		&in.AutoTransition, &in.NotifySubscribers, &in.CreatedBy, &in.CreatedAt, &in.UpdatedAt, &in.ResolvedAt, &in.Auto,
		&updates, &monitors, &pages)
	if err != nil {
		return in, err
	}
	_ = json.Unmarshal(updates, &in.Updates)
	_ = json.Unmarshal(monitors, &in.Monitors)
	_ = json.Unmarshal(pages, &in.PageIDs)
	return in, nil
}

func collectIncidents(rows pgx.Rows, err error) ([]Incident, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		in, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// IncidentFilter selects incidents for listings.
type IncidentFilter struct {
	Kind   string    // "" = both
	Open   *bool     // nil = all; true = not closed; false = closed
	PageID int64     // 0 = any page
	Since  time.Time // created/scheduled after (zero = no limit)
	Before int64     // id < before (pagination), 0 = none
	Limit  int
}

func (s *Store) ListIncidents(ctx context.Context, f IncidentFilter) ([]Incident, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	var open any
	if f.Open != nil {
		open = *f.Open
	}
	return collectIncidents(s.DB.Query(ctx, incidentSelect+`
		WHERE ($1 = '' OR i.kind = $1)
		  AND ($2::boolean IS NULL OR (i.resolved_at IS NULL) = $2)
		  AND ($3 = 0 OR EXISTS (SELECT 1 FROM incident_pages p WHERE p.incident_id = i.id AND p.page_id = $3))
		  AND ($4::timestamptz IS NULL OR COALESCE(i.scheduled_end, i.resolved_at, now()) >= $4)
		  AND ($5 = 0 OR i.id < $5)
		ORDER BY COALESCE(i.scheduled_start, i.created_at) DESC, i.id DESC
		LIMIT $6`, f.Kind, open, f.PageID, nullTime(f.Since), f.Before, f.Limit))
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func (s *Store) GetIncident(ctx context.Context, id int64) (Incident, error) {
	in, err := scanIncident(s.DB.QueryRow(ctx, incidentSelect+` WHERE i.id=$1`, id))
	return in, notFound(err)
}

// SaveIncident creates (ID == 0) or updates an incident's details, affected
// monitors and pages. firstUpdate is posted only when creating.
func (s *Store) SaveIncident(ctx context.Context, in Incident, firstUpdate string, by int64) (Incident, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	creating := in.ID == 0
	if creating {
		err = tx.QueryRow(ctx, `INSERT INTO incidents (kind, title, status, impact, scheduled_start, scheduled_end,
				auto_transition, notify_subscribers, created_by, resolved_at, auto)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, CASE WHEN $3 IN ('resolved','completed','cancelled') THEN now() END, $10)
			RETURNING id`, in.Kind, in.Title, in.Status, in.Impact, in.ScheduledStart, in.ScheduledEnd,
			in.AutoTransition, in.NotifySubscribers, nullID(by), in.Auto).Scan(&in.ID)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = tx.Exec(ctx, `UPDATE incidents SET title=$2, impact=$3, scheduled_start=$4, scheduled_end=$5,
				auto_transition=$6, notify_subscribers=$7, updated_at=now() WHERE id=$1`,
			in.ID, in.Title, in.Impact, in.ScheduledStart, in.ScheduledEnd, in.AutoTransition, in.NotifySubscribers)
		if err == nil && tag.RowsAffected() == 0 {
			return Incident{}, ErrNotFound
		}
	}
	if err != nil {
		return Incident{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM incident_monitors WHERE incident_id=$1`, in.ID); err != nil {
		return Incident{}, err
	}
	for _, m := range in.Monitors {
		if _, err := tx.Exec(ctx, `INSERT INTO incident_monitors (incident_id, monitor_id, impact) VALUES ($1,$2,$3)
			ON CONFLICT DO NOTHING`, in.ID, m.MonitorID, m.Impact); err != nil {
			return Incident{}, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM incident_pages WHERE incident_id=$1`, in.ID); err != nil {
		return Incident{}, err
	}
	for _, p := range in.PageIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO incident_pages (incident_id, page_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, in.ID, p); err != nil {
			return Incident{}, err
		}
	}
	if creating && firstUpdate != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO incident_updates (incident_id, status, body, created_by) VALUES ($1,$2,$3,$4)`,
			in.ID, in.Status, firstUpdate, nullID(by)); err != nil {
			return Incident{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Incident{}, err
	}
	return s.GetIncident(ctx, in.ID)
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// AddIncidentUpdate posts an update and moves the incident to its status.
func (s *Store) AddIncidentUpdate(ctx context.Context, id int64, status, body string, by int64) (Incident, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `UPDATE incidents SET status=$2, updated_at=now(),
			resolved_at = CASE WHEN $2 IN ('resolved','completed','cancelled') THEN COALESCE(resolved_at, now()) ELSE NULL END
		WHERE id=$1`, id, status)
	if err != nil {
		return Incident{}, err
	}
	if tag.RowsAffected() == 0 {
		return Incident{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `INSERT INTO incident_updates (incident_id, status, body, created_by) VALUES ($1,$2,$3,$4)`,
		id, status, body, nullID(by)); err != nil {
		return Incident{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Incident{}, err
	}
	return s.GetIncident(ctx, id)
}

func (s *Store) DeleteIncident(ctx context.Context, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM incidents WHERE id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ActiveMaintenanceMonitors lists monitors under in-progress maintenance.
func (s *Store) ActiveMaintenanceMonitors(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT m.monitor_id FROM incident_monitors m
		JOIN incidents i ON i.id = m.incident_id
		WHERE i.kind = 'maintenance' AND i.status = 'in_progress'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Transition is an automatic maintenance status change.
type Transition struct {
	ID     int64
	Status string
}

// AdvanceMaintenance starts and completes windows whose time has come
// (only those with auto_transition), posting an update for each.
func (s *Store) AdvanceMaintenance(ctx context.Context, now time.Time) ([]Transition, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rows, err := tx.Query(ctx, `
		UPDATE incidents SET
			status = CASE WHEN scheduled_end <= $1 THEN 'completed' ELSE 'in_progress' END,
			resolved_at = CASE WHEN scheduled_end <= $1 THEN $1 END,
			updated_at = $1
		WHERE kind = 'maintenance' AND auto_transition
		  AND ((status = 'scheduled' AND scheduled_start <= $1) OR (status = 'in_progress' AND scheduled_end <= $1))
		RETURNING id, status`, now)
	if err != nil {
		return nil, err
	}
	var out []Transition
	for rows.Next() {
		var t Transition
		if err := rows.Scan(&t.ID, &t.Status); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	rows.Close()
	for _, t := range out {
		body := "Maintenance has started."
		if t.Status == "completed" {
			body = "Maintenance is complete."
		}
		if _, err := tx.Exec(ctx, `INSERT INTO incident_updates (incident_id, status, body, created_at) VALUES ($1,$2,$3,$4)`,
			t.ID, t.Status, body, now); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}
