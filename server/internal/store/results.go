package store

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------- Results / stats ----------

type ResultRow struct {
	MonitorID int64     `json:"monitor_id"`
	CheckedAt time.Time `json:"checked_at"`
	Status    string    `json:"status"`
	LatencyMS float32   `json:"latency_ms"`
	Message   string    `json:"message"`
	AgentID   *string   `json:"agent_id"`
	Location  *string   `json:"location,omitempty"` // multi-location monitors only
}

// InsertResults bulk-loads rows with COPY.
func (s *Store) InsertResults(ctx context.Context, rows []ResultRow) error {
	if len(rows) == 0 {
		return nil
	}
	_, err := s.DB.CopyFrom(ctx, pgx.Identifier{"check_results"},
		[]string{"monitor_id", "checked_at", "status", "latency_ms", "message", "agent_id", "location"},
		pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
			r := rows[i]
			return []any{r.MonitorID, r.CheckedAt, r.Status, r.LatencyMS, r.Message, r.AgentID, r.Location}, nil
		}))
	return err
}

type DailyDelta struct {
	MonitorID int64
	Day       time.Time
	Total     int
	Up        int
	Degraded  int
	Down      int
	// Maintenance counts failed checks during maintenance; they are not part
	// of Total, so they never lower uptime.
	Maintenance int
	LatencySum  float64
}

// AddDailyStats increments per-day counters for many monitors in one statement.
func (s *Store) AddDailyStats(ctx context.Context, deltas []DailyDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	n := len(deltas)
	ids, days := make([]int64, n), make([]time.Time, n)
	tot, up, deg, down, mnt := make([]int32, n), make([]int32, n), make([]int32, n), make([]int32, n), make([]int32, n)
	lat := make([]float64, n)
	for i, d := range deltas {
		ids[i], days[i] = d.MonitorID, d.Day
		tot[i], up[i], deg[i], down[i] = int32(d.Total), int32(d.Up), int32(d.Degraded), int32(d.Down)
		mnt[i] = int32(d.Maintenance)
		lat[i] = d.LatencySum
	}
	_, err := s.DB.Exec(ctx, `
		INSERT INTO daily_stats (monitor_id, day, total, up, degraded, down, maintenance, latency_sum)
		SELECT * FROM unnest($1::bigint[], $2::date[], $3::int[], $4::int[], $5::int[], $6::int[], $7::int[], $8::float8[])
		ON CONFLICT (monitor_id, day) DO UPDATE SET
			total = daily_stats.total + EXCLUDED.total,
			up = daily_stats.up + EXCLUDED.up,
			degraded = daily_stats.degraded + EXCLUDED.degraded,
			down = daily_stats.down + EXCLUDED.down,
			maintenance = daily_stats.maintenance + EXCLUDED.maintenance,
			latency_sum = daily_stats.latency_sum + EXCLUDED.latency_sum`,
		ids, days, tot, up, deg, down, mnt, lat)
	return err
}

type DayStat struct {
	MonitorID   int64
	Day         time.Time
	Total       int
	Up          int
	Degraded    int
	Down        int
	Maintenance int
	LatencySum  float64
}

// DailyStatsSince loads per-day counters for all monitors since a date.
func (s *Store) DailyStatsSince(ctx context.Context, since time.Time) ([]DayStat, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT monitor_id, day, total, up, degraded, down, maintenance, latency_sum
		FROM daily_stats WHERE day >= $1 ORDER BY monitor_id, day`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayStat
	for rows.Next() {
		var d DayStat
		if err := rows.Scan(&d.MonitorID, &d.Day, &d.Total, &d.Up, &d.Degraded, &d.Down, &d.Maintenance, &d.LatencySum); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type LatencyPoint struct {
	T     time.Time `json:"t"`
	Avg   float64   `json:"avg"`
	Max   float64   `json:"max"`
	Min   float64   `json:"min"`
	Fails int       `json:"fails"`
	Total int       `json:"total"`
}

// LatencySeries buckets results for charts.
func (s *Store) LatencySeries(ctx context.Context, monitorID int64, since time.Time, bucket time.Duration) ([]LatencyPoint, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT date_bin($3::interval, checked_at, TIMESTAMPTZ '2000-01-01') AS b,
		       COALESCE(AVG(latency_ms) FILTER (WHERE status <> 'down'), 0),
		       COALESCE(MAX(latency_ms) FILTER (WHERE status <> 'down'), 0),
		       COALESCE(MIN(latency_ms) FILTER (WHERE status <> 'down'), 0),
		       COUNT(*) FILTER (WHERE status = 'down'),
		       COUNT(*)
		FROM check_results
		WHERE monitor_id = $1 AND checked_at >= $2
		GROUP BY b ORDER BY b`, monitorID, since, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LatencyPoint{}
	for rows.Next() {
		var p LatencyPoint
		if err := rows.Scan(&p.T, &p.Avg, &p.Max, &p.Min, &p.Fails, &p.Total); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecentResults returns the newest raw results for a monitor.
func (s *Store) RecentResults(ctx context.Context, monitorID int64, limit int) ([]ResultRow, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT monitor_id, checked_at, status, latency_ms, message, agent_id::text
		FROM check_results WHERE monitor_id=$1 ORDER BY checked_at DESC LIMIT $2`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ResultRow{}
	for rows.Next() {
		var r ResultRow
		if err := rows.Scan(&r.MonitorID, &r.CheckedAt, &r.Status, &r.LatencyMS, &r.Message, &r.AgentID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneResults deletes raw results older than the cutoff, in chunks.
func (s *Store) PruneResults(ctx context.Context, before time.Time) (int64, error) {
	var total int64
	for {
		tag, err := s.DB.Exec(ctx, `
			DELETE FROM check_results WHERE ctid IN (
				SELECT ctid FROM check_results WHERE checked_at < $1 LIMIT 10000)`, before)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < 10000 {
			return total, nil
		}
	}
}

// LocationStat is one location's last-24h summary for a monitor.
type LocationStat struct {
	Location string  `json:"location"`
	AvgMS    float64 `json:"avg_ms"`
	Uptime   float64 `json:"uptime"` // percent; -1 without checks
	Checks   int     `json:"checks"`
}

// LocationStats summarises a monitor's results per location since `since`.
func (s *Store) LocationStats(ctx context.Context, monitorID int64, since time.Time) ([]LocationStat, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT location,
		       COALESCE(AVG(latency_ms) FILTER (WHERE status IN ('up','degraded')), 0),
		       COUNT(*) FILTER (WHERE status IN ('up','degraded')),
		       COUNT(*) FILTER (WHERE status <> 'maintenance')
		FROM check_results
		WHERE monitor_id=$1 AND checked_at >= $2 AND location IS NOT NULL
		GROUP BY location ORDER BY location`, monitorID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LocationStat{}
	for rows.Next() {
		var l LocationStat
		var good, total int
		if err := rows.Scan(&l.Location, &l.AvgMS, &good, &total); err != nil {
			return nil, err
		}
		l.Checks, l.Uptime = total, -1
		if total > 0 {
			l.Uptime = math.Floor(float64(good)*10000/float64(total)) / 100
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
