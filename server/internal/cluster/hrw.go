// Package cluster coordinates agents and server instances through Redis.
package cluster

import (
	"encoding/binary"
	"slices"
	"strconv"
	"sync"

	"github.com/cespare/xxhash/v2"
)

// score is the rendezvous weight of an agent for a monitor.
func score(agent string, monitorID []byte, buf []byte) uint64 {
	buf = append(buf[:0], agent...)
	buf = append(buf, ':')
	buf = append(buf, monitorID...)
	return xxhash.Sum64(buf)
}

// Assign splits monitors across agents as evenly as possible: with M monitors
// and N agents every agent gets ⌊M/N⌋ or ⌈M/N⌉ monitors, exactly M%N of them
// getting the extra one (8 monitors / 4 agents → 2 each; 25 / 5 → 5 each;
// 10 / 4 → 3,3,2,2).
//
// It is rendezvous hashing with capacities ("bounded loads"): monitors are
// visited in a fixed hash order and each goes to its highest-scoring agent
// that still has room. That keeps three properties the cluster relies on:
//   - deterministic: every server instance computes the same answer from the
//     same live agent list, with no coordination and regardless of input order;
//   - balanced: the per-agent counts never differ by more than one;
//   - stable: when an agent joins or leaves, most monitors stay where they are
//     (only the departed agent's monitors plus a few needed to re-level move).
//
// The result is cached for identical inputs (the gateway recomputes every
// couple of seconds) and shared between callers, so treat it as read-only.
func Assign(monitorIDs []int64, agents []string) map[string][]int64 {
	agents = slices.Clone(agents)
	slices.Sort(agents)
	agents = slices.Compact(agents)
	ids := slices.Clone(monitorIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)

	key := fingerprint(ids, agents)
	memo.Lock()
	if memo.ok && memo.key == key {
		out := memo.parts
		memo.Unlock()
		return out
	}
	memo.Unlock()

	out := assign(ids, agents)
	memo.Lock()
	memo.key, memo.parts, memo.ok = key, out, true
	memo.Unlock()
	return out
}

var memo struct {
	sync.Mutex
	ok    bool
	key   uint64
	parts map[string][]int64
}

func fingerprint(ids []int64, agents []string) uint64 {
	h := xxhash.New()
	var b [8]byte
	for _, id := range ids {
		binary.LittleEndian.PutUint64(b[:], uint64(id))
		_, _ = h.Write(b[:])
	}
	_, _ = h.Write([]byte{0xff})
	for _, a := range agents {
		_, _ = h.WriteString(a)
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// assign does the work for Assign on sorted, de-duplicated input.
func assign(ids []int64, agents []string) map[string][]int64 {
	n, m := len(agents), len(ids)
	out := make(map[string][]int64, n)
	if n == 0 || m == 0 {
		return out
	}

	// Visit monitors in a hash order (not ID order) so no group of monitors
	// systematically gets first pick of agents.
	type item struct {
		id  int64
		key []byte
		h   uint64
	}
	items := make([]item, m)
	for i, id := range ids {
		k := strconv.AppendInt(nil, id, 10)
		items[i] = item{id: id, key: k, h: xxhash.Sum64(k)}
	}
	slices.SortFunc(items, func(a, b item) int {
		if a.h != b.h {
			if a.h < b.h {
				return -1
			}
			return 1
		}
		return int(a.id - b.id)
	})

	base, extra := m/n, m%n
	load := make([]int, n)
	full := 0 // agents that already took one of the `extra` slots
	buf := make([]byte, 0, 64)

	for _, it := range items {
		// Highest-scoring agent that still has room. Total capacity is exactly
		// m, so some agent always has room.
		best, bestScore := -1, uint64(0)
		for i, a := range agents {
			if load[i] > base || (load[i] == base && full >= extra) {
				continue
			}
			if s := score(a, it.key, buf); best < 0 || s > bestScore {
				best, bestScore = i, s
			}
		}
		if load[best] == base {
			full++
		}
		load[best]++
		out[agents[best]] = append(out[agents[best]], it.id)
	}
	for _, a := range agents {
		slices.Sort(out[a])
	}
	return out
}

// Owners returns the owning agent for every monitor (inverse of Assign).
func Owners(monitorIDs []int64, agents []string) map[int64]string {
	parts := Assign(monitorIDs, agents)
	owners := make(map[int64]string, len(monitorIDs))
	for a, ids := range parts {
		for _, id := range ids {
			owners[id] = a
		}
	}
	return owners
}
