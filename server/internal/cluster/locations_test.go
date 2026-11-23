package cluster

import (
	"slices"
	"testing"
)

func TestAssignLocations(t *testing.T) {
	byLoc := map[string][]string{"Frankfurt": {"a1", "a2"}, "Amsterdam": {"b1"}, "New York": {"c1", "c2", "c3"}}
	ids := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	got := AssignLocations(ids, byLoc)

	// Every monitor is checked exactly once per location.
	for _, id := range ids {
		for loc, agents := range byLoc {
			n := 0
			for _, a := range agents {
				if slices.Contains(got[a], id) {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("monitor %d checked %d times in %s", id, n, loc)
			}
		}
	}
	// Deterministic.
	again := AssignLocations(ids, map[string][]string{"New York": {"c3", "c1", "c2"}, "Amsterdam": {"b1"}, "Frankfurt": {"a2", "a1"}})
	for a := range got {
		if !slices.Equal(got[a], again[a]) {
			t.Fatalf("agent %s: %v vs %v", a, got[a], again[a])
		}
	}
	// Removing an agent only moves its own monitors.
	smaller := AssignLocations(ids, map[string][]string{"Frankfurt": {"a1", "a2"}, "Amsterdam": {"b1"}, "New York": {"c1", "c2"}})
	for _, a := range []string{"a1", "a2", "b1", "c1", "c2"} {
		for _, id := range got[a] {
			if !slices.Contains(smaller[a], id) {
				t.Fatalf("monitor %d moved away from %s", id, a)
			}
		}
	}
}

func TestLocationName(t *testing.T) {
	if Location(" Frankfurt ", "w1") != "Frankfurt" || Location("", "worker-2") != "worker-2" {
		t.Fatal("Location")
	}
}
