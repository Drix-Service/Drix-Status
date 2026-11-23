// Package notify delivers alerts (email, Discord, Slack, webhooks) and
// transactional email. Work goes through a Redis Stream consumer group, so
// with several server instances each notification is sent exactly once;
// failures are retried with backoff and every outcome is logged.
package notify

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/catalog"
	"drix/server/internal/pipeline"
	"drix/server/internal/secret"
	"drix/server/internal/snapshot"
	"drix/server/internal/store"
)

const (
	stream     = "drix:notify"
	retryZSet  = "drix:notify:retry"
	group      = "notifiers"
	maxAttempt = 5
)

// Job is one unit of delivery work.
type Job struct {
	Kind      string  `json:"kind"` // channel | email | push
	ChannelID int64   `json:"channel_id,omitempty"`
	SubID     int64   `json:"sub_id,omitempty"` // push subscription
	MonitorID int64   `json:"monitor_id,omitempty"`
	Msg       Message `json:"msg"`
	Email     *Email  `json:"email,omitempty"`
	Attempt   int     `json:"attempt"`
	ID        string  `json:"id"`
}

// Notifier is shared by the API, the pipeline and background jobs.
type Notifier struct {
	st        *store.Store
	rdb       *redis.Client
	cat       *catalog.Catalog
	box       *secret.Box
	log       *slog.Logger
	adminURL  string
	statusURL string
	envSMTP   *SMTPSettings
	consumer  string

	mu       sync.Mutex
	channels []store.Channel
	loadedAt time.Time

	vapid *VAPIDKeys // push signing keys, loaded on first use

	ackSecret []byte // signs acknowledge links (from SECRET_KEY)

	// Subscribers (milestone 6) plugs in here.
	OnIncident func(ctx context.Context, inc store.Incident, event string, update *store.IncidentUpdate)
}

type Options struct {
	SecretKey, AdminURL, StatusURL, SMTPURL, ServerID string
}

func New(st *store.Store, rdb *redis.Client, cat *catalog.Catalog, opt Options, log *slog.Logger) *Notifier {
	n := &Notifier{st: st, rdb: rdb, cat: cat, box: secret.New(opt.SecretKey), log: log.With("component", "notify"),
		adminURL: strings.TrimRight(opt.AdminURL, "/"), statusURL: strings.TrimRight(opt.StatusURL, "/"), consumer: opt.ServerID}
	n.OnIncident = n.notifySubscribers
	if opt.SecretKey != "" {
		sum := sha256.Sum256([]byte("drix-ack-v1:" + opt.SecretKey))
		n.ackSecret = sum[:]
	}
	if opt.SMTPURL != "" {
		if s, err := ParseSMTPURL(opt.SMTPURL); err == nil {
			n.envSMTP = &s
		} else {
			n.log.Warn("ignoring SMTP_URL", "err", err)
		}
	}
	return n
}

// Box exposes the secret box (nil without SECRET_KEY).
func (n *Notifier) Box() *secret.Box { return n.box }

// ---------- SMTP settings ----------

type storedSMTP struct {
	SMTPSettings
	PasswordEnc string `json:"password_enc,omitempty"`
}

// SMTP returns the effective settings: saved ones, else SMTP_URL.
func (n *Notifier) SMTP(ctx context.Context) (SMTPSettings, bool, error) {
	var s storedSMTP
	found, err := n.st.GetJSONSetting(ctx, "smtp", &s)
	if err != nil {
		return SMTPSettings{}, false, err
	}
	if found && s.Host != "" {
		if s.PasswordEnc != "" {
			if s.Password, err = n.box.Open(s.PasswordEnc); err != nil {
				return SMTPSettings{}, true, err
			}
		}
		return s.SMTPSettings, true, nil
	}
	if n.envSMTP != nil {
		return *n.envSMTP, false, nil
	}
	return SMTPSettings{}, false, nil
}

