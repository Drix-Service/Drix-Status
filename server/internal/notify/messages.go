package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Message is a channel-independent notification.
type Message struct {
	Event     string    `json:"event"`             // down | recovered | degraded | reminder | incident | test
	Title     string    `json:"title"`             // one line, e.g. "API is down"
	Label     string    `json:"label,omitempty"`   // short status line, e.g. "Monitor down", "Incident · Investigating"
	Subject   string    `json:"subject,omitempty"` // what it's about: the monitor or incident name
	Body      string    `json:"body"`              // details
	Status    string    `json:"status"`            // up | down | degraded | maintenance (drives colour)
	URL       string    `json:"url"`
	StatusURL string    `json:"status_url,omitempty"` // public status page (or the incident's page there)
	AckURL    string    `json:"ack_url,omitempty"`    // "acknowledge" link on down alerts
	Monitor   *MonRef   `json:"monitor,omitempty"`
	Fields    []Field   `json:"fields,omitempty"`
	At        time.Time `json:"at"`
}

type MonRef struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Target string `json:"target"`
}

type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Channel configs.
type EmailConfig struct {
	To []string `json:"to"`
}
type WebhookConfig struct {
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"` // webhook only: HMAC signing key
}

var colors = map[string]int{"up": 0x16a34a, "down": 0xdc2626, "degraded": 0xd97706, "maintenance": 0x2563eb}

