package cluster

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/rkeys"
)

const leaderTTL = 10 * time.Second

var renewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0`)

// Leader elects a single server instance to run cluster-wide jobs.
type Leader struct {
	rdb *redis.Client
	id  string
	log *slog.Logger
	is  atomic.Bool
}

func NewLeader(rdb *redis.Client, serverID string, log *slog.Logger) *Leader {
	return &Leader{rdb: rdb, id: serverID, log: log}
}

// IsLeader reports current leadership.
func (l *Leader) IsLeader() bool { return l.is.Load() }

// Run campaigns for and renews leadership until ctx ends.
func (l *Leader) Run(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		l.tick(ctx)
		select {
		case <-ctx.Done():
			if l.is.Load() {
				// Hand over immediately on shutdown.
				l.rdb.Eval(context.Background(), `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0`,
					[]string{rkeys.Leader}, l.id)
			}
			return
		case <-t.C:
		}
	}
}

func (l *Leader) tick(ctx context.Context) {
	if l.is.Load() {
		n, err := renewScript.Run(ctx, l.rdb, []string{rkeys.Leader}, l.id, leaderTTL.Milliseconds()).Int()
		if err != nil || n == 0 {
			l.is.Store(false)
			l.log.Warn("lost leadership", "err", err)
		}
		return
	}
	ok, err := l.rdb.SetNX(ctx, rkeys.Leader, l.id, leaderTTL).Result()
	if err == nil && ok {
		l.is.Store(true)
		l.log.Info("became cluster leader")
	}
}