// SaveSMTP stores settings; an empty password keeps the existing one.
func (n *Notifier) SaveSMTP(ctx context.Context, s SMTPSettings, keepPassword bool) error {
	var cur storedSMTP
	_, _ = n.st.GetJSONSetting(ctx, "smtp", &cur)
	out := storedSMTP{SMTPSettings: s}
	out.Password = ""
	switch {
	case keepPassword:
		out.PasswordEnc = cur.PasswordEnc
	case s.Password != "":
		enc, err := n.box.Seal(s.Password)
		if err != nil {
			return err
		}
		out.PasswordEnc = enc
	}
	return n.st.SaveJSONSetting(ctx, "smtp", out)
}

// HasSMTPPassword reports whether a password is stored (it's never returned).
func (n *Notifier) HasSMTPPassword(ctx context.Context) bool {
	var cur storedSMTP
	_, _ = n.st.GetJSONSetting(ctx, "smtp", &cur)
	return cur.PasswordEnc != ""
}

// SendEmailNow sends synchronously (test buttons).
func (n *Notifier) SendEmailNow(ctx context.Context, e Email) error {
	s, _, err := n.SMTP(ctx)
	if err != nil {
		return err
	}
	return Send(ctx, s, e)
}

// ---------- channels ----------

// SealConfig encrypts a channel config.
func (n *Notifier) SealConfig(v any) (string, error) {
	b, _ := json.Marshal(v)
	return n.box.Seal(string(b))
}

// OpenConfig decrypts a channel config into v.
func (n *Notifier) OpenConfig(enc string, v any) error {
	plain, err := n.box.Open(enc)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(plain), v)
}

// InvalidateChannels forces a reload on the next event.
func (n *Notifier) InvalidateChannels() {
	n.mu.Lock()
	n.channels = nil
	n.mu.Unlock()
}

func (n *Notifier) loadChannels(ctx context.Context) []store.Channel {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.channels != nil && time.Since(n.loadedAt) < 10*time.Second {
		return n.channels
	}
	chs, err := n.st.ListChannels(ctx)
	if err != nil {
		n.log.Error("load channels", "err", err)
		return n.channels
	}
	n.channels, n.loadedAt = chs, time.Now()
	return chs
}

