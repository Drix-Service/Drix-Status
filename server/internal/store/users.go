package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Roles, from least to most privileged.
const (
	RoleViewer = "viewer"
	RoleEditor = "editor"
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
)

var roleRank = map[string]int{RoleViewer: 1, RoleEditor: 2, RoleAdmin: 3, RoleOwner: 4}

// ValidRole reports whether r is a known role.
func ValidRole(r string) bool { return roleRank[r] > 0 }

// RoleAtLeast reports whether role has at least the privileges of min.
func RoleAtLeast(role, min string) bool { return roleRank[role] >= roleRank[min] && roleRank[min] > 0 }

// ErrLastOwner protects the team from losing its final owner.
var ErrLastOwner = errors.New("the team must keep at least one owner")

type User struct {
	ID           int64      `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Role         string     `json:"role"`
	PasswordHash string     `json:"-"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	TOTPEnabled  bool       `json:"-"`
	Passkeys     int        `json:"-"`
	TwoFactor    bool       `json:"two_factor"` // TOTP or at least one passkey
}

const userCols = `id, email, name, role, password_hash, created_at, last_login_at, totp_enabled_at IS NOT NULL,
	(SELECT count(*) FROM passkeys pk WHERE pk.user_id = users.id)`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &u.CreatedAt, &u.LastLoginAt, &u.TOTPEnabled, &u.Passkeys)
	u.TwoFactor = u.TOTPEnabled || u.Passkeys > 0
	return u, err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	u, err := scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(email)=lower($1)`, email))
	return u, notFound(err)
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	u, err := scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
	return u, notFound(err)
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+userCols+` FROM users
		ORDER BY CASE role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'editor' THEN 2 ELSE 3 END, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, email, name, hash, role string) (User, error) {
	return scanUser(s.DB.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, role)
		VALUES ($1,$2,$3,$4) RETURNING `+userCols, email, name, hash, role))
}

func (s *Store) SetUserPassword(ctx context.Context, id int64, hash string) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, id, hash)
	return err
}

func (s *Store) TouchLogin(ctx context.Context, id int64) {
	_, _ = s.DB.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, id)
}

// UpdateUser changes name and role, refusing to demote the last owner.
// The check and the update share a transaction with a row lock on owners.
func (s *Store) UpdateUser(ctx context.Context, id int64, name, role string) (User, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var owners int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM (SELECT id FROM users WHERE role='owner' FOR UPDATE) o`).Scan(&owners); err != nil {
		return User{}, err
	}
	cur, err := scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
	if err != nil {
		return User{}, notFound(err)
	}
	if cur.Role == RoleOwner && role != RoleOwner && owners <= 1 {
		return User{}, ErrLastOwner
	}
	u, err := scanUser(tx.QueryRow(ctx, `UPDATE users SET name=$2, role=$3 WHERE id=$1 RETURNING `+userCols, id, name, role))
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}

// DeleteUser removes a user, refusing to remove the last owner.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var owners int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM (SELECT id FROM users WHERE role='owner' FOR UPDATE) o`).Scan(&owners); err != nil {
		return err
	}
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1`, id).Scan(&role); err != nil {
		return notFound(err)
	}
	if role == RoleOwner && owners <= 1 {
		return ErrLastOwner
	}
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------- Invites ----------

type Invite struct {
	ID         int64      `json:"id"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	TokenHash  string     `json:"-"`
	ExpiresAt  time.Time  `json:"expires_at"`
	CreatedBy  *int64     `json:"created_by"`
	AcceptedAt *time.Time `json:"accepted_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

const inviteCols = `id, email, role, token_hash, expires_at, created_by, accepted_at, created_at`

func scanInvite(row pgx.Row) (Invite, error) {
	var i Invite
	err := row.Scan(&i.ID, &i.Email, &i.Role, &i.TokenHash, &i.ExpiresAt, &i.CreatedBy, &i.AcceptedAt, &i.CreatedAt)
	return i, err
}

func (s *Store) CreateInvite(ctx context.Context, email, role, tokenHash string, expires time.Time, by int64) (Invite, error) {
	// A new invite for the same email replaces any pending one.
	_, _ = s.DB.Exec(ctx, `DELETE FROM invites WHERE lower(email)=lower($1) AND accepted_at IS NULL`, email)
	return scanInvite(s.DB.QueryRow(ctx, `INSERT INTO invites (email, role, token_hash, expires_at, created_by)
		VALUES ($1,$2,$3,$4,$5) RETURNING `+inviteCols, email, role, tokenHash, expires, by))
}

func (s *Store) PendingInvites(ctx context.Context) ([]Invite, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+inviteCols+` FROM invites WHERE accepted_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) InviteByTokenHash(ctx context.Context, h string) (Invite, error) {
	i, err := scanInvite(s.DB.QueryRow(ctx, `SELECT `+inviteCols+` FROM invites WHERE token_hash=$1`, h))
	return i, notFound(err)
}

func (s *Store) DeleteInvite(ctx context.Context, id int64) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM invites WHERE id=$1 AND accepted_at IS NULL`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// AcceptInvite creates the user and marks the invite used, atomically.
func (s *Store) AcceptInvite(ctx context.Context, inv Invite, name, hash string) (User, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `UPDATE invites SET accepted_at=now() WHERE id=$1 AND accepted_at IS NULL AND expires_at > now()`, inv.ID)
	if err != nil {
		return User{}, err
	}
	if tag.RowsAffected() == 0 {
		return User{}, ErrNotFound
	}
	u, err := scanUser(tx.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, role)
		VALUES ($1,$2,$3,$4) RETURNING `+userCols, inv.Email, name, hash, inv.Role))
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}

// ---------- Audit log ----------

type AuditEntry struct {
	ID         int64     `json:"id"`
	UserID     *int64    `json:"user_id"`
	UserEmail  string    `json:"user_email"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Summary    string    `json:"summary"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Store) InsertAudit(ctx context.Context, e AuditEntry) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO audit_log (user_id, user_email, action, target_type, target_id, summary, ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, e.UserID, e.UserEmail, e.Action, e.TargetType, e.TargetID, e.Summary, e.IP)
	return err
}

// ListAudit returns entries older than `before` (0 = newest), newest first.
func (s *Store) ListAudit(ctx context.Context, before int64, limit int) ([]AuditEntry, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, user_id, user_email, action, target_type, target_id, summary, ip, created_at
		FROM audit_log WHERE ($1 = 0 OR id < $1) ORDER BY id DESC LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.UserEmail, &e.Action, &e.TargetType, &e.TargetID, &e.Summary, &e.IP, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
