package pipeline

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/rkeys"
)

// Multi-location monitors: every location keeps its own live state (with
// its own retries), and the monitor's overall state is combined from the
// locations that reported recently.

// LocState is one location's latest state.
type LocState struct {
	Location string
	Status   string
	At       int64 // unix ms of the last result
	Latency  float64
	Message  string
	Code     string
	Cert     string
}

// Overall is the combined state of a multi-location monitor.
type Overall struct {
	Status  string // up | degraded | down
	Message string
	Latency float64
	Code    string
	Cert    string
}

// Combine applies the location rule: down when more than half of the
// recent locations are down, degraded when any is down or degraded, up
// otherwise. Locations without a result since `cutoff` are left out.
// ok is false when no location reported recently.
func Combine(states []LocState, cutoff int64) (Overall, bool) {
	var fresh []LocState
	for _, s := range states {
		if s.At >= cutoff && s.Status != "" && s.Status != "pending" {
			fresh = append(fresh, s)
		}
	}
	if len(fresh) == 0 {
		return Overall{}, false
	}
	slices.SortFunc(fresh, func(a, b LocState) int { return strings.Compare(a.Location, b.Location) })

	var down, degraded []string
	var o Overall
	var latSum float64
	var latN int
	for _, s := range fresh {
		switch s.Status {
		case "down":
			down = append(down, s.Location)
			if o.Message == "" {
				o.Message, o.Code = s.Message, s.Code
			}
		case "degraded":
			degraded = append(degraded, s.Location)
			latSum += s.Latency
			latN++
		default:
			latSum += s.Latency
			latN++
		}
		if o.Cert == "" {
			o.Cert = s.Cert
		}
	}
	if latN > 0 {
		o.Latency = latSum / float64(latN)
	}
	n := len(fresh)
	detail := o.Message
	switch {
	case len(down)*2 > n:
		o.Status = "down"
		o.Message = fmt.Sprintf("Down from %s (%d of %d)", strings.Join(down, ", "), len(down), n)
	case len(down) > 0:
		o.Status = "degraded"
		o.Message = fmt.Sprintf("Down from %s (%d of %d)", strings.Join(down, ", "), len(down), n)
	case len(degraded) > 0:
		o.Status = "degraded"
		o.Message = fmt.Sprintf("Slow from %s (%d of %d)", strings.Join(degraded, ", "), len(degraded), n)
	default:
		o.Status = "up"
		o.Message = fmt.Sprintf("Up from %d location%s", n, map[bool]string{true: "", false: "s"}[n == 1])
		detail = ""
	}
	if detail != "" {
		o.Message += ": " + detail
	}
	o.Message = truncate(o.Message, 300)
	return o, true
}

// setOverallScript writes the combined state and returns
// {prev status, new status, previous since}.
var setOverallScript = redis.NewScript(`
local prev = redis.call('HGET', KEYS[1], 'status') or 'pending'
local oldsince = redis.call('HGET', KEYS[1], 'since') or ''
redis.call('HSET', KEYS[1], 'status', ARGV[1], 'at', ARGV[2], 'lat', ARGV[3], 'msg', ARGV[4],
  'code', ARGV[5], 'cert', ARGV[6], 'agent', '', 'fails', 0)
if ARGV[1] ~= prev or redis.call('HEXISTS', KEYS[1], 'since') == 0 then
  redis.call('HSET', KEYS[1], 'since', ARGV[2])
end
return {'ok', prev, ARGV[1], oldsince, ARGV[4]}`)

// ReadLocations returns every location's state for a monitor.
func ReadLocations(ctx context.Context, rdb *redis.Client, monitorID int64) ([]LocState, error) {
	locs, err := rdb.SMembers(ctx, rkeys.MonLocations(monitorID)).Result()
	if err != nil || len(locs) == 0 {
		return nil, err
	}
	slices.Sort(locs)
	cmds := make([]*redis.SliceCmd, len(locs))
	if _, err := rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, l := range locs {
			cmds[i] = p.HMGet(ctx, rkeys.StatusLoc(monitorID, l), "status", "at", "lat", "msg", "code", "cert")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	out := make([]LocState, 0, len(locs))
	for i, c := range cmds {
		v := c.Val()
		if len(v) < 6 || v[0] == nil {
			continue // expired: that location stopped checking long ago
		}
		str := func(x any) string { s, _ := x.(string); return s }
		at, _ := strconv.ParseInt(str(v[1]), 10, 64)
		lat, _ := strconv.ParseFloat(str(v[2]), 64)
		out = append(out, LocState{Location: locs[i], Status: str(v[0]), At: at, Latency: lat, Message: str(v[3]), Code: str(v[4]), Cert: str(v[5])})
	}
	return out, nil
}

// staleAfter is how long a location counts without new results.
func staleAfter(interval time.Duration) time.Duration { return 3*interval + 30*time.Second }