func (n *Notifier) channelsFor(ctx context.Context, monitorID int64, event string) []store.Channel {
	var out []store.Channel
	for _, c := range n.loadChannels(ctx) {
		if !c.Enabled || !slices.Contains(c.Events, event) {
			continue
		}
		if monitorID != 0 && !c.ApplyToAll && !slices.Contains(c.MonitorIDs, monitorID) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// Deliver sends one message to one channel right now.
func (n *Notifier) Deliver(ctx context.Context, c store.Channel, m Message, deliveryID string) error {
	switch c.Type {
	case "email":
		var cfg EmailConfig
		if err := n.OpenConfig(c.ConfigEnc, &cfg); err != nil {
			return err
		}
		to := cleanList(cfg.To)
		if len(to) == 0 {
			return errors.New("no recipients")
		}
		e := alertEmail(m)
		e.To = to
		return n.SendEmailNow(ctx, e)
	case "telegram":
		var cfg TelegramConfig
		if err := n.OpenConfig(c.ConfigEnc, &cfg); err != nil {
			return err
		}
		return sendTelegram(ctx, cfg, m)
	case "teams":
		var cfg TeamsConfig
		if err := n.OpenConfig(c.ConfigEnc, &cfg); err != nil {
			return err
		}
		return sendTeams(ctx, cfg, m)
	case "pagerduty":
		var cfg PagerDutyConfig
		if err := n.OpenConfig(c.ConfigEnc, &cfg); err != nil {
			return err
		}
		return sendPagerDuty(ctx, cfg, m)
	case "opsgenie":
		var cfg OpsgenieConfig
		if err := n.OpenConfig(c.ConfigEnc, &cfg); err != nil {
			return err
		}
		return sendOpsgenie(ctx, cfg, m)
	case "ntfy":
		var cfg NtfyConfig
		if err := n.OpenConfig(c.ConfigEnc, &cfg); err != nil {
			return err
		}
		return sendNtfy(ctx, cfg, m)
	case "pushover":
		var cfg PushoverConfig
		if err := n.OpenConfig(c.ConfigEnc, &cfg); err != nil {
			return err
		}
		return sendPushover(ctx, cfg, m)
	case "discord", "slack", "webhook":
		var cfg WebhookConfig
		if err := n.OpenConfig(c.ConfigEnc, &cfg); err != nil {
			return err
		}
		switch c.Type {
		case "discord":
			return sendDiscord(ctx, cfg, m)
		case "slack":
			return sendSlack(ctx, cfg, m)
		default:
			return sendWebhook(ctx, cfg, m, deliveryID)
		}
	}
	return fmt.Errorf("unknown channel type %q", c.Type)
}

// ---------- events in ----------

func (n *Notifier) monitorRef(id int64) (*MonRef, bool) {
	m, ok := n.cat.Get().ByID[id]
	if !ok {
		return nil, false
	}
	return &MonRef{ID: m.ID, Name: m.Name, Type: string(m.Type), Target: m.Target}, true
}

// MonitorChanges is the pipeline hook for status transitions.
func (n *Notifier) MonitorChanges(ctx context.Context, changes []pipeline.Change) {
	data := n.cat.Get()
	for _, ch := range changes {
		if data.InMaintenance[ch.MonitorID] {
			continue // alerts pause during maintenance
		}
		ref, ok := n.monitorRef(ch.MonitorID)
		if !ok {
			continue
		}
		var event, title, label string
		status := ch.To
		switch {
		case ch.To == "down":
			event, title, label = "down", ref.Name+" is down", "Monitor down"
		case ch.From == "down" && (ch.To == "up" || ch.To == "degraded"):
			event, title, label = "recovered", ref.Name+" recovered", "Recovered"
		case ch.To == "degraded" && ch.From == "up":
			event, title, label = "degraded", ref.Name+" is degraded", "Degraded"
		case ch.From == "degraded" && ch.To == "up":
			event, title, label = "degraded", ref.Name+" is back to normal", "Back to normal"
		default:
			continue
		}
		m := Message{Event: event, Title: title, Label: label, Subject: ref.Name, Body: ch.Message, Status: status, At: ch.At, Monitor: ref,
			URL: fmt.Sprintf("%s/monitors/%d", n.adminURL, ref.ID), StatusURL: n.statusURL, Fields: monitorFields(ref)}
		if event == "recovered" {
			if d := ch.At.Sub(ch.Since); ch.Since.Before(ch.At) && d > 0 {
				m.Fields = append(m.Fields, Field{Name: "Down for", Value: human(d)})
			}
		}
		// The outage starts now; acknowledge links point at it.
		since := ch.At.UnixMilli()
		var escalated []string
		if event == "recovered" {
			escalated, _ = n.rdb.SMembers(ctx, escalatedKey(ref.ID)).Result()
		}
		for _, c := range n.channelsFor(ctx, ref.ID, event) {
			key := remindKey(c.ID, ref.ID)
			if c.EscalateAfterMin > 0 {
				switch event {
				case "down":
					n.scheduleEscalation(ctx, c, ref.ID, since)
					continue
				case "recovered":
					n.rdb.Del(ctx, key)
					if !slices.Contains(escalated, strconv.FormatInt(c.ID, 10)) {
						continue // it never got the down alert
					}
				}
			}
			cm := m
			if event == "down" {
				cm.AckURL = n.ackURL(ref.ID, since, c.ID)
			}
			n.enqueue(ctx, Job{Kind: "channel", ChannelID: c.ID, MonitorID: ref.ID, Msg: cm})
			if event == "down" && c.RemindEveryMin > 0 {
				n.rdb.Set(ctx, key, "1", time.Duration(c.RemindEveryMin)*time.Minute)
			}
			if event == "recovered" {
				n.rdb.Del(ctx, key)
			}
		}
		if event == "recovered" || event == "down" {
			n.rdb.Del(ctx, ackKey(ref.ID), escalatedKey(ref.ID))
		}
		pm := m
		if event == "down" {
			pm.AckURL = n.ackURL(ref.ID, since, 0)
		}
		n.pushAll(ctx, event, pm)
	}
}

func monitorFields(r *MonRef) []Field {
	f := []Field{{Name: "Check", Value: strings.ToUpper(r.Type)}}
	if r.Target != "" {
		f = append(f, Field{Name: "Target", Value: r.Target})
	}
	return f
}

var incidentStatusLabel = map[string]string{
	"investigating": "Investigating", "identified": "Identified", "monitoring": "Monitoring", "resolved": "Resolved",
	"scheduled": "Scheduled", "in_progress": "In progress", "completed": "Completed", "cancelled": "Cancelled",
}

// IncidentEvent is the hook for incident/maintenance activity.
func (n *Notifier) IncidentEvent(ctx context.Context, inc store.Incident, event string, u *store.IncidentUpdate) {
	kind := "Incident"
	status := map[string]string{"none": "maintenance", "minor": "degraded", "major": "down", "critical": "down"}[inc.Impact]
	if inc.Kind == store.KindMaintenance {
		kind, status = "Maintenance", "maintenance"
	}
	if store.IsClosedStatus(inc.Status) {
		status = "up"
	}
	m := Message{Event: "incident", Title: fmt.Sprintf("%s: %s (%s)", kind, inc.Title, incidentStatusLabel[inc.Status]),
		Label: kind + " · " + incidentStatusLabel[inc.Status], Subject: inc.Title, Status: status, At: time.Now().UTC(), URL: fmt.Sprintf("%s/incidents/%d", n.adminURL, inc.ID),
		StatusURL: fmt.Sprintf("%s/incidents/%d", n.statusURL, inc.ID)}
	if u != nil {
		m.Body = u.Body
	}
	if inc.ScheduledStart != nil && inc.ScheduledEnd != nil {
		m.Fields = append(m.Fields, Field{Name: "Window", Value: inc.ScheduledStart.Format("2 Jan 15:04 MST") + " – " + inc.ScheduledEnd.Format("2 Jan 15:04 MST")})
	}
	for _, c := range n.channelsFor(ctx, 0, "incident") {
		n.enqueue(ctx, Job{Kind: "channel", ChannelID: c.ID, Msg: m})
	}
	n.pushAll(ctx, "incident", m)
	if n.OnIncident != nil {
		n.OnIncident(ctx, inc, event, u)
	}
}

// SendInvite implements api.Mailer.
func (n *Notifier) SendInvite(ctx context.Context, to string, from store.User, role, link string) error {
	s, _, err := n.SMTP(ctx)
	if err != nil || !s.Configured() {
		return errors.New("email not configured")
	}
	who := from.Name
	if who == "" {
		who = from.Email
	}
	title := who + " invited you to Drix Status"
	d := inviteDoc(title, role, link)
	n.enqueue(ctx, Job{Kind: "email", Email: &Email{To: []string{to}, Subject: title,
		Text: fmt.Sprintf("%s\n\nJoin as %s: %s\n(The link works for 7 days.)\n", title, role, link),
		HTML: renderEmail(d)}, Msg: Message{Event: "invite", Title: title}})
	return nil
}

// EnqueueEmail queues a transactional email (subscribers etc.).
func (n *Notifier) EnqueueEmail(ctx context.Context, e Email, event string) {
	n.enqueue(ctx, Job{Kind: "email", Email: &e, Msg: Message{Event: event, Title: e.Subject}})
}

// ---------- queue ----------

func (n *Notifier) enqueue(ctx context.Context, j Job) {
	if j.ID == "" {
		j.ID = randHex(8)
	}
	b, _ := json.Marshal(j)
	if err := n.rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, MaxLen: 100_000, Approx: true, Values: map[string]any{"j": b}}).Err(); err != nil {
		n.log.Error("enqueue notification", "err", err)
	}
}

