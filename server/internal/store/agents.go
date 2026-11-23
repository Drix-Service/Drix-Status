package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------- Agents ----------

type Agent struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Region       string     `json:"region"`
	TokenHash    string     `json:"-"`
	TokenPrefix  string     `json:"token_prefix"`
	Disabled     bool       `json:"disabled"`
	CreatedAt    time.Time  `json:"created_at"`
	LastSeenAt   *time.Time `json:"last_seen_at"`
	LastHostname string     `json:"last_hostname"`
	LastVersion  string     `json:"last_version"`
}

const agentCols = `id::text, name, region, token_hash, token_prefix, disabled, created_at, last_seen_at, last_hostname, last_version`

func scanAgent(row pgx.Row) (Agent, error) {
	var a Agent
	err := row.Scan(&a.ID, &a.Name, &a.Region, &a.TokenHash, &a.TokenPrefix, &a.Disabled, &a.CreatedAt,
		&a.LastSeenAt, &a.LastHostname, &a.LastVersion)
	return a, err
}

func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+agentCols+` FROM agents ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) AgentByTokenHash(ctx context.Context, hash string) (Agent, error) {
	a, err := scanAgent(s.DB.QueryRow(ctx, `SELECT `+agentCols+` FROM agents WHERE token_hash=$1`, hash))
	return a, notFound(err)
}

func (s *Store) CreateAgent(ctx context.Context, name, region, hash, prefix string) (Agent, error) {
	return scanAgent(s.DB.QueryRow(ctx, `INSERT INTO agents (name, region, token_hash, token_prefix)
		VALUES ($1,$2,$3,$4) RETURNING `+agentCols, name, region, hash, prefix))
}

func (s *Store) UpdateAgent(ctx context.Context, id, name, region string, disabled bool) (Agent, error) {
	a, err := scanAgent(s.DB.QueryRow(ctx, `UPDATE agents SET name=$2, region=$3, disabled=$4
		WHERE id=$1::uuid RETURNING `+agentCols, id, name, region, disabled))
	return a, notFound(err)
}

func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM agents WHERE id=$1::uuid`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) TouchAgent(ctx context.Context, id, hostname, version string) error {
	_, err := s.DB.Exec(ctx, `UPDATE agents SET last_seen_at=now(), last_hostname=$2, last_version=$3 WHERE id=$1::uuid`,
		id, hostname, version)
	return err
}
