package store

import (
	"context"
	"time"
)

// APIKey is a key for scripts and CI. The key itself is never stored.
type APIKey struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Prefix       string     `json:"prefix"`
	Role         string     `json:"role"`
	CreatedBy    int64      `json:"created_by"`
	CreatedEmail string     `json:"created_by_email"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

const apiKeySelect = `SELECT k.id, k.name, k.prefix, k.role, k.created_by, u.email, k.created_at, k.last_used_at
	FROM api_keys k JOIN users u ON u.id = k.created_by`

func (s *Store) CreateAPIKey(ctx context.Context, k *APIKey, tokenHash string) error {
	return s.DB.QueryRow(ctx, `INSERT INTO api_keys (name, prefix, token_hash, role, created_by)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`, k.Name, k.Prefix, tokenHash, k.Role, k.CreatedBy).Scan(&k.ID, &k.CreatedAt)
}

func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.DB.Query(ctx, apiKeySelect+` ORDER BY k.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.Role, &k.CreatedBy, &k.CreatedEmail, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// APIKeyByHash finds a key by the hash of its token.
func (s *Store) APIKeyByHash(ctx context.Context, hash string) (APIKey, error) {
	var k APIKey
	err := s.DB.QueryRow(ctx, apiKeySelect+` WHERE k.token_hash=$1`, hash).
		Scan(&k.ID, &k.Name, &k.Prefix, &k.Role, &k.CreatedBy, &k.CreatedEmail, &k.CreatedAt, &k.LastUsedAt)
	return k, notFound(err)
}

func (s *Store) DeleteAPIKey(ctx context.Context, id int64) (APIKey, error) {
	var k APIKey
	err := s.DB.QueryRow(ctx, `DELETE FROM api_keys WHERE id=$1 RETURNING id, name, prefix, role`, id).Scan(&k.ID, &k.Name, &k.Prefix, &k.Role)
	return k, notFound(err)
}

func (s *Store) TouchAPIKey(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE api_keys SET last_used_at=now() WHERE id=$1`, id)
	return err
}
