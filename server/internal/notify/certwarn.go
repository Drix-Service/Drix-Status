package notify

import (
	"context"
	"fmt"
	"math"
	"time"

	"drix/server/internal/snapshot"
)

// Certificate-expiry warnings. HTTP, SMTP and gRPC checks record when the
// target's TLS certificate expires; this loop warns the "cert_expiring"
// channels 14, 7, 3 and 1 day(s) before, and once it has expired. Each step
// is sent once per certificate, so a renewed certificate starts over.
// Monitors of type "ssl" are skipped: they already alert by going degraded
// and then down as the expiry gets close.

// certSteps are the warning points in days, largest first.
var certSteps = []int{14, 7, 3, 1}

// certStep returns the warning step for a certificate that expires in
// `days` whole days: the smallest step that is >= days, or -1 once it has
// expired. ok is false while it's further away than the first step.
func certStep(days int) (step int, ok bool) {
	if days < 0 {
		return -1, true
	}
	step, ok = 0, false
	for _, s := range certSteps {
		if days <= s {
			step, ok = s, true
		}
	}
	return step, ok
}

// daysLeft is the number of whole days until t (negative once past).
func daysLeft(t, now time.Time) int {
	return int(math.Floor(t.Sub(now).Hours() / 24))
}

func certWarnKey(monitorID int64, expiry time.Time, step int) string {
	return fmt.Sprintf("drix:certwarn:%d:%d:%d", monitorID, expiry.Unix(), step)
}

// certMessage builds the warning for one monitor.
func (n *Notifier) certMessage(ref *MonRef, expiry, now time.Time) Message {
	days := daysLeft(expiry, now)
	date := expiry.UTC().Format("Mon 2 Jan 2006, 15:04 UTC")
	m := Message{Event: "cert_expiring", Label: "Certificate expiring", Subject: ref.Name, Status: "degraded",
		At: now, Monitor: ref, URL: fmt.Sprintf("%s/monitors/%d", n.adminURL, ref.ID), StatusURL: n.statusURL,
		Fields: append(monitorFields(ref), Field{Name: "Expires", Value: date})}
	switch {
	case days < 0:
		m.Label, m.Status = "Certificate expired", "down"
		m.Title = ref.Name + ": certificate expired"
		m.Body = "The TLS certificate has expired. Visitors now get a security warning. Renew it as soon as possible."
	case days == 0:
		m.Status = "down"
		m.Title = ref.Name + ": certificate expires today"
		m.Body = "The TLS certificate expires today. Renew it now to avoid security warnings."
	default:
		if days <= 3 {
			m.Status = "down"
		}
		unit := "days"
		if days == 1 {
			unit = "day"
		}
		m.Title = fmt.Sprintf("%s: certificate expires in %d %s", ref.Name, days, unit)
		m.Body = fmt.Sprintf("The TLS certificate expires in %d %s. Renew it before then to avoid security warnings.", days, unit)
	}
	return m
}

// CertWarnings checks certificate expiry every hour (leader only).
func (n *Notifier) CertWarnings(ctx context.Context, isLeader func() bool) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	// First pass shortly after start, once live statuses are in.
	first := time.NewTimer(2 * time.Minute)
	defer first.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-t.C:
		}
		if isLeader() {
			n.checkCerts(ctx, time.Now().UTC())
		}
	}
}

func (n *Notifier) checkCerts(ctx context.Context, now time.Time) {
	data := n.cat.Get()
	var ids []int64
	for _, m := range data.Monitors {
		if m.Enabled && string(m.Type) != "ssl" && !data.InMaintenance[m.ID] {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	live, _, err := snapshot.ReadStatuses(ctx, n.rdb, ids, false)
	if err != nil {
		n.log.Warn("cert warnings: read statuses", "err", err)
		return
	}
	for id, ls := range live {
		if ls.CertExpiry == nil {
			continue
		}
		step, ok := certStep(daysLeft(*ls.CertExpiry, now))
		if !ok {
			continue
		}
		chans := n.channelsFor(ctx, id, "cert_expiring")
		subs, _ := n.st.PushSubsFor(ctx, "cert_expiring")
		if len(chans) == 0 && len(subs) == 0 {
			continue // nobody to tell yet; a channel added later still gets it
		}
		ref, ok := n.monitorRef(id)
		if !ok {
			continue
		}
		ttl := max(ls.CertExpiry.Sub(now), 0) + 72*time.Hour
		first, err := n.rdb.SetNX(ctx, certWarnKey(id, *ls.CertExpiry, step), "1", ttl).Result()
		if err != nil || !first {
			continue
		}
		m := n.certMessage(ref, *ls.CertExpiry, now)
		for _, c := range chans {
			n.enqueue(ctx, Job{Kind: "channel", ChannelID: c.ID, MonitorID: id, Msg: m})
		}
		for _, s := range subs {
			n.enqueue(ctx, Job{Kind: "push", SubID: s.ID, Msg: m})
		}
	}
}
