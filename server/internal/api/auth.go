package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"drix/server/internal/rkeys"
	"drix/server/internal/store"
)

const (
	cookieName = "drix_session"
	sessionTTL = 7 * 24 * time.Hour
	bcryptCost = 12
)

type ctxKey struct{}

func userFrom(ctx context.Context) store.User {
	u, _ := ctx.Value(ctxKey{}).(store.User)
	return u
}

func hashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// auth requires a signed-in user of any role.
func (a *API) auth(next http.HandlerFunc) http.Handler { return a.role(store.RoleViewer, next) }

// role requires a signed-in user, or an API key, with at least the given role.
func (a *API) role(min string, next http.HandlerFunc) http.Handler { return a.access(min, true, next) }

// session is like role but refuses API keys: for managing keys, the team
// and passwords, which should always need a person signed in.
func (a *API) session(min string, next http.HandlerFunc) http.Handler {
	return a.access(min, false, next)
}

func (a *API) access(min string, allowKey bool, next http.HandlerFunc) http.Handler {
	return a.accessFull(min, allowKey, false, next)
}

// setupOK is for the routes someone who must still set up two-factor sign-in
// can use: their own account, two-factor setup and passkeys.
func (a *API) setupOK(next http.HandlerFunc) http.Handler {
	return a.accessFull(store.RoleViewer, false, true, next)
}

func (a *API) accessFull(min string, allowKey, allowSetup bool, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("Authorization"); h != "" {
			if !allowKey {
				writeErr(w, http.StatusForbidden, "API keys can't do this. Sign in to the admin instead.")
				return
			}
			a.withAPIKey(w, r, h, min, next)
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil || c.Value == "" {
			writeErr(w, http.StatusUnauthorized, "not signed in")
			return
		}
		key := rkeys.Session(hashToken(c.Value))
		idStr, err := a.Redis.Get(r.Context(), key).Result()
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "session expired")
			return
		}
		id, _ := strconv.ParseInt(idStr, 10, 64)
		user, err := a.Store.UserByID(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "session invalid")
			return
		}
		if !allowSetup && !user.TwoFactor && a.requireTwoFactor(r.Context()) {
			writeJSON(w, http.StatusForbidden, apiError{Error: "Set up two-factor sign-in to continue.", Code: "two_factor_setup_required"})
			return
		}
		if !store.RoleAtLeast(user.Role, min) {
			writeErr(w, http.StatusForbidden, "your role ("+user.Role+") can't do this. Ask an "+min+" or owner.")
			return
		}
		// Sliding expiration.
		a.Redis.Expire(r.Context(), key, sessionTTL)
		a.Redis.Expire(r.Context(), rkeys.UserSessions(user.ID), sessionTTL)
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
	})
}

// ---------- API keys ----------

const apiKeyPrefix = "drix_"

type apiKeyCtx struct{}

// apiKeyFrom returns the name of the API key behind the request, if any.
func apiKeyFrom(ctx context.Context) string {
	n, _ := ctx.Value(apiKeyCtx{}).(string)
	return n
}

// withAPIKey authenticates "Authorization: Bearer drix_…". The request runs
// as the key's creator, limited to the key's role (and never above the
// creator's current role).
func (a *API) withAPIKey(w http.ResponseWriter, r *http.Request, header, min string, next http.HandlerFunc) {
	ctx := r.Context()
	token, ok := strings.CutPrefix(header, "Bearer ")
	token = strings.TrimSpace(token)
	if !ok || !strings.HasPrefix(token, apiKeyPrefix) {
		writeErr(w, http.StatusUnauthorized, "expected Authorization: Bearer drix_…")
		return
	}
	limitKey := "drix:apikey:fail:" + remoteIP(r)
	if n, _ := a.Redis.Get(ctx, limitKey).Int(); n >= 20 {
		writeErr(w, http.StatusTooManyRequests, "too many invalid API keys; try again later")
		return
	}
	k, err := a.Store.APIKeyByHash(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			a.throttle(ctx, limitKey, 20, 15*time.Minute)
			writeErr(w, http.StatusUnauthorized, "invalid API key")
			return
		}
		a.serverErr(w, err)
		return
	}
	user, err := a.Store.UserByID(ctx, k.CreatedBy)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid API key")
		return
	}
	// The key's role, capped by what its creator can do today.
	role := k.Role
	if !store.RoleAtLeast(user.Role, role) {
		role = user.Role
	}
	if !store.RoleAtLeast(role, min) {
		writeErr(w, http.StatusForbidden, "this API key's role ("+role+") can't do this. It needs "+min+".")
		return
	}
	user.Role = role
	// last_used_at, at most once a minute per key.
	if ok, _ := a.Redis.SetNX(ctx, "drix:apikey:used:"+strconv.FormatInt(k.ID, 10), "1", time.Minute).Result(); ok {
		if err := a.Store.TouchAPIKey(ctx, k.ID); err != nil {
			a.Log.Warn("api key touch", "err", err)
		}
	}
	ctx = context.WithValue(ctx, ctxKey{}, user)
	ctx = context.WithValue(ctx, apiKeyCtx{}, k.Name)
	next(w, r.WithContext(ctx))
}

