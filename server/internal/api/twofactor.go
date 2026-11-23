package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"drix/server/internal/rkeys"
	"drix/server/internal/store"
	"drix/server/internal/totp"
)

const (
	ticketTTL      = 5 * time.Minute
	ticketAttempts = 5
	recoveryCount  = 10
)

// ---------- "require two-factor for everyone" ----------

type securitySettings struct {
	RequireTwoFactor bool `json:"require_two_factor"`
}

var secCache struct {
	sync.Mutex
	v  securitySettings
	at time.Time
}

func (a *API) security(ctx context.Context) securitySettings {
	secCache.Lock()
	defer secCache.Unlock()
	if time.Since(secCache.at) < 5*time.Second {
		return secCache.v
	}
	var v securitySettings
	if _, err := a.Store.GetJSONSetting(ctx, "security", &v); err != nil {
		return secCache.v // keep the last known value on a DB hiccup
	}
	secCache.v, secCache.at = v, time.Now()
	return v
}

func (a *API) requireTwoFactor(ctx context.Context) bool { return a.security(ctx).RequireTwoFactor }

func (a *API) getSecurity(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.security(r.Context()))
}

func (a *API) putSecurity(w http.ResponseWriter, r *http.Request) {
	var in securitySettings
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	me := userFrom(r.Context())
	if in.RequireTwoFactor && !me.TwoFactor {
		writeErr(w, http.StatusConflict, "Turn on two-factor sign-in for your own account first.")
		return
	}
	if err := a.Store.SaveJSONSetting(r.Context(), "security", in); err != nil {
		a.serverErr(w, err)
		return
	}
	secCache.Lock()
	secCache.at = time.Time{}
	secCache.Unlock()
	if in.RequireTwoFactor {
		a.audit(r, "security.require_2fa", "settings", "security", "Required two-factor sign-in for everyone")
	} else {
		a.audit(r, "security.require_2fa", "settings", "security", "Stopped requiring two-factor sign-in")
	}
	writeJSON(w, http.StatusOK, in)
}

// ---------- WebAuthn ----------

type waUser struct {
	u     store.User
	keys  []store.Passkey
	creds []webauthn.Credential
}

func (w *waUser) WebAuthnID() []byte                         { return userHandle(w.u.ID) }
func (w *waUser) WebAuthnName() string                       { return w.u.Email }
func (w *waUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }
func (w *waUser) WebAuthnDisplayName() string {
	if w.u.Name != "" {
		return w.u.Name
	}
	return w.u.Email
}

func userHandle(id int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(id))
	return b
}

func (a *API) waUser(ctx context.Context, u store.User) (*waUser, error) {
	keys, err := a.Store.ListPasskeys(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	w := &waUser{u: u, keys: keys}
	for _, k := range keys {
		var c webauthn.Credential
		if err := json.Unmarshal(k.Credential, &c); err != nil {
			return nil, err
		}
		w.creds = append(w.creds, c)
	}
	return w, nil
}

// passkeyFor finds the stored passkey a credential belongs to.
func (w *waUser) passkeyFor(credID []byte) *store.Passkey {
	for i, c := range w.creds {
		if string(c.ID) == string(credID) {
			return &w.keys[i]
		}
	}
	return nil
}

// webAuthn is configured for the admin site's address: passkeys belong to
// that domain, so changing ADMIN_URL's host means adding them again.
func (a *API) webAuthn() (*webauthn.WebAuthn, error) {
	u, err := url.Parse(a.Cfg.AdminURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("ADMIN_URL is not a valid address")
	}
	return webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: "Drix Status",
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
	})
}

func (a *API) savePasskeyUse(ctx context.Context, w *waUser, cred *webauthn.Credential) {
	if pk := w.passkeyFor(cred.ID); pk != nil {
		if raw, err := json.Marshal(cred); err == nil {
			_ = a.Store.PasskeyUsed(ctx, pk.ID, raw)
		}
	}
}

func (a *API) putRedisJSON(ctx context.Context, key string, v any, ttl time.Duration) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return a.Redis.Set(ctx, key, raw, ttl).Err()
}

func (a *API) getRedisJSON(ctx context.Context, key string, v any) bool {
	raw, err := a.Redis.Get(ctx, key).Bytes()
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}

// ---------- sign-in: second step ----------

type loginTicket struct {
	UserID   int64                 `json:"user_id"`
	Attempts int                   `json:"attempts"`
	WA       *webauthn.SessionData `json:"wa,omitempty"`
}

