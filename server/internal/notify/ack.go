package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/snapshot"
	"drix/server/internal/store"
)

// Acknowledging a down monitor says "someone is on it": reminders and
// escalations stop until it recovers. An acknowledgement belongs to one
// outage, identified by when the monitor went down (its "since").

const (
	escalateZ = "drix:escalate" // ZSET "{channel}:{monitor}:{sinceMs}" -> due (unix ms)
	ackTTL    = 7 * 24 * time.Hour
)

func ackKey(monitorID int64) string       { return fmt.Sprintf("drix:ack:%d", monitorID) }
func escalatedKey(monitorID int64) string { return fmt.Sprintf("drix:escalated:%d", monitorID) }

// Ack is who acknowledged an outage. By is a person's name; Via says how
// when nobody was signed in (e.g. the alert channel's name).
type Ack struct {
	By    string    `json:"by,omitempty"`
	Via   string    `json:"via,omitempty"`
	At    time.Time `json:"at"`
	Since int64     `json:"-"`
}

// Who is a short "Lucas" or "via Discord ops".
func (a Ack) Who() string {
	if a.By != "" {
		return a.By
	}
	if a.Via != "" {
		return "via " + a.Via
	}
	return "someone"
}

var (
	ErrNotDown    = errors.New("this monitor isn't down right now")
	ErrOutageOver = errors.New("that outage is over")
)

// chatTypes get a short "acknowledged" message so the team sees it.
var chatTypes = []string{"discord", "slack", "teams", "telegram"}

// ---------- links ----------

func (n *Notifier) ackMAC(payload string) []byte {
	m := hmac.New(sha256.New, n.ackSecret)
	m.Write([]byte("ack:" + payload))
	return m.Sum(nil)[:12]
}

// AckToken signs (monitor, outage, channel); channel 0 is a phone notification.
func (n *Notifier) AckToken(monitorID, sinceMs, channelID int64) string {
	p := fmt.Sprintf("%d.%d.%d", monitorID, sinceMs, channelID)
	return p + "." + base64.RawURLEncoding.EncodeToString(n.ackMAC(p))
}

// ParseAckToken checks a token's signature.
func (n *Notifier) ParseAckToken(tok string) (monitorID, sinceMs, channelID int64, ok bool) {
	parts := strings.Split(tok, ".")
	if len(n.ackSecret) == 0 || len(parts) != 4 {
		return 0, 0, 0, false
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || !hmac.Equal(mac, n.ackMAC(strings.Join(parts[:3], "."))) {
		return 0, 0, 0, false
	}
	var nums [3]int64
	for i := range nums {
		if nums[i], err = strconv.ParseInt(parts[i], 10, 64); err != nil {
			return 0, 0, 0, false
		}
	}
	return nums[0], nums[1], nums[2], true
}

// ackURL is the link in an alert; empty without SECRET_KEY or ADMIN_URL.
func (n *Notifier) ackURL(monitorID, sinceMs, channelID int64) string {
	if len(n.ackSecret) == 0 || n.adminURL == "" || sinceMs == 0 {
		return ""
	}
	return n.adminURL + "/ack/" + n.AckToken(monitorID, sinceMs, channelID)
}

// ---------- state ----------

// Acks returns the current outage's acknowledgement for down monitors.
func (n *Notifier) Acks(ctx context.Context, live map[int64]snapshot.LiveStatus) map[int64]Ack {
	out := map[int64]Ack{}
	var ids []int64
	for id, ls := range live {
		if ls.Status == "down" && ls.Since != nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out
	}
	cmds := make([]*redis.MapStringStringCmd, len(ids))
	_, _ = n.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, id := range ids {
			cmds[i] = p.HGetAll(ctx, ackKey(id))
		}
		return nil
	})
	for i, id := range ids {
		h, err := cmds[i].Result()
		if err != nil || len(h) == 0 {
			continue
		}
		a := parseAck(h)
		if a.Since == live[id].Since.UnixMilli() {
			out[id] = a
		}
	}
	return out
}

func parseAck(h map[string]string) Ack {
	a := Ack{By: h["by"], Via: h["via"]}
	a.Since, _ = strconv.ParseInt(h["since"], 10, 64)
	if ms, err := strconv.ParseInt(h["at"], 10, 64); err == nil {
		a.At = time.UnixMilli(ms).UTC()
	}
	return a
}

// acked reports whether the outage that began at sinceMs is acknowledged.
func (n *Notifier) acked(ctx context.Context, monitorID, sinceMs int64) bool {
	s, err := n.rdb.HGet(ctx, ackKey(monitorID), "since").Int64()
	return err == nil && s == sinceMs
}

