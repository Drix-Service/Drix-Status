package store

import (
	"context"
	"time"
)

// PushSub is one device subscribed to Web Push notifications.
type PushSub struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Endpoint   string     `json:"endpoint"` // shown to its owner only, to recognise this device
	P256dh     string     `json:"-"`
	Auth       string     `json:"-"`
	Events     []string   `json:"events"`
	Device     string     `json:"device"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

const pushCols = `id, user_id, endpoint, p256dh, auth, events, device, created_at, last_used_at`

func scanPush(row interface{ Scan(...any) error }) (PushSub, error) {
	var p PushSub
	err := row.Scan(&p.ID, &p.UserID, &p.Endpoint, &p.P256dh, &p.Auth, &p.Events, &p.Device, &p.CreatedAt, &p.LastUsedAt)
	return p, err
}

// UpsertPushSub saves a subscription; the same endpoint (device) is updated
// in place, and moves to the signed-in user.
func (s *Store) UpsertPushSub(ctx context.Context, p PushSub) (PushSub, error) {
	return scanPush(s.DB.QueryRow(ctx, `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, events, device)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (endpoint) DO UPDATE SET user_id=EXCLUDED.user_id, p256dh=EXCLUDED.p256dh,
			auth=EXCLUDED.auth, events=EXCLUDED.events, device=EXCLUDED.device
		RETURNING `+pushCols, p.UserID, p.Endpoint, p.P256dh, p.Auth, p.Events, p.Device))
}

func (s *Store) ListPushSubs(ctx context.Context, userID int64) ([]PushSub, error) {
	return s.queryPush(ctx, `SELECT `+pushCols+` FROM push_subscriptions WHERE user_id=$1 ORDER BY created_at`, userID)
}

// PushSubsFor returns every subscription that wants this event.
func (s *Store) PushSubsFor(ctx context.Context, event string) ([]PushSub, error) {
	return s.queryPush(ctx, `SELECT `+pushCols+` FROM push_subscriptions WHERE $1 = ANY(events)`, event)
}

func (s *Store) queryPush(ctx context.Context, q string, args ...any) ([]PushSub, error) {
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PushSub{}
	for rows.Next() {
		p, err := scanPush(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPushSub(ctx context.Context, id int64) (PushSub, error) {
	p, err := scanPush(s.DB.QueryRow(ctx, `SELECT `+pushCols+` FROM push_subscriptions WHERE id=$1`, id))
	return p, notFound(err)
}

func (s *Store) PushSubByEndpoint(ctx context.Context, endpoint string) (PushSub, error) {
	p, err := scanPush(s.DB.QueryRow(ctx, `SELECT `+pushCols+` FROM push_subscriptions WHERE endpoint=$1`, endpoint))
	return p, notFound(err)
}

func (s *Store) SetPushEvents(ctx context.Context, id, userID int64, events []string) (PushSub, error) {
	p, err := scanPush(s.DB.QueryRow(ctx, `UPDATE push_subscriptions SET events=$3 WHERE id=$1 AND user_id=$2 RETURNING `+pushCols, id, userID, events))
	return p, notFound(err)
}

// DeletePushSub removes a device; userID 0 skips the owner check.
func (s *Store) DeletePushSub(ctx context.Context, id, userID int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM push_subscriptions WHERE id=$1 AND ($2 = 0 OR user_id=$2)`, id, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) TouchPushSub(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE push_subscriptions SET last_used_at=now() WHERE id=$1`, id)
	return err
}

// CreateJSONSetting stores v under key only if nothing is there yet, and
// reports whether it did (so concurrent servers agree on one value).
func (s *Store) CreateJSONSetting(ctx context.Context, key string, raw []byte) (bool, error) {
	tag, err := s.DB.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, key, raw)
	return err == nil && tag.RowsAffected() == 1, err
}