// startTwoFactor answers a correct password for a user with two-factor on.
func (a *API) startTwoFactor(w http.ResponseWriter, r *http.Request, u store.User) {
	tok := randomToken()
	if err := a.putRedisJSON(r.Context(), rkeys.TwoFactorTicket(hashToken(tok)), loginTicket{UserID: u.ID}, ticketTTL); err != nil {
		a.serverErr(w, err)
		return
	}
	methods := []string{}
	if u.TOTPEnabled {
		methods = append(methods, "totp")
	}
	if u.Passkeys > 0 {
		methods = append(methods, "passkey")
	}
	methods = append(methods, "recovery")
	writeJSON(w, http.StatusOK, map[string]any{"two_factor": true, "ticket": tok, "methods": methods})
}

// ticket loads a pending sign-in, or writes an error.
func (a *API) ticket(w http.ResponseWriter, r *http.Request, tok string) (loginTicket, store.User, string, bool) {
	key := rkeys.TwoFactorTicket(hashToken(tok))
	var t loginTicket
	if tok == "" || !a.getRedisJSON(r.Context(), key, &t) {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "This sign-in expired. Enter your password again.", Code: "ticket_expired"})
		return t, store.User{}, "", false
	}
	u, err := a.Store.UserByID(r.Context(), t.UserID)
	if err != nil {
		a.Redis.Del(r.Context(), key)
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "This sign-in expired. Enter your password again.", Code: "ticket_expired"})
		return t, store.User{}, "", false
	}
	return t, u, key, true
}

// ticketFailed counts a wrong code; the ticket ends after a few.
func (a *API) ticketFailed(w http.ResponseWriter, r *http.Request, key string, t loginTicket, msg string) {
	t.Attempts++
	if t.Attempts >= ticketAttempts {
		a.Redis.Del(r.Context(), key)
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Too many wrong codes. Enter your password again.", Code: "ticket_expired"})
		return
	}
	ttl, _ := a.Redis.TTL(r.Context(), key).Result()
	if ttl <= 0 {
		ttl = time.Minute
	}
	_ = a.putRedisJSON(r.Context(), key, t, ttl)
	writeErr(w, http.StatusUnauthorized, msg)
}

func (a *API) finishLogin(w http.ResponseWriter, r *http.Request, key string, u store.User, how string) {
	a.Redis.Del(r.Context(), key)
	a.Redis.Del(r.Context(), rkeys.LoginAttempts(remoteIP(r)))
	if err := a.startSession(w, r, u); err != nil {
		a.serverErr(w, err)
		return
	}
	a.auditAs(r, u, "user.login", "user", u.ID, "Signed in ("+how+")")
	writeJSON(w, http.StatusOK, u)
}

func (a *API) loginTwoFactor(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ticket       string `json:"ticket"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !a.throttle(r.Context(), rkeys.LoginAttempts(remoteIP(r)), 20, 10*time.Minute) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	t, u, key, ok := a.ticket(w, r, in.Ticket)
	if !ok {
		return
	}
	ctx := r.Context()
	switch {
	case in.RecoveryCode != "":
		used, err := a.Store.UseRecoveryCode(ctx, u.ID, recoveryHash(in.RecoveryCode))
		if err != nil {
			a.serverErr(w, err)
			return
		}
		if !used {
			a.ticketFailed(w, r, key, t, "That recovery code didn't work. Each code works once.")
			return
		}
		a.auditAs(r, u, "user.recovery_code", "user", u.ID, "Used a recovery code")
		a.finishLogin(w, r, key, u, "recovery code")
	case in.Code != "":
		if !a.checkTOTP(ctx, u, in.Code) {
			a.ticketFailed(w, r, key, t, "That code didn't work. Check the time on your phone and try the newest code.")
			return
		}
		a.finishLogin(w, r, key, u, "authenticator app")
	default:
		writeErr(w, http.StatusBadRequest, "Enter the code from your authenticator app")
	}
}

// checkTOTP verifies a code against the user's secret; each code works once.
func (a *API) checkTOTP(ctx context.Context, u store.User, code string) bool {
	enc, err := a.Store.TOTPSecret(ctx, u.ID)
	if err != nil || enc == "" {
		return false
	}
	sec, err := a.Notify.Box().Open(enc)
	if err != nil {
		a.Log.Error("open authenticator secret", "user", u.ID, "err", err)
		return false
	}
	step, ok := totp.Verify(sec, code, time.Now())
	if !ok {
		return false
	}
	fresh, err := a.Redis.SetNX(ctx, rkeys.TOTPUsed(u.ID, step), "1", 3*totp.Period*time.Second).Result()
	return err == nil && fresh
}

func (a *API) loginPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ticket string `json:"ticket"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	t, u, key, ok := a.ticket(w, r, in.Ticket)
	if !ok {
		return
	}
	wa, err := a.webAuthn()
	if err != nil {
		a.serverErr(w, err)
		return
	}
	wu, err := a.waUser(r.Context(), u)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if len(wu.creds) == 0 {
		writeErr(w, http.StatusBadRequest, "You have no passkeys. Use your authenticator app or a recovery code.")
		return
	}
	opts, sess, err := wa.BeginLogin(wu, webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		a.serverErr(w, err)
		return
	}
	t.WA = sess
	ttl, _ := a.Redis.TTL(r.Context(), key).Result()
	if ttl <= 0 {
		ttl = time.Minute
	}
	_ = a.putRedisJSON(r.Context(), key, t, ttl)
	writeJSON(w, http.StatusOK, opts)
}

