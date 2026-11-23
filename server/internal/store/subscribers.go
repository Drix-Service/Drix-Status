package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Subscriber struct {
	ID          int64      `json:"id"`
	PageID      int64      `json:"page_id"`
	Email       string     `json:"email"`
	Token       string     `json:"-"`
	ConfirmedAt *time.Time `json:"confirmed_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

const subCols = `id, page_id, email, token, confirmed_at, created_at`

func scanSub(row pgx.Row) (Subscriber, error) {
	var s Subscriber
	err := row.Scan(&s.ID, &s.PageID, &s.Email, &s.Token, &s.ConfirmedAt, &s.CreatedAt)
	return s, err
}

// Subscribe adds a pending subscriber, or returns the existing one.
func (s *Store) Subscribe(ctx context.Context, pageID int64, email, token string) (Subscriber, error) {
	sub, err := scanSub(s.DB.QueryRow(ctx, `INSERT INTO subscribers (page_id, email, token) VALUES ($1,$2,$3)
		ON CONFLICT (page_id, lower(email)) DO UPDATE SET email = subscribers.email
		RETURNING `+subCols, pageID, email, token))
	return sub, err
}

// ConfirmSubscriber marks a subscription confirmed by its token.
func (s *Store) ConfirmSubscriber(ctx context.Context, token string) (Subscriber, error) {
	sub, err := scanSub(s.DB.QueryRow(ctx, `UPDATE subscribers SET confirmed_at = COALESCE(confirmed_at, now())
		WHERE token=$1 RETURNING `+subCols, token))
	return sub, notFound(err)
}

// Unsubscribe deletes a subscription by its token.
func (s *Store) Unsubscribe(ctx context.Context, token string) (Subscriber, error) {
	sub, err := scanSub(s.DB.QueryRow(ctx, `DELETE FROM subscribers WHERE token=$1 RETURNING `+subCols, token))
	return sub, notFound(err)
}

func (s *Store) DeleteSubscriber(ctx context.Context, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM subscribers WHERE id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ListSubscribers lists a page's subscribers, optionally filtered by email.
func (s *Store) ListSubscribers(ctx context.Context, pageID int64, q string, limit int) ([]Subscriber, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+subCols+` FROM subscribers
		WHERE page_id=$1 AND ($2 = '' OR email ILIKE '%' || $2 || '%') ORDER BY created_at DESC LIMIT $3`, pageID, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscriber{}
	for rows.Next() {
		sub, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// SubscriberCounts returns confirmed and pending counts per page.
func (s *Store) SubscriberCounts(ctx context.Context) (map[int64][2]int, error) {
	rows, err := s.DB.Query(ctx, `SELECT page_id, COUNT(*) FILTER (WHERE confirmed_at IS NOT NULL), COUNT(*) FILTER (WHERE confirmed_at IS NULL)
		FROM subscribers GROUP BY page_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][2]int{}
	for rows.Next() {
		var id int64
		var c, p int
		if err := rows.Scan(&id, &c, &p); err != nil {
			return nil, err
		}
		out[id] = [2]int{c, p}
	}
	return out, rows.Err()
}

// ConfirmedSubscribers returns confirmed subscribers of the given pages,
// one row per email (the first matching page wins).
func (s *Store) ConfirmedSubscribers(ctx context.Context, pageIDs []int64) ([]Subscriber, error) {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT ON (lower(email)) `+subCols+` FROM subscribers
		WHERE page_id = ANY($1) AND confirmed_at IS NOT NULL ORDER BY lower(email), page_id`, pageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscriber{}
	for rows.Next() {
		sub, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// PruneUnconfirmed drops subscriptions never confirmed within a week.
func (s *Store) PruneUnconfirmed(ctx context.Context) (int64, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM subscribers WHERE confirmed_at IS NULL AND created_at < now() - interval '7 days'`)
	return tag.RowsAffected(), err
}
