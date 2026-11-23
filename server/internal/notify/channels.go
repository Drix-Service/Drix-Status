package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html"
	"net/url"
	"strconv"
	"strings"
)

// Telegram, Microsoft Teams, PagerDuty, Opsgenie, ntfy and Pushover.
// They share the wording of the Discord card: a heading, the status in
// plain words, details, and links to Drix and the status page.

// Channel configs.
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}
type TeamsConfig struct {
	URL string `json:"url"`
}
type PagerDutyConfig struct {
	RoutingKey string `json:"routing_key"`
}
type OpsgenieConfig struct {
	APIKey string `json:"api_key"`
	Region string `json:"region"` // us | eu
}
type NtfyConfig struct {
	Server string `json:"server"` // default https://ntfy.sh
	Topic  string `json:"topic"`
	Token  string `json:"token,omitempty"`
}
type PushoverConfig struct {
	AppToken string `json:"app_token"`
	UserKey  string `json:"user_key"`
}

// Service addresses (variables so tests can point them elsewhere).
var (
	telegramAPI   = "https://api.telegram.org"
	pagerDutyAPI  = "https://events.pagerduty.com/v2/enqueue"
	opsgenieUSAPI = "https://api.opsgenie.com"
	opsgenieEUAPI = "https://api.eu.opsgenie.com"
	pushoverAPI   = "https://api.pushover.net/1/messages.json"
)

// alertText is a message in plain parts, for channels without rich cards.
type alertText struct {
	Heading string      // monitor or incident name
	Status  string      // e.g. "Down", "Recovered after 18m", "Investigating"
	Summary string      // one line, e.g. the check error
	Body    string      // incident update or test text (may be multi-line)
	Rows    [][2]string // details
	Links   [][2]string // label, URL
}

func plainAlert(m Message) alertText {
	t := alertText{Heading: m.Subject, Summary: truncate(firstLine(m.Body), 300)}
	if t.Heading == "" || m.Event == "test" {
		t.Heading = strings.TrimPrefix(m.Title, "[Test] ")
	}
	field := func(name string) string {
		for _, f := range m.Fields {
			if f.Name == name {
				return f.Value
			}
		}
		return ""
	}
	switch m.Event {
	case "down":
		t.Status = "Down"
	case "reminder":
		t.Status = "Still down"
	case "recovered":
		t.Status, t.Summary = "Recovered", ""
		if d := field("Down for"); d != "" {
			t.Status += " after " + d
		}
	case "degraded":
		t.Status = "Degraded"
		if m.Status == "up" {
			t.Status = "Operational"
		}
	case "incident":
		t.Status, t.Summary, t.Body = strings.TrimSuffix(m.Label, " · Test"), "", strings.TrimSpace(m.Body)
	case "cert_expiring":
		t.Status = strings.TrimSuffix(m.Label, " · Test")
	case "acknowledged":
		t.Status, t.Summary = m.Body, ""
	case "test":
		t.Status, t.Summary, t.Body = "Test", "", strings.TrimSpace(m.Body)
	default:
		if m.Status != "" {
			t.Status = strings.ToUpper(m.Status[:1]) + m.Status[1:]
		}
	}
	if strings.HasPrefix(m.Title, "[Test] ") && m.Event != "test" {
		t.Status += " · Test"
	}
	for _, f := range m.Fields {
		if f.Name == "" || (f.Name == "Down for" && m.Event == "recovered") {
			continue
		}
		v := f.Value
		if f.Name == "Target" {
			v = strings.TrimPrefix(strings.TrimPrefix(v, "https://"), "http://")
		}
		t.Rows = append(t.Rows, [2]string{f.Name, v})
	}
	if !m.At.IsZero() {
		t.Rows = append(t.Rows, [2]string{"Time", m.At.UTC().Format("Mon 2 Jan 2006, 15:04 UTC")})
	}
	if isHTTPURL(m.AckURL) {
		t.Links = append(t.Links, [2]string{"Acknowledge", m.AckURL})
	}
	if isHTTPURL(m.URL) {
		t.Links = append(t.Links, [2]string{"Open in Drix", m.URL})
	}
	if isHTTPURL(m.StatusURL) {
		t.Links = append(t.Links, [2]string{"Status page", m.StatusURL})
	}
	return t
}

func isHTTPURL(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}

// publicURL is false for addresses outside services can't open (Telegram
// rejects buttons that point at localhost).
func publicURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !isHTTPURL(raw) {
		return false
	}
	h := u.Hostname()
	return h != "localhost" && !strings.HasPrefix(h, "127.") && h != "::1" && strings.Contains(h, ".")
}