func colorHex(status string) string {
	c, ok := colors[status]
	if !ok {
		c = 0x71717a
	}
	return fmt.Sprintf("#%06x", c)
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func post(ctx context.Context, url string, body []byte, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DrixStatus/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// Discord: a Components V2 card. The container's accent bar shows the
// status colour; inside: the name as a heading, the status in plain words
// with a one-line summary, details in rows below a divider, and buttons
// to the admin panel and the public status page.
// https://discord.com/developers/docs/components/reference
const (
	discordComponentsV2 = 1 << 15
	dActionRow          = 1
	dButton             = 2
	dTextDisplay        = 10
	dSeparator          = 14
	dContainer          = 17
	dButtonLink         = 5
	discordTextBudget   = 3800 // Discord allows 4000 characters across all text components
	discordGrey         = 0x71717a
)

func sendDiscord(ctx context.Context, cfg WebhookConfig, m Message) error {
	body, _ := json.Marshal(discordPayload(m))
	return post(ctx, discordURL(cfg.URL), body, nil)
}

// discordURL adds with_components=true, which webhooks need before they
// accept components, keeping any query the user set (e.g. thread_id).
func discordURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Set("with_components", "true")
	u.RawQuery = q.Encode()
	return u.String()
}

func discordPayload(m Message) map[string]any {
	accent := discordGrey
	if c, ok := colors[m.Status]; ok && m.Event != "test" {
		accent = c
	}
	field := func(name string) string {
		for _, f := range m.Fields {
			if f.Name == name {
				return f.Value
			}
		}
		return ""
	}
	label := strings.TrimSuffix(m.Label, " · Test") // e.g. "Monitor down", "Incident · Investigating"
	kind, state, _ := strings.Cut(label, " · ")     // incidents: "Incident", "Investigating"

	// Heading: the monitor or incident name.
	heading := m.Subject
	if heading == "" || m.Event == "test" {
		heading = strings.TrimPrefix(m.Title, "[Test] ")
	}

	// The status in plain words with a one-line summary, e.g.
	// "Down · HTTP 503 Service Unavailable" or "Recovered after 18m".
	status, summary := "", discordEscape(truncate(firstLine(m.Body), 300))
	switch m.Event {
	case "down":
		status = "Down"
	case "reminder":
		status = "Still down"
	case "recovered":
		status, summary = "Recovered", ""
		if d := field("Down for"); d != "" {
			status += " after " + d
		}
	case "degraded":
		status = "Degraded"
		if m.Status == "up" {
			status = "Operational"
		}
	case "incident":
		status, summary = discordEscape(state), "" // the update text goes below
	case "test":
		summary = ""
	case "cert_expiring":
		status = discordEscape(strings.TrimSuffix(m.Label, " · Test"))
	case "acknowledged":
		status, summary = discordEscape(m.Body), ""
	default:
		if m.Status != "" {
			status = strings.ToUpper(m.Status[:1]) + m.Status[1:]
		}
	}
	top := "### " + discordEscape(heading)
	if line := strings.Trim(status+" · "+summary, " ·"); line != "" {
		top += "\n" + line
	}
	if m.Event == "incident" || m.Event == "test" {
		// Updates are written by your team: keep their text (and markdown) whole.
		if b := strings.TrimSpace(m.Body); b != "" {
			top += "\n" + truncate(b, 2500)
		}
	}

	// Details, one row each.
	var rows []string
	row := func(name, value string) {
		if value != "" {
			rows = append(rows, "**"+name+"**  "+value)
		}
	}
	if m.Event == "incident" {
		row("Type", discordEscape(kind))
	}
	for _, f := range m.Fields {
		v := f.Value
		switch f.Name {
		case "":
			continue
		case "Target":
			v = strings.TrimPrefix(strings.TrimPrefix(v, "https://"), "http://")
		case "Down for":
			if m.Event == "recovered" {
				continue // already in the summary
			}
		}
		row(discordEscape(f.Name), discordEscape(v))
	}
	if !m.At.IsZero() {
		when := map[string]string{"down": "Started", "recovered": "Recovered", "incident": "Updated", "test": "Sent"}[m.Event]
		if when == "" {
			when = "Time"
		}
		row(when, fmt.Sprintf("<t:%d:f>", m.At.Unix()))
	}

	parts := []any{map[string]any{"type": dTextDisplay, "content": top}}
	if len(rows) > 0 {
		parts = append(parts,
			map[string]any{"type": dSeparator, "divider": true, "spacing": 1},
			map[string]any{"type": dTextDisplay, "content": strings.Join(rows, "\n")})
	}

	var buttons []any
	link := func(label, u string) {
		if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
			buttons = append(buttons, map[string]any{"type": dButton, "style": dButtonLink, "label": label, "url": u})
		}
	}
	link("Acknowledge", m.AckURL)
	link("Open in Drix", m.URL)
	link("Status page", m.StatusURL)
	if len(buttons) > 0 {
		parts = append(parts,
			map[string]any{"type": dSeparator, "divider": false, "spacing": 1},
			map[string]any{"type": dActionRow, "components": buttons})
	}

	return map[string]any{
		"username":         "Drix Status",
		"flags":            discordComponentsV2,
		"components":       []any{map[string]any{"type": dContainer, "accent_color": accent, "components": parts}},
		"allowed_mentions": map[string]any{"parse": []string{}},
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// discordEscape stops names and labels from being read as markdown.
var discordMD = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`", "|", `\|`, ">", `\>`, "#", `\#`, "[", `\[`, "]", `\]`)

func discordEscape(s string) string { return discordMD.Replace(s) }

// Slack: Block Kit with an attachment colour.
func sendSlack(ctx context.Context, cfg WebhookConfig, m Message) error {
	text := "*" + m.Title + "*"
	if m.URL != "" {
		text = fmt.Sprintf("*<%s|%s>*", m.URL, m.Title)
	}
	blocks := []map[string]any{{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": text + "\n" + truncate(m.Body, 2500)}}}
	if len(m.Fields) > 0 {
		var fs []map[string]any
		for _, f := range m.Fields {
			fs = append(fs, map[string]any{"type": "mrkdwn", "text": "*" + f.Name + "*\n" + f.Value})
		}
		blocks = append(blocks, map[string]any{"type": "section", "fields": fs})
	}
	var actions []map[string]any
	for _, l := range [][2]string{{"Acknowledge", m.AckURL}, {"Open in Drix", m.URL}} {
		if strings.HasPrefix(l[1], "https://") || strings.HasPrefix(l[1], "http://") {
			actions = append(actions, map[string]any{"type": "button", "text": map[string]any{"type": "plain_text", "text": l[0]}, "url": l[1]})
		}
	}
	if m.AckURL != "" && len(actions) > 0 {
		blocks = append(blocks, map[string]any{"type": "actions", "elements": actions})
	}
	body, _ := json.Marshal(map[string]any{
		"text":        m.Title, // notification fallback
		"attachments": []map[string]any{{"color": colorHex(m.Status), "blocks": blocks}},
	})
	return post(ctx, cfg.URL, body, nil)
}

// Webhook: the Message as JSON, signed with HMAC-SHA256 when a secret is set.
func sendWebhook(ctx context.Context, cfg WebhookConfig, m Message, deliveryID string) error {
	body, _ := json.Marshal(m)
	headers := map[string]string{"X-Drix-Event": m.Event, "X-Drix-Delivery": deliveryID}
	if cfg.Secret != "" {
		headers["X-Drix-Signature"] = "sha256=" + Sign(cfg.Secret, body)
	}
	return post(ctx, cfg.URL, body, headers)
}

// Sign returns hex(HMAC-SHA256(secret, body)), the X-Drix-Signature value.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// alertEmail renders a monitor/incident message as an email.
func alertEmail(m Message) Email {
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n\n%s\n", m.Title, m.Body)
	for _, f := range m.Fields {
		fmt.Fprintf(&text, "%s: %s\n", f.Name, f.Value)
	}
	if m.URL != "" {
		fmt.Fprintf(&text, "\n%s\n", m.URL)
	}
	text.WriteString("\n-- \nSent by Drix Status\n")

	title := strings.TrimPrefix(m.Title, "[Test] ")
	if m.Event == "incident" && m.Subject != "" {
		title = m.Subject
	}
	d := emailDoc{
		Brand:  emailBrand{Name: "Drix Status"},
		Status: alertStatus(m), StatusColor: colorHex(m.Status),
		Title:  title,
		Footer: "You're receiving this because an alert channel in Drix Status sends to this address.",
	}
	if m.Event == "test" {
		d.StatusColor = "#a1a1aa"
	}
	// Check errors in a monospace box; incident updates as text.
	// Recoveries skip it: "HTTP 200 OK" adds nothing once it's back up.
	switch {
	case m.Event == "incident" || m.Event == "test" || m.Event == "cert_expiring":
		d.Paragraphs = []string{m.Body}
	case m.Status != "up":
		d.Code = m.Body
	}
	for _, f := range m.Fields {
		d.Rows = append(d.Rows, [2]string{f.Name, f.Value})
	}
	if !m.At.IsZero() {
		d.Rows = append(d.Rows, [2]string{"When", m.At.UTC().Format("Mon 2 Jan 2006, 15:04 MST")})
	}
	if m.URL != "" {
		d.Button = &emailButton{Label: "Open in Drix", URL: m.URL}
	}
	if m.AckURL != "" {
		d.Secondary = &emailButton{Label: "Acknowledge", URL: m.AckURL}
	}
	pre := []string{firstLine(m.Body)}
	for _, f := range m.Fields {
		if f.Name == "Target" || f.Name == "Down for" || f.Name == "Window" {
			pre = append(pre, f.Value)
		}
	}
	d.Preheader = strings.Trim(strings.Join(pre, " · "), " ·")
	return Email{Subject: m.Title, Text: text.String(), HTML: renderEmail(d)}
}

// alertStatus is the short status label shown above an alert's title.
func alertStatus(m Message) string {
	switch m.Event {
	case "down":
		return "Down"
	case "reminder":
		return "Still down"
	case "recovered":
		return "Recovered"
	case "degraded":
		if m.Status == "up" {
			return "Operational"
		}
		return "Degraded"
	case "incident", "cert_expiring", "acknowledged":
		return strings.TrimSuffix(m.Label, " · Test")
	case "test":
		return "Test"
	}
	return m.Label
}

// truncate shortens s to about n bytes without splitting a character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - 1
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