// Run consumes the queue until ctx ends (every server instance runs it).
func (n *Notifier) Run(ctx context.Context) {
	if err := n.rdb.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		n.log.Error("create consumer group", "err", err)
	}
	go n.retryMover(ctx)
	claim := time.NewTicker(30 * time.Second)
	defer claim.Stop()
	for ctx.Err() == nil {
		select {
		case <-claim.C:
			n.reclaim(ctx)
		default:
		}
		res, err := n.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: n.consumer, Streams: []string{stream, ">"}, Count: 20, Block: 5 * time.Second}).Result()
		if err != nil {
			if !errors.Is(err, redis.Nil) && ctx.Err() == nil {
				n.log.Warn("read notifications", "err", err)
				time.Sleep(time.Second)
			}
			continue
		}
		for _, s := range res {
			n.process(ctx, s.Messages)
		}
	}
}

// reclaim takes over messages another (crashed) instance never acknowledged.
func (n *Notifier) reclaim(ctx context.Context) {
	msgs, _, err := n.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: stream, Group: group, Consumer: n.consumer, MinIdle: time.Minute, Start: "0", Count: 50}).Result()
	if err == nil && len(msgs) > 0 {
		n.process(ctx, msgs)
	}
}

func (n *Notifier) process(ctx context.Context, msgs []redis.XMessage) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, msg := range msgs {
		raw, _ := msg.Values["j"].(string)
		var j Job
		if json.Unmarshal([]byte(raw), &j) != nil {
			n.rdb.XAck(ctx, stream, group, msg.ID)
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(id string, j Job) {
			defer wg.Done()
			defer func() { <-sem }()
			n.attempt(ctx, j)
			n.rdb.XAck(ctx, stream, group, id)
			n.rdb.XDel(ctx, stream, id)
		}(msg.ID, j)
	}
	wg.Wait()
}

