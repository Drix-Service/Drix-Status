package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Settings are global monitoring options. Branding lives on each status page.
type Settings struct {
	RetentionDays   int `json:"retention_days"`
	DefaultInterval int `json:"default_interval_s"`
	DefaultTimeout  int `json:"default_timeout_ms"`
}

func DefaultSettings() Settings {
	return Settings{RetentionDays: 30, DefaultInterval: 60, DefaultTimeout: 10000}
}

func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	out := DefaultSettings()
	var raw []byte
	err := s.DB.QueryRow(ctx, `SELECT value FROM settings WHERE key='site'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out) // older rows may carry extra (branding) keys; they're ignored
	return out, err
}

func (s *Store) SaveSettings(ctx context.Context, v Settings) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('site', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, raw)
	return err
}

// GetJSONSetting / SaveJSONSetting store arbitrary JSON blobs under a key.
func (s *Store) GetJSONSetting(ctx context.Context, key string, v any) (bool, error) {
	var raw []byte
	err := s.DB.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(raw, v)
}

func (s *Store) SaveJSONSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, raw)
	return err
}
