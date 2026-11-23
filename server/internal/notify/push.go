package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"drix/server/internal/store"
)

// Push notifications to the Drix app. Every device that turned them on
// is a push subscription owned by a user, with its own list of events.
// Alerts are queued as "push" jobs on the same stream as channel alerts,
// so they get the same retries.

// PushEvents are the events a device can pick.
var PushEvents = []string{"down", "recovered", "degraded", "incident", "cert_expiring"}

const vapidSettingKey = "webpush"

type storedVAPID struct {
	PrivateEnc string `json:"private_enc"`
}

var vapidMu sync.Mutex

// VAPID returns this installation's push signing keys, creating them on
// first use. The private key is stored encrypted with SECRET_KEY.
func (n *Notifier) VAPID(ctx context.Context) (VAPIDKeys, error) {
	vapidMu.Lock()
	defer vapidMu.Unlock()
	if n.vapid != nil {
		return *n.vapid, nil
	}
	if n.box == nil {
		return VAPIDKeys{}, errors.New("set SECRET_KEY to use push notifications")
	}
	var s storedVAPID
	found, err := n.st.GetJSONSetting(ctx, vapidSettingKey, &s)
	if err != nil {
		return VAPIDKeys{}, err
	}
	if !found || s.PrivateEnc == "" {
		k, err := GenerateVAPID()
		if err != nil {
			return VAPIDKeys{}, err
		}
		enc, err := n.box.Seal(encodeVAPIDPrivate(k))
		if err != nil {
			return VAPIDKeys{}, err
		}
		raw, _ := json.Marshal(storedVAPID{PrivateEnc: enc})
		if _, err := n.st.CreateJSONSetting(ctx, vapidSettingKey, raw); err != nil {
			return VAPIDKeys{}, err
		}
		// Another server may have won the race: always use what's stored.
		if _, err := n.st.GetJSONSetting(ctx, vapidSettingKey, &s); err != nil {
			return VAPIDKeys{}, err
		}
	}
	plain, err := n.box.Open(s.PrivateEnc)
	if err != nil {
		return VAPIDKeys{}, fmt.Errorf("push keys: %w", err)
	}
	k, err := decodeVAPIDPrivate(plain)
	if err != nil {
		return VAPIDKeys{}, fmt.Errorf("push keys: %w", err)
	}
	keys := VAPIDKeys{Private: k, Subject: n.vapidSubject()}
	n.vapid = &keys
	return keys, nil
}

// vapidSubject is the contact push services see: the admin site.
func (n *Notifier) vapidSubject() string {
	if strings.HasPrefix(n.adminURL, "https://") {
		return n.adminURL
	}
	return "mailto:admin@drix.invalid" // local development
}

// appPath turns an admin link into the matching app screen,
// e.g. https://admin.x/monitors/4 → /m/monitors/4.
func (n *Notifier) appPath(adminURL string) string {
	p := strings.TrimPrefix(adminURL, n.adminURL)
	if strings.HasPrefix(p, "/monitors/") || strings.HasPrefix(p, "/incidents/") {
		return "/m" + p
	}
	return "/m"
}

// pushPayload is the notification for a message: a short title, the
// reason and target underneath, and where tapping it goes.
func (n *Notifier) pushPayload(m Message) PushPayload {
	title := strings.TrimPrefix(m.Title, "[Test] ")
	if strings.HasPrefix(m.Title, "[Test] ") {
		title = "Test: " + title
	}
	var parts []string
	if m.Event == "incident" {
		parts = append(parts, strings.TrimSuffix(m.Label, " · Test"))
		if b := firstLine(m.Body); b != "" {
			parts = append(parts, b)
		}
	} else {
		if b := firstLine(m.Body); b != "" {
			parts = append(parts, b)
		}
		if m.Monitor != nil && m.Monitor.Target != "" {
			parts = append(parts, strings.TrimPrefix(strings.TrimPrefix(m.Monitor.Target, "https://"), "http://"))
		}
	}
	p := PushPayload{Title: truncate(title, 120), Body: truncate(strings.Join(parts, " · "), 240), URL: n.appPath(m.URL), Ack: strings.TrimPrefix(m.AckURL, n.adminURL)}
	switch {
	case m.Monitor != nil && m.Monitor.ID != 0:
		p.Tag = fmt.Sprintf("monitor-%d", m.Monitor.ID)
	case strings.HasPrefix(p.URL, "/m/incidents/"):
		p.Tag = "incident-" + strings.TrimPrefix(p.URL, "/m/incidents/")
	}
	return p
}

// pushAll queues a message for every device that wants this event.
func (n *Notifier) pushAll(ctx context.Context, event string, m Message) {
	subs, err := n.st.PushSubsFor(ctx, event)
	if err != nil {
		n.log.Warn("push: list subscriptions", "err", err)
		return
	}
	for _, s := range subs {
		n.enqueue(ctx, Job{Kind: "push", SubID: s.ID, Msg: m})
	}
}

// deliverPush sends one queued push job.
func (n *Notifier) deliverPush(ctx context.Context, j Job) error {
	sub, err := n.st.GetPushSub(ctx, j.SubID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // device removed meanwhile
	}
	if err != nil {
		return err
	}
	return n.SendPushTo(ctx, sub, j.Msg)
}

// SendPushTo sends a message to one device right now. An expired
// subscription is removed and not reported as an error.
func (n *Notifier) SendPushTo(ctx context.Context, sub store.PushSub, m Message) error {
	keys, err := n.VAPID(ctx)
	if err != nil {
		return err
	}
	p := n.pushPayload(m)
	urgent := m.Status == "down" || m.Event == "down"
	err = SendPush(ctx, keys, PushSubscription{Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth}, p, urgent, p.Tag)
	if errors.Is(err, ErrSubscriptionGone) {
		_ = n.st.DeletePushSub(context.WithoutCancel(ctx), sub.ID, 0)
		n.log.Info("push: removed expired subscription", "id", sub.ID)
		return nil
	}
	if err == nil {
		_ = n.st.TouchPushSub(context.WithoutCancel(ctx), sub.ID)
	}
	return err
}