func (n *Notifier) attempt(ctx context.Context, j Job) {
	j.Attempt++
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var err error
	var ch store.Channel
	switch j.Kind {
	case "channel":
		ch, err = n.st.GetChannel(sctx, j.ChannelID)
		if errors.Is(err, store.ErrNotFound) {
			return // channel deleted meanwhile
		}
		if err == nil {
			err = n.Deliver(sctx, ch, j.Msg, j.ID)
		}
	case "email":
		if j.Email != nil {
			err = n.SendEmailNow(sctx, *j.Email)
		}
	case "push":
		err = n.deliverPush(sctx, j)
	}
	if err != nil && j.Attempt < maxAttempt {
		// Retry with backoff: 10s, 40s, 90s, 160s.
		b, _ := json.Marshal(j)
		due := time.Now().Add(time.Duration(j.Attempt*j.Attempt) * 10 * time.Second)
		n.rdb.ZAdd(ctx, retryZSet, redis.Z{Score: float64(due.UnixMilli()), Member: b})
		n.log.Warn("notification failed, will retry", "kind", j.Kind, "attempt", j.Attempt, "err", err)
		return
	}
	if j.Kind == "channel" {
		d := store.Delivery{ChannelID: &j.ChannelID, Event: j.Msg.Event, Title: j.Msg.Title, Status: "sent", Attempts: j.Attempt}
		if j.MonitorID != 0 {
			d.MonitorID = &j.MonitorID
		}
		if err != nil {
			d.Status, d.Error = "failed", truncate(err.Error(), 500)
		}
		_ = n.st.LogDelivery(context.WithoutCancel(ctx), d)
	}
	if err != nil {
		n.log.Error("notification gave up", "kind", j.Kind, "attempts", j.Attempt, "err", err)
	}
}

// retryMover requeues due retries (atomically claimed with ZREM).
func (n *Notifier) retryMover(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		due, err := n.rdb.ZRangeByScore(ctx, retryZSet, &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(time.Now().UnixMilli(), 10), Count: 100}).Result()
		if err != nil {
			continue
		}
		for _, raw := range due {
			if removed, _ := n.rdb.ZRem(ctx, retryZSet, raw).Result(); removed == 1 {
				n.rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"j": raw}})
			}
		}
	}
}