// line is "Status · summary" without dangling separators.
func (t alertText) line() string { return strings.Trim(t.Status+" · "+t.Summary, " ·") }

// ---------- Telegram ----------

func sendTelegram(ctx context.Context, cfg TelegramConfig, m Message) error {
	body, _ := json.Marshal(telegramPayload(cfg.ChatID, m))
	return post(ctx, telegramAPI+"/bot"+cfg.BotToken+"/sendMessage", body, nil)
}

func telegramPayload(chatID string, m Message) map[string]any {
	t := plainAlert(m)
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString("<b>" + esc(t.Heading) + "</b>")
	if l := t.line(); l != "" {
		b.WriteString("\n" + esc(l))
	}
	if t.Body != "" {
		b.WriteString("\n\n" + esc(truncate(t.Body, 3000)))
	}
	if len(t.Rows) > 0 {
		b.WriteString("\n")
		for _, r := range t.Rows {
			b.WriteString("\n<b>" + esc(r[0]) + "</b>  " + esc(r[1]))
		}
	}
	p := map[string]any{"chat_id": chatID, "text": b.String(), "parse_mode": "HTML", "link_preview_options": map[string]any{"is_disabled": true}}
	var buttons []map[string]string
	for _, l := range t.Links {
		if publicURL(l[1]) {
			buttons = append(buttons, map[string]string{"text": l[0], "url": l[1]})
		}
	}
	if len(buttons) > 0 {
		p["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]string{buttons}}
	}
	return p
}

// ---------- Microsoft Teams ----------

func sendTeams(ctx context.Context, cfg TeamsConfig, m Message) error {
	body, _ := json.Marshal(teamsPayload(m))
	return post(ctx, cfg.URL, body, nil)
}

// teamsPayload is an Adaptive Card, which Teams workflow webhooks post as-is.
func teamsPayload(m Message) map[string]any {
	t := plainAlert(m)
	color := map[string]string{"down": "attention", "up": "good", "degraded": "warning"}[m.Status]
	if color == "" || m.Event == "test" {
		color = "default"
	}
	blocks := []any{
		map[string]any{"type": "TextBlock", "text": t.Heading, "size": "Large", "weight": "Bolder", "wrap": true},
	}
	if l := t.line(); l != "" {
		blocks = append(blocks, map[string]any{"type": "TextBlock", "text": l, "color": color, "weight": "Bolder", "spacing": "Small", "wrap": true})
	}
	if t.Body != "" {
		blocks = append(blocks, map[string]any{"type": "TextBlock", "text": truncate(t.Body, 3000), "wrap": true})
	}
	if len(t.Rows) > 0 {
		var facts []map[string]string
		for _, r := range t.Rows {
			facts = append(facts, map[string]string{"title": r[0], "value": r[1]})
		}
		blocks = append(blocks, map[string]any{"type": "FactSet", "facts": facts, "separator": true, "spacing": "Medium"})
	}
	var actions []map[string]string
	for _, l := range t.Links {
		actions = append(actions, map[string]string{"type": "Action.OpenUrl", "title": l[0], "url": l[1]})
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body":    blocks,
	}
	if len(actions) > 0 {
		card["actions"] = actions
	}
	return map[string]any{
		"type":        "message",
		"attachments": []any{map[string]any{"contentType": "application/vnd.microsoft.card.adaptive", "contentUrl": nil, "content": card}},
	}
}

// ---------- PagerDuty and Opsgenie ----------

// pageAction decides what an on-call tool should do with a message:
// open (trigger) or close (resolve) the alert with this key, at this
// severity (critical | error | warning | info), or nothing.
type pageAction struct {
	Skip     bool
	Resolve  bool
	Key      string
	Severity string
}

func idFromURL(u, part string) string {
	i := strings.LastIndex(u, "/"+part+"/")
	if i < 0 {
		return ""
	}
	return strings.Trim(u[i+len(part)+2:], "/")
}

func pageActionFor(m Message) pageAction {
	monKey := ""
	if m.Monitor != nil {
		monKey = "drix-monitor-" + strconv.FormatInt(m.Monitor.ID, 10)
	}
	switch m.Event {
	case "down":
		return pageAction{Key: monKey, Severity: "critical"}
	case "recovered":
		return pageAction{Key: monKey, Resolve: true}
	case "reminder":
		return pageAction{Skip: true} // the on-call tool reminds by itself
	case "degraded":
		if m.Status == "up" {
			return pageAction{Key: monKey + "-degraded", Resolve: true}
		}
		return pageAction{Key: monKey + "-degraded", Severity: "warning"}
	case "cert_expiring":
		sev := "warning"
		if m.Status == "down" {
			sev = "error"
		}
		return pageAction{Key: strings.Replace(monKey, "monitor", "cert", 1), Severity: sev}
	case "incident":
		key := "drix-incident-" + idFromURL(m.URL, "incidents")
		switch m.Status {
		case "maintenance":
			return pageAction{Skip: true} // planned work doesn't page anyone
		case "up":
			return pageAction{Key: key, Resolve: true}
		case "down":
			return pageAction{Key: key, Severity: "critical"}
		default:
			return pageAction{Key: key, Severity: "warning"}
		}
	}
	return pageAction{Skip: true}
}

func randomKey() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func pageSummary(m Message) string {
	t := plainAlert(m)
	s := t.Heading
	if l := t.line(); l != "" {
		s += ": " + l
	}
	return truncate(s, 1000)
}

func pageDetails(m Message) map[string]string {
	t := plainAlert(m)
	d := map[string]string{}
	for _, r := range t.Rows {
		d[r[0]] = r[1]
	}
	if t.Body != "" {
		d["Update"] = truncate(t.Body, 2000)
	}
	return d
}

func sendPagerDuty(ctx context.Context, cfg PagerDutyConfig, m Message) error {
	if m.Event == "test" {
		// A test opens and immediately resolves an info-level alert.
		key := "drix-test-" + randomKey()
		if err := postPagerDuty(ctx, cfg, m, pageAction{Key: key, Severity: "info"}); err != nil {
			return err
		}
		return postPagerDuty(ctx, cfg, m, pageAction{Key: key, Resolve: true})
	}
	a := pageActionFor(m)
	if a.Skip || a.Key == "" || strings.HasSuffix(a.Key, "-") {
		return nil
	}
	return postPagerDuty(ctx, cfg, m, a)
}

func pagerDutyPayload(cfg PagerDutyConfig, m Message, a pageAction) map[string]any {
	p := map[string]any{"routing_key": cfg.RoutingKey, "dedup_key": a.Key, "event_action": "trigger"}
	if a.Resolve {
		p["event_action"] = "resolve"
		return p
	}
	ev := map[string]any{
		"summary": pageSummary(m), "source": "Drix Status", "severity": a.Severity,
		"custom_details": pageDetails(m),
	}
	if !m.At.IsZero() {
		ev["timestamp"] = m.At.UTC().Format("2006-01-02T15:04:05Z")
	}
	if m.Monitor != nil {
		ev["component"] = m.Monitor.Name
		ev["class"] = m.Monitor.Type
	}
	p["payload"] = ev
	p["client"] = "Drix Status"
	if isHTTPURL(m.URL) {
		p["client_url"] = m.URL
		p["links"] = []map[string]string{{"href": m.URL, "text": "Open in Drix"}}
	}
	return p
}

func postPagerDuty(ctx context.Context, cfg PagerDutyConfig, m Message, a pageAction) error {
	body, _ := json.Marshal(pagerDutyPayload(cfg, m, a))
	return post(ctx, pagerDutyAPI, body, nil)
}

var opsgeniePriority = map[string]string{"critical": "P1", "error": "P2", "warning": "P3", "info": "P5"}

func opsgenieBase(cfg OpsgenieConfig) string {
	if strings.EqualFold(cfg.Region, "eu") {
		return opsgenieEUAPI
	}
	return opsgenieUSAPI
}

func sendOpsgenie(ctx context.Context, cfg OpsgenieConfig, m Message) error {
	if m.Event == "test" {
		key := "drix-test-" + randomKey()
		if err := postOpsgenie(ctx, cfg, m, pageAction{Key: key, Severity: "info"}); err != nil {
			return err
		}
		return postOpsgenie(ctx, cfg, m, pageAction{Key: key, Resolve: true})
	}
	a := pageActionFor(m)
	if a.Skip || a.Key == "" || strings.HasSuffix(a.Key, "-") {
		return nil
	}
	return postOpsgenie(ctx, cfg, m, a)
}

func opsgenieRequest(cfg OpsgenieConfig, m Message, a pageAction) (string, map[string]any) {
	base := opsgenieBase(cfg) + "/v2/alerts"
	if a.Resolve {
		return base + "/" + url.PathEscape(a.Key) + "/close?identifierType=alias", map[string]any{"source": "Drix Status", "note": "Resolved in Drix"}
	}
	t := plainAlert(m)
	desc := t.line()
	if t.Body != "" {
		desc += "\n\n" + t.Body
	}
	if isHTTPURL(m.URL) {
		desc += "\n\n" + m.URL
	}
	return base, map[string]any{
		"message":     truncate(pageSummary(m), 130),
		"alias":       a.Key,
		"description": truncate(desc, 15000),
		"priority":    opsgeniePriority[a.Severity],
		"source":      "Drix Status",
		"details":     pageDetails(m),
		"tags":        []string{"drix"},
	}
}

func postOpsgenie(ctx context.Context, cfg OpsgenieConfig, m Message, a pageAction) error {
	u, p := opsgenieRequest(cfg, m, a)
	body, _ := json.Marshal(p)
	return post(ctx, u, body, map[string]string{"Authorization": "GenieKey " + cfg.APIKey})
}

// ---------- ntfy ----------

func ntfyPriority(m Message) int {
	switch {
	case m.Event == "down" || m.Event == "reminder":
		return 5
	case m.Status == "down":
		return 4
	case m.Status == "degraded":
		return 4
	default:
		return 3
	}
}

var ntfyTags = map[string]string{
	"down": "rotating_light", "reminder": "rotating_light", "recovered": "white_check_mark",
	"degraded": "warning", "incident": "loudspeaker", "cert_expiring": "lock", "test": "white_check_mark",
}

func ntfyPayload(cfg NtfyConfig, m Message) map[string]any {
	t := plainAlert(m)
	msg := t.line()
	if t.Body != "" {
		msg = strings.Trim(msg+"\n"+t.Body, "\n")
	}
	for _, r := range t.Rows {
		msg += "\n" + r[0] + ": " + r[1]
	}
	p := map[string]any{"topic": cfg.Topic, "title": t.Heading, "message": truncate(strings.TrimSpace(msg), 3800), "priority": ntfyPriority(m)}
	if tag := ntfyTags[m.Event]; tag != "" {
		p["tags"] = []string{tag}
	}
	if m.Event == "degraded" && m.Status == "up" {
		p["tags"] = []string{"white_check_mark"}
	}
	if isHTTPURL(m.URL) {
		p["click"] = m.URL
	}
	var actions []map[string]any
	for _, l := range t.Links {
		actions = append(actions, map[string]any{"action": "view", "label": l[0], "url": l[1]})
	}
	if len(actions) > 0 {
		p["actions"] = actions
	}
	return p
}

func ntfyServer(cfg NtfyConfig) string {
	s := strings.TrimRight(strings.TrimSpace(cfg.Server), "/")
	if s == "" {
		return "https://ntfy.sh"
	}
	return s
}

func sendNtfy(ctx context.Context, cfg NtfyConfig, m Message) error {
	body, _ := json.Marshal(ntfyPayload(cfg, m))
	var h map[string]string
	if cfg.Token != "" {
		h = map[string]string{"Authorization": "Bearer " + cfg.Token}
	}
	return post(ctx, ntfyServer(cfg), body, h)
}

// ---------- Pushover ----------

func pushoverPayload(cfg PushoverConfig, m Message) map[string]any {
	t := plainAlert(m)
	esc := html.EscapeString
	msg := ""
	if l := t.line(); l != "" {
		msg = "<b>" + esc(l) + "</b>"
	}
	if t.Body != "" {
		msg += "\n" + esc(t.Body)
	}
	for _, r := range t.Rows {
		msg += "\n" + esc(r[0]) + ": " + esc(r[1])
	}
	p := map[string]any{
		"token": cfg.AppToken, "user": cfg.UserKey, "title": truncate(t.Heading, 250),
		"message": truncate(strings.TrimSpace(msg), 1000), "html": 1, "priority": 0,
	}
	if m.Event == "down" || m.Event == "reminder" {
		p["priority"] = 1
	}
	if !m.At.IsZero() {
		p["timestamp"] = m.At.Unix()
	}
	if isHTTPURL(m.URL) {
		p["url"], p["url_title"] = m.URL, "Open in Drix"
	}
	return p
}

func sendPushover(ctx context.Context, cfg PushoverConfig, m Message) error {
	body, _ := json.Marshal(pushoverPayload(cfg, m))
	return post(ctx, pushoverAPI, body, nil)
}

// RichPreviewTypes are channels whose "Send test" sends one message of
// every alert kind (on-call tools get a single test that resolves itself).
var RichPreviewTypes = map[string]bool{"discord": true, "telegram": true, "teams": true, "ntfy": true, "pushover": true}

// ChannelTypes are every supported channel type.
var ChannelTypes = []string{"email", "discord", "slack", "webhook", "telegram", "teams", "pagerduty", "opsgenie", "ntfy", "pushover"}
