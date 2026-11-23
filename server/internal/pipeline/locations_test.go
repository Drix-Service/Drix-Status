package pipeline

import (
	"strings"
	"testing"
)

func TestCombineLocations(t *testing.T) {
	now := int64(1_000_000)
	loc := func(name, status string, lat float64, msg string) LocState {
		return LocState{Location: name, Status: status, At: now, Latency: lat, Message: msg}
	}
	cases := []struct {
		name   string
		states []LocState
		status string
		msg    string
	}{
		{"all up", []LocState{loc("Frankfurt", "up", 10, ""), loc("Amsterdam", "up", 30, "")}, "up", "Up from 2 locations"},
		{"one of three down", []LocState{loc("Frankfurt", "down", 0, "HTTP 503"), loc("Amsterdam", "up", 30, ""), loc("New York", "up", 90, "")}, "degraded", "Down from Frankfurt (1 of 3): HTTP 503"},
		{"two of three down", []LocState{loc("Frankfurt", "down", 0, "timeout"), loc("Amsterdam", "down", 0, "timeout"), loc("New York", "up", 90, "")}, "down", "Down from Amsterdam, Frankfurt (2 of 3): timeout"},
		{"one of two down is not a majority", []LocState{loc("A", "down", 0, "x"), loc("B", "up", 5, "")}, "degraded", "Down from A (1 of 2): x"},
		{"slow somewhere", []LocState{loc("A", "degraded", 900, ""), loc("B", "up", 100, "")}, "degraded", "Slow from A (1 of 2)"},
		{"single location behaves like before", []LocState{loc("Only", "down", 0, "refused")}, "down", "Down from Only (1 of 1): refused"},
	}
	for _, c := range cases {
		o, ok := Combine(c.states, now-1)
		if !ok || o.Status != c.status || o.Message != c.msg {
			t.Errorf("%s: got %q %q", c.name, o.Status, o.Message)
		}
	}

	// Stale locations are ignored: the only fresh one is up.
	stale := loc("Gone", "down", 0, "x")
	stale.At = now - 10_000
	o, ok := Combine([]LocState{stale, loc("Here", "up", 20, "")}, now-1)
	if !ok || o.Status != "up" || o.Latency != 20 || !strings.HasPrefix(o.Message, "Up from 1 location") {
		t.Fatalf("%+v", o)
	}
	// Nothing fresh at all.
	if _, ok := Combine([]LocState{stale}, now-1); ok {
		t.Fatal("expected no result")
	}
	// Average latency over the locations that answered.
	o, _ = Combine([]LocState{loc("A", "up", 10, ""), loc("B", "up", 30, ""), loc("C", "down", 0, "x")}, now-1)
	if o.Latency != 20 {
		t.Fatal(o.Latency)
	}
}