// Acknowledge marks the monitor's current outage as handled. sinceMs, when
// not 0, must match the current outage (links from an alert). It returns the
// existing acknowledgement if there is one.
func (n *Notifier) Acknowledge(ctx context.Context, monitorID, sinceMs int64, by, via string) (Ack, bool, error) {
	live, _, err := snapshot.ReadStatuses(ctx, n.rdb, []int64{monitorID}, false)
	if err != nil {
		return Ack{}, false, err
	}
	ls := live[monitorID]
	if ls.Status != "down" || ls.Since == nil {
		if sinceMs != 0 {
			return Ack{}, false, ErrOutageOver
		}
		return Ack{}, false, ErrNotDown
	}
	cur := ls.Since.UnixMilli()
	if sinceMs != 0 && sinceMs != cur {
		return Ack{}, false, ErrOutageOver
	}
	if h, err := n.rdb.HGetAll(ctx, ackKey(monitorID)).Result(); err == nil && len(h) > 0 {
		if a := parseAck(h); a.Since == cur {
			return a, false, nil
		}
	}
	a := Ack{By: by, Via: via, At: time.Now().UTC(), Since: cur}
	key := ackKey(monitorID)
	if _, err := n.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, key)
		p.HSet(ctx, key, "since", cur, "by", by, "via", via, "at", a.At.UnixMilli())
		p.Expire(ctx, key, ackTTL)
		return nil
	}); err != nil {
		return Ack{}, false, err
	}

	// Tell the chat channels that got the down alert.
	if ref, ok := n.monitorRef(monitorID); ok {
		m := Message{Event: "acknowledged", Title: ref.Name + " acknowledged by " + a.Who(), Label: "Acknowledged",
			Subject: ref.Name, Body: "Acknowledged by " + a.Who(), Status: "degraded", At: a.At, Monitor: ref,
			URL: fmt.Sprintf("%s/monitors/%d", n.adminURL, ref.ID)}
		escalated, _ := n.rdb.SMembers(ctx, escalatedKey(monitorID)).Result()
		for _, c := range n.channelsFor(ctx, monitorID, "down") {
			if !slices.Contains(chatTypes, c.Type) {
				continue
			}
			if c.EscalateAfterMin > 0 && !slices.Contains(escalated, strconv.FormatInt(c.ID, 10)) {
				continue // never heard about it
			}
			n.enqueue(ctx, Job{Kind: "channel", ChannelID: c.ID, MonitorID: monitorID, Msg: m})
		}
	}
	return a, true, nil
}

// ClearOutage forgets acknowledgements and escalations, e.g. when a monitor
// is paused or edited and starts over.
func (n *Notifier) ClearOutage(ctx context.Context, ids ...int64) {
	keys := make([]string, 0, len(ids)*2)
	for _, id := range ids {
		keys = append(keys, ackKey(id), escalatedKey(id))
	}
	if len(keys) > 0 {
		n.rdb.Del(ctx, keys...)
	}
}

// ---------- escalation ----------

func escalateMember(channelID, monitorID, sinceMs int64) string {
	return fmt.Sprintf("%d:%d:%d", channelID, monitorID, sinceMs)
}

// scheduleEscalation queues a delayed channel's down alert.
func (n *Notifier) scheduleEscalation(ctx context.Context, c store.Channel, monitorID, sinceMs int64) {
	due := time.Now().Add(time.Duration(c.EscalateAfterMin) * time.Minute).UnixMilli()
	n.rdb.ZAdd(ctx, escalateZ, redis.Z{Score: float64(due), Member: escalateMember(c.ID, monitorID, sinceMs)})
}

// Escalations sends delayed down alerts when nobody acknowledged in time
// (leader only).
func (n *Notifier) Escalations(ctx context.Context, isLeader func() bool) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if isLeader() {
			n.escalateDue(ctx, time.Now())
		}
	}
}

func (n *Notifier) escalateDue(ctx context.Context, now time.Time) {
	due, err := n.rdb.ZRangeByScore(ctx, escalateZ, &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(now.UnixMilli(), 10), Count: 500}).Result()
	if err != nil || len(due) == 0 {
		return
	}
	data := n.cat.Get()
	for _, member := range due {
		// Whoever removes it sends it: no duplicates across servers.
		if removed, err := n.rdb.ZRem(ctx, escalateZ, member).Result(); err != nil || removed == 0 {
			continue
		}
		var chID, monID, since int64
		if _, err := fmt.Sscanf(member, "%d:%d:%d", &chID, &monID, &since); err != nil {
			continue
		}
		if data.InMaintenance[monID] || n.acked(ctx, monID, since) {
			continue
		}
		live, _, err := snapshot.ReadStatuses(ctx, n.rdb, []int64{monID}, false)
		if err != nil {
			continue
		}
		ls := live[monID]
		if ls.Status != "down" || ls.Since == nil || ls.Since.UnixMilli() != since {
			continue // recovered, or a different outage now
		}
		var ch *store.Channel
		for _, c := range n.channelsFor(ctx, monID, "down") {
			if c.ID == chID {
				ch = &c
				break
			}
		}
		ref, ok := n.monitorRef(monID)
		if ch == nil || !ok {
			continue
		}
		after := human(now.Sub(time.UnixMilli(since)))
		m := Message{Event: "down", Title: ref.Name + " is down (escalated)", Label: "Escalated", Subject: ref.Name,
			Body: ls.Message, Status: "down", At: now.UTC(), Monitor: ref,
			URL: fmt.Sprintf("%s/monitors/%d", n.adminURL, monID), StatusURL: n.statusURL,
			AckURL: n.ackURL(monID, since, ch.ID),
			Fields: append(monitorFields(ref), Field{Name: "Escalated", Value: "Down for " + after + " and nobody has acknowledged it"})}
		n.enqueue(ctx, Job{Kind: "channel", ChannelID: ch.ID, MonitorID: monID, Msg: m})
		n.rdb.SAdd(ctx, escalatedKey(monID), ch.ID)
		n.rdb.Expire(ctx, escalatedKey(monID), ackTTL)
		if ch.RemindEveryMin > 0 {
			n.rdb.Set(ctx, remindKey(ch.ID, monID), "1", time.Duration(ch.RemindEveryMin)*time.Minute)
		}
	}
}