func (a *API) loginPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	t, u, key, ok := a.ticket(w, r, r.URL.Query().Get("ticket"))
	if !ok {
		return
	}
	if t.WA == nil {
		writeErr(w, http.StatusBadRequest, "Start the passkey sign-in again")
		return
	}
	wa, err := a.webAuthn()
	if err != nil {
		a.serverErr(w, err)
		return
	}
	wu, err := a.waUser(r.Context(), u)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	cred, err := wa.FinishLogin(wu, *t.WA, r)
	if err != nil {
		a.Log.Info("passkey second factor failed", "user", u.ID, "err", err)
		a.ticketFailed(w, r, key, t, "That passkey didn't work. Try again or use another way.")
		return
	}
	a.savePasskeyUse(r.Context(), wu, cred)
	a.finishLogin(w, r, key, u, "password + passkey")
}

// ---------- passwordless sign-in with a passkey ----------

func (a *API) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !a.throttle(r.Context(), rkeys.LoginAttempts(remoteIP(r)), 20, 10*time.Minute) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	wa, err := a.webAuthn()
	if err != nil {
		a.serverErr(w, err)
		return
	}
	opts, sess, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		a.serverErr(w, err)
		return
	}
	id := randomToken()
	if err := a.putRedisJSON(r.Context(), rkeys.PasskeyLogin(hashToken(id)), sess, ticketTTL); err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "options": opts})
}

func (a *API) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	key := rkeys.PasskeyLogin(hashToken(r.URL.Query().Get("id")))
	var sess webauthn.SessionData
	if !a.getRedisJSON(r.Context(), key, &sess) {
		writeErr(w, http.StatusUnauthorized, "This sign-in expired. Try again.")
		return
	}
	a.Redis.Del(r.Context(), key) // one try per challenge
	wa, err := a.webAuthn()
	if err != nil {
		a.serverErr(w, err)
		return
	}
	var found *waUser
	handler := func(rawID, handle []byte) (webauthn.User, error) {
		pk, err := a.Store.PasskeyByCredentialID(r.Context(), rawID)
		if err != nil {
			return nil, err
		}
		if string(handle) != string(userHandle(pk.UserID)) {
			return nil, errors.New("passkey belongs to another account")
		}
		u, err := a.Store.UserByID(r.Context(), pk.UserID)
		if err != nil {
			return nil, err
		}
		found, err = a.waUser(r.Context(), u)
		return found, err
	}
	_, cred, err := wa.FinishPasskeyLogin(handler, sess, r)
	if err != nil || found == nil {
		a.Log.Info("passkey sign-in failed", "err", err)
		writeErr(w, http.StatusUnauthorized, "That passkey isn't registered here. Sign in with your password, then add it under Settings → Account.")
		return
	}
	a.savePasskeyUse(r.Context(), found, cred)
	a.finishLogin(w, r, "", found.u, "passkey")
}

// ---------- your own two-factor settings ----------

type twoFactorOut struct {
	TOTP         bool            `json:"totp"`
	Passkeys     []store.Passkey `json:"passkeys"`
	RecoveryLeft int             `json:"recovery_left"`
	Required     bool            `json:"required"`
	CanTOTP      bool            `json:"can_totp"` // SECRET_KEY set
}

