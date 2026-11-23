package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"drix/server/internal/store"
)

const inviteTTL = 7 * 24 * time.Hour

// audit records who did what. Failures are logged, never surfaced.
func (a *API) audit(r *http.Request, action, targetType string, targetID any, summary string) {
	a.auditAs(r, userFrom(r.Context()), action, targetType, targetID, summary)
}

func (a *API) auditAs(r *http.Request, u store.User, action, targetType string, targetID any, summary string) {
	e := store.AuditEntry{
		UserEmail: u.Email, Action: action, TargetType: targetType,
		TargetID: fmt.Sprint(targetID), Summary: truncateStr(summary, 300), IP: remoteIP(r),
	}
	if u.ID != 0 {
		id := u.ID
		e.UserID = &id
	}
	if k := apiKeyFrom(r.Context()); k != "" {
		e.UserEmail += " (API key: " + k + ")"
	}
	if targetID == nil {
		e.TargetID = ""
	}
	if err := a.Store.InsertAudit(r.Context(), e); err != nil {
		a.Log.Warn("audit insert failed", "err", err, "action", action)
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ---------- Users ----------

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.Store.ListUsers(r.Context())
	if err != nil {
		a.serverErr(w, err)
		return
	}
	invites, err := a.Store.PendingInvites(r.Context())
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users, "invites": invites})
}

// canManage reports whether actor may change target's role/membership.
// Only owners may touch owners or grant the owner role.
func canManage(actor store.User, targetRole, newRole string) bool {
	if !store.RoleAtLeast(actor.Role, store.RoleAdmin) {
		return false
	}
	if (targetRole == store.RoleOwner || newRole == store.RoleOwner) && actor.Role != store.RoleOwner {
		return false
	}
	return true
}

func (a *API) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil || !store.ValidRole(in.Role) {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	actor := userFrom(r.Context())
	target, err := a.Store.UserByID(r.Context(), id)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if !canManage(actor, target.Role, in.Role) {
		writeErr(w, http.StatusForbidden, "only owners can change owners or make someone an owner")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = target.Name
	}
	u, err := a.Store.UpdateUser(r.Context(), id, truncateStr(name, 80), in.Role)
	if errors.Is(err, store.ErrLastOwner) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if u.Role != target.Role {
		// New permissions apply immediately: sign them out everywhere
		// except the actor's own session when editing themselves.
		keep := ""
		if actor.ID == u.ID {
			keep = currentSessionHash(r)
		}
		a.revokeSessions(r.Context(), u.ID, keep)
		a.audit(r, "user.role", "user", u.ID, fmt.Sprintf("Changed %s from %s to %s", u.Email, target.Role, u.Role))
	} else {
		a.audit(r, "user.update", "user", u.ID, "Renamed "+u.Email)
	}
	writeJSON(w, http.StatusOK, u)
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	actor := userFrom(r.Context())
	if actor.ID == id {
		writeErr(w, http.StatusBadRequest, "you can't remove yourself; ask another admin")
		return
	}
	target, err := a.Store.UserByID(r.Context(), id)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if !canManage(actor, target.Role, target.Role) {
		writeErr(w, http.StatusForbidden, "only owners can remove owners")
		return
	}
	if err := a.Store.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrLastOwner) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		a.serverErr(w, err)
		return
	}
	a.revokeSessions(r.Context(), id, "")
	a.audit(r, "user.remove", "user", id, "Removed "+target.Email)
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Invites ----------

func (a *API) createInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	email := strings.TrimSpace(strings.ToLower(in.Email))
	if _, err := mail.ParseAddress(email); err != nil || len(email) > 254 {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Enter a valid email address", Field: map[string]string{"email": "Enter a valid email address"}})
		return
	}
	if !store.ValidRole(in.Role) {
		writeErr(w, http.StatusBadRequest, "invalid role")
		return
	}
	actor := userFrom(r.Context())
	if !canManage(actor, "", in.Role) {
		writeErr(w, http.StatusForbidden, "only owners can invite owners")
		return
	}
	if _, err := a.Store.UserByEmail(r.Context(), email); err == nil {
		writeJSON(w, http.StatusConflict, apiError{Error: "This person is already on the team", Field: map[string]string{"email": "Already a member"}})
		return
	}
	tok := randomToken()
	inv, err := a.Store.CreateInvite(r.Context(), email, in.Role, hashToken(tok), time.Now().Add(inviteTTL), actor.ID)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	link := strings.TrimRight(a.Cfg.AdminURL, "/") + "/accept-invite?token=" + tok
	emailed := false
	if a.Mailer != nil {
		emailed = a.Mailer.SendInvite(r.Context(), email, actor, in.Role, link) == nil
	}
	a.audit(r, "invite.create", "invite", inv.ID, fmt.Sprintf("Invited %s as %s", email, in.Role))
	writeJSON(w, http.StatusCreated, map[string]any{"invite": inv, "link": link, "emailed": emailed})
}

func (a *API) deleteInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := a.Store.DeleteInvite(r.Context(), id); err != nil {
		a.serverErr(w, err)
		return
	}
	a.audit(r, "invite.revoke", "invite", id, "Revoked invite")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) lookupInvite(r *http.Request, tok string) (store.Invite, int, string) {
	if tok == "" {
		return store.Invite{}, http.StatusBadRequest, "missing invite token"
	}
	inv, err := a.Store.InviteByTokenHash(r.Context(), hashToken(tok))
	if err != nil {
		return inv, http.StatusNotFound, "This invite link is invalid. Ask for a new one."
	}
	if inv.AcceptedAt != nil {
		return inv, http.StatusGone, "This invite has already been used. Sign in instead."
	}
	if time.Now().After(inv.ExpiresAt) {
		return inv, http.StatusGone, "This invite has expired. Ask for a new one."
	}
	return inv, 0, ""
}

// inviteInfo lets the accept page show who the invite is for (no auth).
func (a *API) inviteInfo(w http.ResponseWriter, r *http.Request) {
	inv, code, msg := a.lookupInvite(r, r.URL.Query().Get("token"))
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": inv.Email, "role": inv.Role, "expires_at": inv.ExpiresAt})
}

func (a *API) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !a.throttle(r.Context(), "drix:invite-accept:"+remoteIP(r), 20, 10*time.Minute) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	inv, code, msg := a.lookupInvite(r, in.Token)
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 80 {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Enter your name", Field: map[string]string{"name": "Required"}})
		return
	}
	if len(in.Password) < 10 {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Password too short", Field: map[string]string{"password": "Use at least 10 characters"}})
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcryptCost)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	u, err := a.Store.AcceptInvite(r.Context(), inv, name, string(h))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusGone, "This invite is no longer valid.")
			return
		}
		writeErr(w, http.StatusConflict, "An account with this email already exists. Sign in instead.")
		return
	}
	if err := a.startSession(w, r, u); err != nil {
		a.serverErr(w, err)
		return
	}
	a.auditAs(r, u, "invite.accept", "user", u.ID, fmt.Sprintf("%s joined as %s", u.Email, u.Role))
	writeJSON(w, http.StatusOK, u)
}

// ---------- Audit ----------

func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	entries, err := a.Store.ListAudit(r.Context(), before, queryInt(r, "limit", 50, 1, 200))
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}
