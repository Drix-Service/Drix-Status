package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------- authenticator app ----------

// TOTPSecret returns the user's encrypted authenticator secret ("" if none).
func (s *Store) TOTPSecret(ctx context.Context, userID int64) (string, error) {
	var enc string
	err := s.DB.QueryRow(ctx, `SELECT totp_secret_enc FROM users WHERE id=$1 AND totp_enabled_at IS NOT NULL`, userID).Scan(&enc)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return enc, err
}

func (s *Store) EnableTOTP(ctx context.Context, userID int64, secretEnc string) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET totp_secret_enc=$2, totp_enabled_at=now() WHERE id=$1`, userID, secretEnc)
	return err
}

func (s *Store) DisableTOTP(ctx context.Context, userID int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET totp_secret_enc='', totp_enabled_at=NULL WHERE id=$1`, userID)
	return err
}

// ---------- recovery codes ----------

// ReplaceRecoveryCodes swaps the user's codes for new ones (hashes only).
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID int64, hashes []string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id=$1`, userID); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.Exec(ctx, `INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1,$2)`, userID, h); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// UseRecoveryCode marks a code used; false if it doesn't exist or was used.
func (s *Store) UseRecoveryCode(ctx context.Context, userID int64, hash string) (bool, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE recovery_codes SET used_at=now()
		WHERE id = (SELECT id FROM recovery_codes WHERE user_id=$1 AND code_hash=$2 AND used_at IS NULL LIMIT 1)`, userID, hash)
	return tag.RowsAffected() == 1, err
}

func (s *Store) RecoveryCodesLeft(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM recovery_codes WHERE user_id=$1 AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

// ---------- passkeys ----------

type Passkey struct {
	ID           int64      `json:"id"`
	UserID       int64      `json:"-"`
	CredentialID []byte     `json:"-"`
	Credential   []byte     `json:"-"` // webauthn.Credential as JSON
	Name         string     `json:"name"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

const passkeyCols = `id, user_id, credential_id, credential, name, created_at, last_used_at`

func scanPasskey(row pgx.Row) (Passkey, error) {
	var p Passkey
	err := row.Scan(&p.ID, &p.UserID, &p.CredentialID, &p.Credential, &p.Name, &p.CreatedAt, &p.LastUsedAt)
	return p, err
}

func (s *Store) ListPasskeys(ctx context.Context, userID int64) ([]Passkey, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+passkeyCols+` FROM passkeys WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Passkey{}
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PasskeyByCredentialID(ctx context.Context, credID []byte) (Passkey, error) {
	p, err := scanPasskey(s.DB.QueryRow(ctx, `SELECT `+passkeyCols+` FROM passkeys WHERE credential_id=$1`, credID))
	return p, notFound(err)
}

func (s *Store) AddPasskey(ctx context.Context, userID int64, credID, cred []byte, name string) (Passkey, error) {
	return scanPasskey(s.DB.QueryRow(ctx, `INSERT INTO passkeys (user_id, credential_id, credential, name)
		VALUES ($1,$2,$3,$4) RETURNING `+passkeyCols, userID, credID, cred, name))
}

// PasskeyUsed stores the updated credential (sign counter) after a sign-in.
func (s *Store) PasskeyUsed(ctx context.Context, id int64, cred []byte) error {
	_, err := s.DB.Exec(ctx, `UPDATE passkeys SET credential=$2, last_used_at=now() WHERE id=$1`, id, cred)
	return err
}

func (s *Store) RenamePasskey(ctx context.Context, userID, id int64, name string) (Passkey, error) {
	p, err := scanPasskey(s.DB.QueryRow(ctx, `UPDATE passkeys SET name=$3 WHERE id=$2 AND user_id=$1 RETURNING `+passkeyCols, userID, id, name))
	return p, notFound(err)
}

func (s *Store) DeletePasskey(ctx context.Context, userID, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM passkeys WHERE id=$2 AND user_id=$1`, userID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ResetTwoFactor removes every second factor of a user (owner action).
func (s *Store) ResetTwoFactor(ctx context.Context, userID int64) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{
		`UPDATE users SET totp_secret_enc='', totp_enabled_at=NULL WHERE id=$1`,
		`DELETE FROM recovery_codes WHERE user_id=$1`,
		`DELETE FROM passkeys WHERE user_id=$1`,
	} {
		if _, err := tx.Exec(ctx, q, userID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
