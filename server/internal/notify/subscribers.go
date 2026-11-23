package notify

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"drix/server/internal/store"
)

// PageURL is the public address of a status page (custom domain preferred).
func (n *Notifier) PageURL(p *store.Page) string {
	if p.CustomDomain != nil && *p.CustomDomain != "" {
		return "https://" + *p.CustomDomain
	}
	if p.IsDefault {
		return n.statusURL
	}
	return n.statusURL + "/" + p.Slug
}

// pageHost is where the page's API is reachable (for confirm/unsubscribe links).
func (n *Notifier) pageHost(p *store.Page) string {
	if p.CustomDomain != nil && *p.CustomDomain != "" {
		return "https://" + *p.CustomDomain
	}
	return n.statusURL
}

func (n *Notifier) unsubscribeURL(p *store.Page, token string) string {
	return n.pageHost(p) + "/api/public/subscriptions/unsubscribe?token=" + token
}

// SendConfirmation emails the double opt-in link.
func (n *Notifier) SendConfirmation(ctx context.Context, p *store.Page, sub store.Subscriber) {
	link := n.pageHost(p) + "/api/public/subscriptions/confirm?token=" + sub.Token
	title := "Confirm your subscription to " + p.Name
	n.EnqueueEmail(ctx, Email{To: []string{sub.Email}, Subject: title,
		Text: fmt.Sprintf("%s\n\nConfirm here: %s\n\nIf you didn't ask for this, ignore this email.\n", title, link),
		HTML: renderEmail(confirmDoc(p, link))}, "subscribe.confirm")
}

func confirmDoc(p *store.Page, link string) emailDoc {
	return emailDoc{
		Preheader:    "Confirm to get incident and maintenance updates from " + p.Name + ".",
		Brand:        pageBrand(p),
		Title:        "Confirm your subscription",
		Paragraphs:   []string{"You asked to get an email from " + p.Name + " when there's an incident or scheduled maintenance. Confirm your address to start receiving updates."},
		Button:       &emailButton{Label: "Confirm subscription", URL: link},
		LinkFallback: true,
		Note:         "If you didn't ask for this, you can ignore this email and nothing will happen.",
		Footer:       "Sent by " + html.EscapeString(p.Name) + ".",
	}
}

// notifySubscribers emails confirmed subscribers about incident activity.
func (n *Notifier) notifySubscribers(ctx context.Context, inc store.Incident, event string, u *store.IncidentUpdate) {
	if !inc.NotifySubscribers || u == nil || len(inc.PageIDs) == 0 {
		return
	}
	subs, err := n.st.ConfirmedSubscribers(ctx, inc.PageIDs)
	if err != nil {
		n.log.Error("load subscribers", "err", err)
		return
	}
	data := n.cat.Get()
	kind := "Incident"
	accent := map[string]string{"none": "#71717a", "minor": "#d97706", "major": "#dc2626", "critical": "#dc2626"}[inc.Impact]
	if inc.Kind == store.KindMaintenance {
		kind, accent = "Maintenance", "#2563eb"
	}
	if store.IsClosedStatus(inc.Status) {
		accent = "#16a34a"
	}
	var affected []string
	for _, m := range inc.Monitors {
		if mon, ok := data.ByID[m.MonitorID]; ok {
			affected = append(affected, mon.Name)
		}
	}
	status := incidentStatusLabel[inc.Status]
	for _, sub := range subs {
		p := data.PageByID[sub.PageID]
		if p == nil {
			continue
		}
		link := fmt.Sprintf("%s/incidents/%d", strings.TrimRight(n.PageURL(p), "/"), inc.ID)
		unsub := n.unsubscribeURL(p, sub.Token)
		subject := fmt.Sprintf("[%s] %s: %s (%s)", p.Name, kind, inc.Title, status)

		var text strings.Builder
		fmt.Fprintf(&text, "%s: %s\nStatus: %s\n\n%s\n", kind, inc.Title, status, u.Body)
		if inc.ScheduledStart != nil && inc.ScheduledEnd != nil {
			fmt.Fprintf(&text, "\nWhen: %s – %s\n", inc.ScheduledStart.Format("Mon 2 Jan 15:04 MST"), inc.ScheduledEnd.Format("Mon 2 Jan 15:04 MST"))
		}
		if len(affected) > 0 {
			fmt.Fprintf(&text, "Affected: %s\n", strings.Join(affected, ", "))
		}
		fmt.Fprintf(&text, "\nDetails: %s\n\nUnsubscribe: %s\n", link, unsub)

		n.EnqueueEmail(ctx, Email{
			To: []string{sub.Email}, Subject: subject, Text: text.String(),
			HTML: renderEmail(updateDoc(p, inc, u.Body, kind, status, accent, affected, link, unsub)),
			Headers: map[string]string{
				"List-Unsubscribe":      "<" + unsub + ">",
				"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			},
		}, "subscriber."+event)
	}
	n.log.Info("subscribers notified", "incident", inc.ID, "event", event, "recipients", len(subs))
}

// pageBrand uses the status page's name, logo and accent colour.
func pageBrand(p *store.Page) emailBrand {
	return emailBrand{Name: p.Name, LogoURL: p.LogoURL}
}

// window formats a maintenance window: "Mon 24 Nov, 10:00 – 11:00 UTC", or
// with both dates when it spans more than one day.
func window(start, end time.Time) string {
	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		return start.Format("Mon 2 Jan, 15:04") + " – " + end.Format("15:04 MST")
	}
	return start.Format("Mon 2 Jan, 15:04") + " – " + end.Format("Mon 2 Jan, 15:04 MST")
}

// updateDoc is the incident/maintenance update sent to subscribers.
func updateDoc(p *store.Page, inc store.Incident, body, kind, status, accent string, affected []string, link, unsub string) emailDoc {
	label := status
	if kind == "Maintenance" {
		label = "Maintenance · " + status
	}
	d := emailDoc{
		Preheader:   firstLine(body),
		Brand:       pageBrand(p),
		Status:      label,
		StatusColor: accent,
		Title:       inc.Title,
		Paragraphs:  []string{body},
		Button:      &emailButton{Label: "View on status page", URL: link},
		Footer: fmt.Sprintf(`You're receiving this because you subscribed to updates from %s. <a href="%s" style="color:#a1a1aa;text-decoration:underline;">Unsubscribe</a>`,
			html.EscapeString(p.Name), html.EscapeString(unsub)),
	}
	if inc.ScheduledStart != nil && inc.ScheduledEnd != nil {
		d.Rows = append(d.Rows, [2]string{"When", window(*inc.ScheduledStart, *inc.ScheduledEnd)})
	}
	if len(affected) > 0 {
		d.Rows = append(d.Rows, [2]string{"Affected", strings.Join(affected, ", ")})
	}
	return d
}
