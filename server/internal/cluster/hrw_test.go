package cluster

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

func ids(n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(i + 1)
	}
	return out
}

func agentNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("4f1c2a9e-0000-4000-8000-%012d", i+1)
	}
	return out
}

func counts(parts map[string][]int64, agents []string) []int {
	c := make([]int, len(agents))
	for i, a := range agents {
		c[i] = len(parts[a])
	}
	return c
}

func TestAssignExactlyEven(t *testing.T) {
	cases := []struct{ monitors, agents int }{
		{8, 4}, {25, 5}, {10, 4}, {13, 3}, {3, 5}, {1, 3}, {0, 3}, {7, 1}, {1000, 7}, {5000, 20},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d_monitors_%d_agents", tc.monitors, tc.agents), func(t *testing.T) {
			agents := agentNames(tc.agents)
			parts := Assign(ids(tc.monitors), agents)

			lo, hi := tc.monitors/tc.agents, (tc.monitors+tc.agents-1)/tc.agents
			seen := map[int64]bool{}
			withExtra := 0
			for _, a := range agents {
				n := len(parts[a])
				if n < lo || n > hi {
					t.Fatalf("agent got %d monitors, want %d..%d (counts %v)", n, lo, hi, counts(parts, agents))
				}
				if n == hi && hi != lo {
					withExtra++
				}
				for _, id := range parts[a] {
					if seen[id] {
						t.Fatalf("monitor %d assigned twice", id)
					}
					seen[id] = true
				}
			}
			if len(seen) != tc.monitors {
				t.Fatalf("assigned %d monitors, want %d", len(seen), tc.monitors)
			}
			if hi != lo && withExtra != tc.monitors%tc.agents {
				t.Fatalf("%d agents got the extra monitor, want %d", withExtra, tc.monitors%tc.agents)
			}
		})
	}
}

func TestAssignExamplesFromSpec(t *testing.T) {
	for _, tc := range []struct{ m, n, each int }{{8, 4, 2}, {25, 5, 5}} {
		agents := agentNames(tc.n)
		for _, c := range counts(Assign(ids(tc.m), agents), agents) {
			if c != tc.each {
				t.Fatalf("%d monitors / %d workers: got a worker with %d, want %d each", tc.m, tc.n, c, tc.each)
			}
		}
	}
}

func TestAssignDeterministicAndOrderIndependent(t *testing.T) {
	agents := agentNames(5)
	mons := ids(500)
	want := Assign(mons, agents)

	r := rand.New(rand.NewPCG(1, 2))
	for range 20 {
		a := slices.Clone(agents)
		m := slices.Clone(mons)
		r.Shuffle(len(a), func(i, j int) { a[i], a[j] = a[j], a[i] })
		r.Shuffle(len(m), func(i, j int) { m[i], m[j] = m[j], m[i] })
		got := Assign(m, a)
		for _, ag := range agents {
			if !slices.Equal(got[ag], want[ag]) {
				t.Fatal("assignment depends on input order")
			}
		}
	}
}

func moved(before, after map[int64]string) int {
	n := 0
	for id, a := range before {
		if after[id] != a {
			n++
		}
	}
	return n
}

// When a worker dies, its monitors must move; everything else should mostly
// stay put so checks aren't needlessly reshuffled.
func TestAssignStableWhenAgentLeaves(t *testing.T) {
	agents := agentNames(5)
	mons := ids(1000)
	before := Owners(mons, agents)
	after := Owners(mons, agents[:4]) // agent 5 died

	for id, a := range after {
		if a == agents[4] {
			t.Fatalf("monitor %d still on the dead agent", id)
		}
	}
	mustMove := 0
	for _, a := range before {
		if a == agents[4] {
			mustMove++
		}
	}
	total := moved(before, after)
	extra := total - mustMove
	t.Logf("1000 monitors, 5→4 workers: %d had to move (dead worker), %d extra moved to re-level", mustMove, extra)
	if extra > len(mons)/10 {
		t.Fatalf("too much churn: %d monitors moved besides the dead worker's %d", extra, mustMove)
	}
}

func TestAssignStableWhenAgentJoins(t *testing.T) {
	agents := agentNames(5)
	mons := ids(1000)
	before := Owners(mons, agents[:4])
	after := Owners(mons, agents)
	total := moved(before, after)
	t.Logf("1000 monitors, 4→5 workers: %d moved (ideal minimum 200)", total)
	if total > 200+len(mons)/10 {
		t.Fatalf("too much churn when a worker joins: %d moved", total)
	}
}

func TestAssignStableWhenMonitorAdded(t *testing.T) {
	agents := agentNames(5)
	before := Owners(ids(1000), agents)
	after := Owners(ids(1001), agents)
	n := moved(before, after)
	t.Logf("adding 1 monitor to 1000 moved %d existing monitors", n)
	if n > 50 {
		t.Fatalf("adding one monitor moved %d others", n)
	}
}

func TestAssignNoAgents(t *testing.T) {
	if got := Assign(ids(5), nil); len(got) != 0 {
		t.Fatalf("expected empty assignment, got %v", got)
	}
}

func BenchmarkAssign10kMonitors20Agents(b *testing.B) {
	mons, agents := ids(10_000), agentNames(20)
	slices.Sort(agents)
	for b.Loop() {
		assign(mons, agents) // uncached
	}
}

func BenchmarkAssignCached(b *testing.B) {
	mons, agents := ids(10_000), agentNames(20)
	Assign(mons, agents)
	for b.Loop() {
		Assign(mons, agents)
	}
}
