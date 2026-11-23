package api

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"drix/server/internal/store"
)

func (a *API) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.Store.ListAPIKeys(r.Context())
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

// createAPIKey makes a key and returns it once; only its hash is kept.
func (a *API) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	u := userFrom(r.Context())
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > 80:
		writeValidation(w, fieldErr{"name", "Give the key a name (max 80 characters)"})
		return
	case in.Role != store.RoleViewer && in.Role != store.RoleEditor && in.Role != store.RoleAdmin:
		writeValidation(w, fieldErr{"role", "Pick viewer, editor or admin"})
		return
	case !store.RoleAtLeast(u.Role, in.Role):
		writeValidation(w, fieldErr{"role", "You can't create a key with more access than your own role (" + u.Role + ")"})
		return
	}
	token := apiKeyPrefix + randomToken()
	k := store.APIKey{Name: in.Name, Prefix: token[:12], Role: in.Role, CreatedBy: u.ID, CreatedEmail: u.Email}
	if err := a.Store.CreateAPIKey(r.Context(), &k, hashToken(token)); err != nil {
		a.serverErr(w, err)
		return
	}
	a.audit(r, "api_key.create", "api_key", k.ID, k.Name+" ("+k.Role+")")
	writeJSON(w, http.StatusCreated, struct {
		store.APIKey
		Token string `json:"token"`
	}{k, token})
}

func (a *API) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	k, err := a.Store.DeleteAPIKey(r.Context(), id)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.audit(r, "api_key.delete", "api_key", k.ID, k.Name)
	w.WriteHeader(http.StatusNoContent)
}
