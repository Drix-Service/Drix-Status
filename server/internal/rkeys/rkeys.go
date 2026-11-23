// Package rkeys centralizes every Redis key and channel name.
package rkeys

import "strconv"

const (
	prefix = "drix:"

	// Pub/sub channels.
	ChanMonitorsChanged = prefix + "ch:monitors" // catalog must reload
	ChanSettingsChanged = prefix + "ch:settings"
	ChanCluster         = prefix + "ch:cluster" // agent joined/left
	ChanEvents          = prefix + "ch:events"  // live status updates for SSE

	Agents = prefix + "agents" // ZSET agentID -> last seen (unix ms)
	Leader = prefix + "leader"
	// SnapshotDirty is a SET of page IDs whose public snapshot must be rebuilt;
	// the member AllPages means "rebuild every page".
	SnapshotDirty = prefix + "snap:dirty"
	AllPages      = "*"
)

// Public snapshot keys, one set per status page.
func SnapshotJSON(pageID int64) string { return prefix + "snap:" + id(pageID) + ":json" }
func SnapshotGzip(pageID int64) string { return prefix + "snap:" + id(pageID) + ":gz" }
func SnapshotETag(pageID int64) string { return prefix + "snap:" + id(pageID) + ":etag" }

func id(n int64) string { return strconv.FormatInt(n, 10) }

// Status is the live state hash for a monitor.
func Status(monitorID int64) string { return prefix + "st:" + id(monitorID) }

// Latency is a short list of recent latencies for sparklines.
func Latency(monitorID int64) string { return prefix + "lat:" + id(monitorID) }

// Slot dedupes results per scheduled tick.
func Slot(monitorID, slot int64) string { return prefix + "slot:" + id(monitorID) + ":" + id(slot) }

// StatusLoc is a multi-location monitor's live state in one location.
func StatusLoc(monitorID int64, loc string) string {
	return prefix + "stl:" + id(monitorID) + ":" + loc
}

// SlotLoc dedupes one result per schedule slot per location.
func SlotLoc(monitorID, slot int64, loc string) string {
	return prefix + "slot:" + id(monitorID) + ":" + id(slot) + ":" + loc
}

// MonLocations is the set of locations that have checked a monitor.
func MonLocations(monitorID int64) string { return prefix + "locs:" + id(monitorID) }

// PushLast stores when a heartbeat monitor was last pinged (unix ms).
func PushLast(monitorID int64) string { return prefix + "push:" + id(monitorID) }

// Agent holds live metadata for a connected agent.
func Agent(agentID string) string { return prefix + "agent:" + agentID }

// ServerChan is the command channel for one server instance.
func ServerChan(serverID string) string { return prefix + "ch:srv:" + serverID }

// TestReply is where a test check result is published.
func TestReply(reqID string) string { return prefix + "ch:test:" + reqID }

// Session maps a hashed session token to an admin.
func Session(hash string) string { return prefix + "sess:" + hash }

// UserSessions is the set of session hashes belonging to a user.
func UserSessions(userID int64) string { return prefix + "usess:" + id(userID) }

// LoginAttempts throttles logins per IP.
func LoginAttempts(ip string) string { return prefix + "login:" + ip }

// LatencyCache caches chart queries.
func LatencyCache(monitorID int64, rng string) string {
	return prefix + "latc:" + id(monitorID) + ":" + rng
}

// TwoFactorTicket is a pending sign-in waiting for its second factor.
func TwoFactorTicket(hash string) string { return prefix + "2fa:ticket:" + hash }

// TOTPSetup holds an authenticator secret until the user confirms a code.
func TOTPSetup(userID int64) string { return prefix + "2fa:totp-setup:" + id(userID) }

// TOTPUsed marks a code's time step as used, so a code works once.
func TOTPUsed(userID, step int64) string {
	return prefix + "2fa:totp-used:" + id(userID) + ":" + id(step)
}

// PasskeyRegistration holds a passkey registration challenge.
func PasskeyRegistration(userID int64) string { return prefix + "2fa:pk-reg:" + id(userID) }

// PasskeyLogin holds a passwordless passkey sign-in challenge.
func PasskeyLogin(hash string) string { return prefix + "2fa:pk-login:" + hash }
