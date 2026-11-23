package protocol

import (
	"strconv"
	"time"

	"github.com/cespare/xxhash/v2"
)

// Phase is the deterministic offset within its interval at which a monitor
// is checked. Every agent uses the same phase for the same monitor, so when a
// monitor moves between agents the check cadence is unchanged.
func Phase(id int64, interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	return time.Duration(xxhash.Sum64String(strconv.FormatInt(id, 10)) % uint64(interval))
}

// NextRun returns the next scheduled tick for a monitor strictly after now.
func NextRun(id int64, interval time.Duration, now time.Time) time.Time {
	next := now.Truncate(interval).Add(Phase(id, interval))
	for !next.After(now) {
		next = next.Add(interval)
	}
	return next
}

// Slot maps a check start time to its scheduled tick number. It rounds to the
// nearest tick so clock drift up to ±interval/2 still lands in the same slot.
// The server uses it to drop duplicate results during reassignments.
func Slot(id int64, interval time.Duration, at time.Time) int64 {
	if interval <= 0 {
		return at.Unix()
	}
	t := at.UnixNano() - int64(Phase(id, interval)) + int64(interval)/2
	return t / int64(interval)
}
