package api

import (
	"testing"

	"drix/server/internal/store"
)

func TestRoleAtLeast(t *testing.T) {
	roles := []string{store.RoleViewer, store.RoleEditor, store.RoleAdmin, store.RoleOwner}
	for i, have := range roles {
		for j, need := range roles {
			if got, want := store.RoleAtLeast(have, need), i >= j; got != want {
				t.Errorf("RoleAtLeast(%s, %s) = %v, want %v", have, need, got, want)
			}
		}
	}
	if store.RoleAtLeast("superuser", store.RoleViewer) || store.RoleAtLeast(store.RoleOwner, "bogus") {
		t.Error("unknown roles must never pass")
	}
}

func TestCanManage(t *testing.T) {
	u := func(role string) store.User { return store.User{ID: 1, Role: role} }
	cases := []struct {
		actor, target, newRole string
		want                   bool
	}{
		{store.RoleViewer, store.RoleViewer, store.RoleEditor, false},
		{store.RoleEditor, store.RoleViewer, store.RoleEditor, false},
		{store.RoleAdmin, store.RoleViewer, store.RoleEditor, true},
		{store.RoleAdmin, store.RoleEditor, store.RoleAdmin, true},
		{store.RoleAdmin, store.RoleOwner, store.RoleAdmin, false}, // can't demote an owner
		{store.RoleAdmin, store.RoleAdmin, store.RoleOwner, false}, // can't grant owner
		{store.RoleAdmin, "", store.RoleOwner, false},              // can't invite an owner
		{store.RoleOwner, store.RoleOwner, store.RoleAdmin, true},
		{store.RoleOwner, "", store.RoleOwner, true},
	}
	for _, c := range cases {
		if got := canManage(u(c.actor), c.target, c.newRole); got != c.want {
			t.Errorf("canManage(actor=%s, target=%s, new=%s) = %v, want %v", c.actor, c.target, c.newRole, got, c.want)
		}
	}
}