func (a *API) twoFactorState(ctx context.Context, u store.User) (twoFactorOut, error) {
	keys, err := a.Store.ListPasskeys(ctx, u.ID)
	if err != nil {
		return twoFactorOut{}, err
	}
	left, err := a.Store.RecoveryCodesLeft(ctx, u.ID)
	if err != nil {
		return twoFactorOut{}, err
	}
	return twoFactorOut{TOTP: u.TOTPEnabled, Passkeys: keys, RecoveryLeft: left, Required: a.requireTwoFactor(ctx), CanTOTP: a.Notify.Box() != nil}, nil
}

func (a *API) getTwoFactor(w http.ResponseWriter, r *http.Request) {
	u, err := a.Store.UserByID(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	out, err := a.twoFactorState(r.Context(), u)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) totpSetup(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	box := a.Notify.Box()
	if box == nil {
		writeErr(w, http.StatusConflict, "Set SECRET_KEY on the server to use an authenticator app. Passkeys work without it.")
		return
	}
	// A setup already in progress keeps its secret (the page may load twice).
	if enc, err := a.Redis.Get(r.Context(), rkeys.TOTPSetup(u.ID)).Result(); err == nil {
		if sec, err := box.Open(enc); err == nil {
			writeJSON(w, http.StatusOK, map[string]string{"secret": sec, "uri": totp.URI("Drix Status", u.Email, sec)})
			return
		}
	}
	sec := totp.NewSecret()
	enc, err := box.Seal(sec)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if err := a.Redis.Set(r.Context(), rkeys.TOTPSetup(u.ID), enc, 15*time.Minute).Err(); err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": sec, "uri": totp.URI("Drix Status", u.Email, sec)})
}

func (a *API) totpEnable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	u := userFrom(r.Context())
	ctx := r.Context()
	enc, err := a.Redis.Get(ctx, rkeys.TOTPSetup(u.ID)).Result()
	if errors.Is(err, redis.Nil) {
		writeErr(w, http.StatusConflict, "The setup expired. Start again to get a new QR code.")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	sec, err := a.Notify.Box().Open(enc)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	step, ok := totp.Verify(sec, in.Code, time.Now())
	if !ok {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "That code didn't match", Field: map[string]string{"code": "That code didn't match. Check the time on your phone and try the newest code."}})
		return
	}
	a.Redis.SetNX(ctx, rkeys.TOTPUsed(u.ID, step), "1", 3*totp.Period*time.Second)
	if err := a.Store.EnableTOTP(ctx, u.ID, enc); err != nil {
		a.serverErr(w, err)
		return
	}
	a.Redis.Del(ctx, rkeys.TOTPSetup(u.ID))
	a.audit(r, "user.2fa_totp_on", "user", u.ID, "Turned on the authenticator app")
	codes, err := a.ensureRecoveryCodes(ctx, u.ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// checkPassword is required before turning a factor off.
func (a *API) checkPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	u, err := a.Store.UserByID(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		a.serverErr(w, err)
		return false
	}
	if !a.throttle(r.Context(), rkeys.LoginAttempts(remoteIP(r)), 10, 10*time.Minute) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Wrong password", Field: map[string]string{"password": "That's not your current password"}})
		return false
	}
	return true
}

func (a *API) totpDisable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !a.checkPassword(w, r, in.Password) {
		return
	}
	u, _ := a.Store.UserByID(r.Context(), userFrom(r.Context()).ID)
	if u.Passkeys == 0 && a.requireTwoFactor(r.Context()) {
		writeErr(w, http.StatusConflict, "Two-factor sign-in is required on this team. Add a passkey before turning the app off.")
		return
	}
	if err := a.Store.DisableTOTP(r.Context(), u.ID); err != nil {
		a.serverErr(w, err)
		return
	}
	if u.Passkeys == 0 {
		_ = a.Store.ReplaceRecoveryCodes(r.Context(), u.ID, nil) // no second factor left
	}
	a.audit(r, "user.2fa_totp_off", "user", u.ID, "Turned off the authenticator app")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !a.checkPassword(w, r, in.Password) {
		return
	}
	u, _ := a.Store.UserByID(r.Context(), userFrom(r.Context()).ID)
	if !u.TwoFactor {
		writeErr(w, http.StatusConflict, "Turn on two-factor sign-in first")
		return
	}
	codes, hashes := newRecoveryCodes()
	if err := a.Store.ReplaceRecoveryCodes(r.Context(), u.ID, hashes); err != nil {
		a.serverErr(w, err)
		return
	}
	a.audit(r, "user.2fa_recovery_new", "user", u.ID, "Made new recovery codes")
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// ensureRecoveryCodes makes codes when someone turns on their first factor.
func (a *API) ensureRecoveryCodes(ctx context.Context, userID int64) ([]string, error) {
	left, err := a.Store.RecoveryCodesLeft(ctx, userID)
	if err != nil || left > 0 {
		return nil, err
	}
	codes, hashes := newRecoveryCodes()
	return codes, a.Store.ReplaceRecoveryCodes(ctx, userID, hashes)
}

