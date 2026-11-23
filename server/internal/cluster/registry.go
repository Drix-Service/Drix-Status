package cluster

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/rkeys"
)

// AgentTTL is how long an agent stays "live" without a heartbeat.
const AgentTTL = 15 * time.Second

// LiveAgent is the shared view of a connected agent.
type LiveAgent struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Region      string    `json:"region"`
	Server      string    `json:"server"`
	Hostname    string    `json:"hostname"`
	Version     string    `json:"version"`
	OS          string    `json:"os"`
	Arch        string    `json:"arch"`
	RemoteAddr  string    `json:"remote_addr"`
	ConnectedAt time.Time `json:"connected_at"`
	LastSeen    time.Time `json:"last_seen"`
	Running     int       `json:"running"`
	InFlight    int       `json:"in_flight"`
	Buffered    int       `json:"buffered"`
	ConnID      string    `json:"-"`
}

// Registry tracks live agents across all server instances.
type Registry struct{ rdb *redis.Client }

func NewRegistry(rdb *redis.Client) *Registry { return &Registry{rdb: rdb} }

// Touch records an agent as alive (connect + every heartbeat).
func (r *Registry) Touch(ctx context.Context, a LiveAgent) error {
	now := time.Now()
	_, err := r.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.ZAdd(ctx, rkeys.Agents, redis.Z{Score: float64(now.UnixMilli()), Member: a.ID})
		p.HSet(ctx, rkeys.Agent(a.ID), map[string]any{
			"id": a.ID, "name": a.Name, "region": a.Region, "server": a.Server, "hostname": a.Hostname,
			"version": a.Version, "os": a.OS, "arch": a.Arch, "remote_addr": a.RemoteAddr,
			"connected_at": a.ConnectedAt.UnixMilli(), "last_seen": now.UnixMilli(),
			"running": a.Running, "in_flight": a.InFlight, "buffered": a.Buffered, "conn": a.ConnID,
		})
		p.Expire(ctx, rkeys.Agent(a.ID), AgentTTL+5*time.Second)
		return nil
	})
	return err
}

// Remove drops an agent immediately (clean disconnect), but only if the
// stored connection is ours — a newer connection may have replaced it.
var removeScript = redis.NewScript(`
local conn = redis.call('HGET', KEYS[2], 'conn')
if conn == false or conn == ARGV[2] then
  redis.call('ZREM', KEYS[1], ARGV[1])
  redis.call('DEL', KEYS[2])
  return 1
end
return 0`)

func (r *Registry) Remove(ctx context.Context, agentID, connID string) (bool, error) {
	n, err := removeScript.Run(ctx, r.rdb, []string{rkeys.Agents, rkeys.Agent(agentID)}, agentID, connID).Int()
	return n == 1, err
}

// LiveIDs returns sorted IDs of agents seen within AgentTTL.
func (r *Registry) LiveIDs(ctx context.Context) ([]string, error) {
	min := strconv.FormatInt(time.Now().Add(-AgentTTL).UnixMilli(), 10)
	ids, err := r.rdb.ZRangeByScore(ctx, rkeys.Agents, &redis.ZRangeBy{Min: min, Max: "+inf"}).Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}

// List returns full details for live agents.
func (r *Registry) List(ctx context.Context) ([]LiveAgent, error) {
	ids, err := r.LiveIDs(ctx)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	cmds := make([]*redis.MapStringStringCmd, len(ids))
	_, err = r.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, id := range ids {
			cmds[i] = p.HGetAll(ctx, rkeys.Agent(id))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]LiveAgent, 0, len(ids))
	for _, c := range cmds {
		m := c.Val()
		if len(m) == 0 {
			continue
		}
		out = append(out, LiveAgent{
			ID: m["id"], Name: m["name"], Region: m["region"], Server: m["server"], Hostname: m["hostname"],
			Version: m["version"], OS: m["os"], Arch: m["arch"], RemoteAddr: m["remote_addr"],
			ConnectedAt: msTime(m["connected_at"]), LastSeen: msTime(m["last_seen"]),
			Running: atoi(m["running"]), InFlight: atoi(m["in_flight"]), Buffered: atoi(m["buffered"]),
			ConnID: m["conn"],
		})
	}
	return out, nil
}

// Get returns one live agent.
func (r *Registry) Get(ctx context.Context, id string) (LiveAgent, bool) {
	m, err := r.rdb.HGetAll(ctx, rkeys.Agent(id)).Result()
	if err != nil || len(m) == 0 {
		return LiveAgent{}, false
	}
	return LiveAgent{ID: m["id"], Server: m["server"], ConnID: m["conn"]}, true
}

// Prune removes long-dead entries from the ZSET (leader only).
func (r *Registry) Prune(ctx context.Context) {
	max := strconv.FormatInt(time.Now().Add(-time.Minute).UnixMilli(), 10)
	r.rdb.ZRemRangeByScore(ctx, rkeys.Agents, "-inf", max)
}

func msTime(s string) time.Time {
	n, _ := strconv.ParseInt(s, 10, 64)
	if n == 0 {
		return time.Time{}
	}
	return time.UnixMilli(n)
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