// ---------- reminders ----------

func remindKey(channelID, monitorID int64) string {
	return fmt.Sprintf("drix:remind:%d:%d", channelID, monitorID)
}

// Reminders re-sends "still down" for channels with a reminder interval (leader only).
func (n *Notifier) Reminders(ctx context.Context, isLeader func() bool) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !isLeader() {
			continue
		}
		data := n.cat.Get()
		var ids []int64
		for _, m := range data.Monitors {
			if m.Enabled && !data.InMaintenance[m.ID] {
				ids = append(ids, m.ID)
			}
		}
		live, _, err := snapshot.ReadStatuses(ctx, n.rdb, ids, false)
		if err != nil {
			continue
		}
		for id, ls := range live {
			if ls.Status != "down" || ls.Since == nil {
				continue
			}
			since := ls.Since.UnixMilli()
			if n.acked(ctx, id, since) {
				continue // someone is on it
			}
			ref, ok := n.monitorRef(id)
			if !ok {
				continue
			}
			escalated, _ := n.rdb.SMembers(ctx, escalatedKey(id)).Result()
			for _, c := range n.channelsFor(ctx, id, "down") {
				if c.RemindEveryMin == 0 {
					continue
				}
				if c.EscalateAfterMin > 0 && !slices.Contains(escalated, strconv.FormatInt(c.ID, 10)) {
					continue // not escalated to this channel (yet)
				}
				ok, _ := n.rdb.SetNX(ctx, remindKey(c.ID, id), "1", time.Duration(c.RemindEveryMin)*time.Minute).Result()
				if !ok {
					continue
				}
				m := Message{Event: "reminder", Title: ref.Name + " is still down", Label: "Still down", Subject: ref.Name, Body: ls.Message, Status: "down",
					At: time.Now().UTC(), Monitor: ref, URL: fmt.Sprintf("%s/monitors/%d", n.adminURL, id), StatusURL: n.statusURL, Fields: monitorFields(ref),
					AckURL: n.ackURL(id, since, c.ID)}
				if ls.Since != nil {
					m.Fields = append(m.Fields, Field{Name: "Down for", Value: human(time.Since(*ls.Since))})
				}
				n.enqueue(ctx, Job{Kind: "channel", ChannelID: c.ID, MonitorID: id, Msg: m})
			}
		}
	}
}

func human(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func inviteDoc(title, role, link string) emailDoc {
	roleName := role
	if roleName != "" {
		roleName = strings.ToUpper(role[:1]) + role[1:]
	}
	return emailDoc{
		Preheader:    "Join the team as " + roleName + ". The invite works for 7 days.",
		Brand:        emailBrand{Name: "Drix Status"},
		Title:        title,
		Paragraphs:   []string{"You've been invited to help run the status page and monitoring. Accept to set up your account."},
		Rows:         [][2]string{{"Role", roleName}},
		Button:       &emailButton{Label: "Accept invite", URL: link},
		LinkFallback: true,
		Note:         "This invite expires in 7 days. If you weren't expecting it, you can ignore this email.",
	}
}

// BackupFailed tells the team a scheduled database backup failed: the
// channels that get "down" alerts and phones that get them.
func (n *Notifier) BackupFailed(ctx context.Context, err error) {
	m := Message{Event: "backup_failed", Title: "Database backup failed", Label: "Backup failed", Subject: "Drix database backup",
		Body: err.Error(), Status: "down", At: time.Now().UTC(), URL: n.adminURL + "/settings#backups",
		Fields: []Field{{Name: "Next try", Value: "In about an hour"}}}
	for _, c := range n.channelsFor(ctx, 0, "down") {
		n.enqueue(ctx, Job{Kind: "channel", ChannelID: c.ID, Msg: m})
	}
	n.pushAll(ctx, "down", m)
}