const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func newRecoveryCodes() (codes, hashes []string) {
	for range recoveryCount {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		var s strings.Builder
		for i, c := range b {
			if i == 4 {
				s.WriteByte('-')
			}
			s.WriteByte(recoveryAlphabet[int(c)%len(recoveryAlphabet)])
		}
		codes = append(codes, s.String())
		hashes = append(hashes, recoveryHash(s.String()))
	}
	return codes, hashes
}

func recoveryHash(code string) string {
	norm := strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	h := sha256.Sum256([]byte("drix-recovery:" + norm))
	return hex.EncodeToString(h[:])
}

// ---------- passkeys ----------

func (a *API) passkeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	u, err := a.Store.UserByID(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	wa, err := a.webAuthn()
	if err != nil {
		a.serverErr(w, err)
		return
	}
	wu, err := a.waUser(r.Context(), u)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	var exclude []protocol.CredentialDescriptor
	for _, c := range wu.creds {
		exclude = append(exclude, c.Descriptor())
	}
	opts, sess, err := wa.BeginRegistration(wu,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(exclude))
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if err := a.putRedisJSON(r.Context(), rkeys.PasskeyRegistration(u.ID), sess, ticketTTL); err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

func (a *API) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := a.Store.UserByID(ctx, userFrom(ctx).ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	var sess webauthn.SessionData
	key := rkeys.PasskeyRegistration(u.ID)
	if !a.getRedisJSON(ctx, key, &sess) {
		writeErr(w, http.StatusConflict, "That took too long. Try adding the passkey again.")
		return
	}
	a.Redis.Del(ctx, key)
	wa, err := a.webAuthn()
	if err != nil {
		a.serverErr(w, err)
		return
	}
	wu, err := a.waUser(ctx, u)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	cred, err := wa.FinishRegistration(wu, sess, r)
	if err != nil {
		a.Log.Info("passkey registration failed", "user", u.ID, "err", err)
		writeErr(w, http.StatusBadRequest, "The passkey couldn't be added. Try again.")
		return
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "Passkey"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	pk, err := a.Store.AddPasskey(ctx, u.ID, cred.ID, raw, name)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			writeErr(w, http.StatusConflict, "That passkey is already added")
			return
		}
		a.serverErr(w, err)
		return
	}
	a.audit(r, "user.passkey_add", "user", u.ID, "Added passkey "+name)
	codes, err := a.ensureRecoveryCodes(ctx, u.ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkey": pk, "recovery_codes": codes})
}

func (a *API) renamePasskey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" || len(in.Name) > 60 {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Give it a name", Field: map[string]string{"name": "1–60 characters"}})
		return
	}
	pk, err := a.Store.RenamePasskey(r.Context(), userFrom(r.Context()).ID, id, strings.TrimSpace(in.Name))
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pk)
}

func (a *API) deletePasskey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx := r.Context()
	u, err := a.Store.UserByID(ctx, userFrom(ctx).ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	last := !u.TOTPEnabled && u.Passkeys <= 1
	if last && a.requireTwoFactor(ctx) {
		writeErr(w, http.StatusConflict, "Two-factor sign-in is required on this team. Add another passkey or the authenticator app first.")
		return
	}
	if err := a.Store.DeletePasskey(ctx, u.ID, id); err != nil {
		a.serverErr(w, err)
		return
	}
	if last {
		_ = a.Store.ReplaceRecoveryCodes(ctx, u.ID, nil)
	}
	a.audit(r, "user.passkey_remove", "user", u.ID, "Removed a passkey")
	w.WriteHeader(http.StatusNoContent)
}

// ---------- owner: reset someone's two-factor ----------

func (a *API) resetTwoFactor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	me := userFrom(r.Context())
	if id == me.ID {
		writeErr(w, http.StatusBadRequest, "Change your own two-factor settings under Settings → Account")
		return
	}
	u, err := a.Store.UserByID(r.Context(), id)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if err := a.Store.ResetTwoFactor(r.Context(), id); err != nil {
		a.serverErr(w, err)
		return
	}
	a.revokeSessions(r.Context(), id, "")
	a.audit(r, "user.2fa_reset", "user", id, "Reset two-factor sign-in for "+u.Email)
	w.WriteHeader(http.StatusNoContent)
}
