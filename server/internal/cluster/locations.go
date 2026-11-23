package cluster

import (
	"slices"
	"strconv"
	"strings"
)

// Location is where an agent checks from: its region, or its name when
// no region is set.
func Location(region, name string) string {
	if r := strings.TrimSpace(region); r != "" {
		return r
	}
	return strings.TrimSpace(name)
}

// AssignLocations picks, for every multi-location monitor, one agent in
// each location: the highest rendezvous score among that location's
// agents. Every server instance gets the same answer from the same input,
// and when an agent leaves only its monitors move to another agent in the
// same location. The result maps agent ID → monitors.
func AssignLocations(monitorIDs []int64, byLocation map[string][]string) map[string][]int64 {
	out := map[string][]int64{}
	var buf []byte
	locs := make([]string, 0, len(byLocation))
	for l := range byLocation {
		locs = append(locs, l)
	}
	slices.Sort(locs)
	for _, id := range monitorIDs {
		key := []byte(strconv.FormatInt(id, 10))
		for _, l := range locs {
			agents := byLocation[l]
			best, bestScore := "", uint64(0)
			for _, a := range agents {
				if s := score(a, key, buf); best == "" || s > bestScore || (s == bestScore && a < best) {
					best, bestScore = a, s
				}
			}
			if best != "" {
				out[best] = append(out[best], id)
			}
		}
	}
	return out
}