// startSession issues a session cookie for the user.
func (a *API) startSession(w http.ResponseWriter, r *http.Request, u store.User) error {
	tok := randomToken()
	h := hashToken(tok)
	_, err := a.Redis.TxPipelined(r.Context(), func(p redis.Pipeliner) error {
		p.Set(r.Context(), rkeys.Session(h), u.ID, sessionTTL)
		p.SAdd(r.Context(), rkeys.UserSessions(u.ID), h)
		p.Expire(r.Context(), rkeys.UserSessions(u.ID), sessionTTL)
		return nil
	})
	if err != nil {
		return err
	}
	a.Store.TouchLogin(r.Context(), u.ID)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: tok, Path: "/", HttpOnly: true, Secure: a.Cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()),
	})
	return nil
}

// revokeSessions signs a user out everywhere (role change, removal, password change).
func (a *API) revokeSessions(ctx context.Context, userID int64, keepHash string) {
	set := rkeys.UserSessions(userID)
	hashes, _ := a.Redis.SMembers(ctx, set).Result()
	for _, h := range hashes {
		if h == keepHash {
			continue
		}
		a.Redis.Del(ctx, rkeys.Session(h))
		a.Redis.SRem(ctx, set, h)
	}
}

func currentSessionHash(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil {
		return hashToken(c.Value)
	}
	return ""
}

func remoteIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// throttle allows `limit` hits per window for a key; returns false when exceeded.
func (a *API) throttle(ctx context.Context, key string, limit int64, window time.Duration) bool {
	n, err := a.Redis.Incr(ctx, key).Result()
	if err != nil {
		return true // fail open on Redis trouble
	}
	if n == 1 {
		a.Redis.Expire(ctx, key, window)
	}
	return n <= limit
}

// dummyHash keeps login timing similar for unknown emails.
const dummyHash = "$2a$12$C6UzMDM.H6dfI/f/IKcEeO4JXJ5yZB4p1O6S1uhy2q3uSpe8gcNlK"

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx := r.Context()
	lk := rkeys.LoginAttempts(remoteIP(r))
	if !a.throttle(ctx, lk, 10, 10*time.Minute) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}

	user, err := a.Store.UserByEmail(ctx, strings.TrimSpace(in.Email))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.serverErr(w, err)
		return
	}
	hash := user.PasswordHash
	if hash == "" {
		hash = dummyHash
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil || user.ID == 0 {
		writeErr(w, http.StatusUnauthorized, "incorrect email or password")
		return
	}
	if user.TwoFactor {
		a.startTwoFactor(w, r, user) // the attempt counter is cleared after the second step
		return
	}
	a.Redis.Del(ctx, lk)
	if err := a.startSession(w, r, user); err != nil {
		a.serverErr(w, err)
		return
	}
	a.auditAs(r, user, "user.login", "user", user.ID, "Signed in")
	writeJSON(w, http.StatusOK, user)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		h := hashToken(c.Value)
		if id, err := a.Redis.Get(r.Context(), rkeys.Session(h)).Int64(); err == nil {
			a.Redis.SRem(r.Context(), rkeys.UserSessions(id), h)
		}
		a.Redis.Del(r.Context(), rkeys.Session(h))
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"user":                      u,
		"two_factor_setup_required": !u.TwoFactor && apiKeyFrom(r.Context()) == "" && a.requireTwoFactor(r.Context()),
		"public_url":                a.Cfg.PublicURL,
		"status_url":                a.Cfg.StatusURL,
		"version":                   a.Version,
	})
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	user := userFrom(r.Context())
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(in.Current)) != nil {
		writeErr(w, http.StatusBadRequest, "current password is incorrect")
		return
	}
	if len(in.New) < 10 {
		writeErr(w, http.StatusBadRequest, "new password must be at least 10 characters")
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(in.New), bcryptCost)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if err := a.Store.SetUserPassword(r.Context(), user.ID, string(h)); err != nil {
		a.serverErr(w, err)
		return
	}
	// Sign out other devices, keep this one.
	a.revokeSessions(r.Context(), user.ID, currentSessionHash(r))
	a.audit(r, "user.password", "user", user.ID, "Changed password")
	w.WriteHeader(http.StatusNoContent)
}

// EnsureOwner creates the first account (an owner) from env vars if no users exist.
func EnsureOwner(ctx context.Context, st *store.Store, email, password string) (bool, error) {
	n, err := st.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	if email == "" || password == "" {
		return false, errors.New("no users exist: set ADMIN_EMAIL and ADMIN_PASSWORD for the first start")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return false, err
	}
	_, err = st.CreateUser(ctx, email, "Owner", string(h), store.RoleOwner)
	return err == nil, err
}
