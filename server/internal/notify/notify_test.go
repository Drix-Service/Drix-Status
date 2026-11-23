package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"drix/server/internal/store"
)

func TestParseSMTPURL(t *testing.T) {
	s, err := ParseSMTPURL("smtp://localhost:1025?from=Status%20%3Cstatus@drix.local%3E&tls=none")
	if err != nil || s.Host != "localhost" || s.Port != 1025 || s.TLS != "none" || s.From != "Status <status@drix.local>" {
		t.Fatalf("got %+v %v", s, err)
	}
	s, _ = ParseSMTPURL("smtps://me:p%40ss@mail.example.com?from=a@b.c")
	if s.TLS != "tls" || s.Port != 465 || s.Username != "me" || s.Password != "p@ss" {
		t.Fatalf("smtps defaults: %+v", s)
	}
	s, _ = ParseSMTPURL("smtp://mail.example.com?from=a@b.c")
	if s.TLS != "starttls" || s.Port != 587 {
		t.Fatalf("smtp defaults: %+v", s)
	}
}

func TestCleanList(t *testing.T) {
	got := cleanList([]string{"a@x.io, b@x.io", "not-an-email", "c@x.io\nd@x.io"})
	if strings.Join(got, ",") != "a@x.io,b@x.io,c@x.io,d@x.io" {
		t.Fatalf("got %v", got)
	}
}

func capture(t *testing.T) (*httptest.Server, chan *http.Request, chan []byte) {
	reqs, bodies := make(chan *http.Request, 10), make(chan []byte, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs <- r
		bodies <- b
	}))
	t.Cleanup(srv.Close)
	return srv, reqs, bodies
}

var sample = Message{Event: "down", Title: "API is down", Body: "connection refused", Status: "down",
	URL: "https://admin.test/monitors/1", At: time.Unix(1700000000, 0).UTC(), Fields: []Field{{Name: "Target", Value: "api:443"}}}

func TestWebhookSigned(t *testing.T) {
	srv, reqs, bodies := capture(t)
	if err := sendWebhook(context.Background(), WebhookConfig{URL: srv.URL, Secret: "s3cret"}, sample, "d1"); err != nil {
		t.Fatal(err)
	}
	r, body := <-reqs, <-bodies
	if r.Header.Get("X-Drix-Event") != "down" || r.Header.Get("X-Drix-Delivery") != "d1" {
		t.Fatalf("headers: %v", r.Header)
	}
	if want := "sha256=" + Sign("s3cret", body); r.Header.Get("X-Drix-Signature") != want {
		t.Fatal("signature doesn't verify")
	}
	var m Message
	if json.Unmarshal(body, &m) != nil || m.Title != "API is down" {
		t.Fatalf("body: %s", body)
	}
}

// discordComp mirrors the Components V2 fields the tests look at.
type discordComp struct {
	Type        int           `json:"type"`
	Content     string        `json:"content"`
	AccentColor *int          `json:"accent_color"`
	Components  []discordComp `json:"components"`
	Accessory   *discordComp  `json:"accessory"`
	Style       int           `json:"style"`
	Label       string        `json:"label"`
	URL         string        `json:"url"`
	Divider     *bool         `json:"divider"`
	Spacing     int           `json:"spacing"`
}

type discordMsg struct {
	Flags           int             `json:"flags"`
	Content         *string         `json:"content"`
	Embeds          json.RawMessage `json:"embeds"`
	Components      []discordComp   `json:"components"`
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

// texts returns every Text Display's content, in order.
func (c discordComp) texts() []string {
	var out []string
	if c.Type == 10 {
		out = append(out, c.Content)
	}
	for _, k := range c.Components {
		out = append(out, k.texts()...)
	}
	return out
}

func cardShape(c discordComp) string {
	var shape []string
	for _, k := range c.Components {
		switch {
		case k.Type == 14 && k.Divider != nil && *k.Divider:
			shape = append(shape, fmt.Sprintf("divider%d", k.Spacing))
		case k.Type == 14:
			shape = append(shape, fmt.Sprintf("gap%d", k.Spacing))
		default:
			shape = append(shape, fmt.Sprint(k.Type))
		}
	}
	return strings.Join(shape, " ")
}

func card(t *testing.T, m Message) discordComp {
	t.Helper()
	b, _ := json.Marshal(discordPayload(m)["components"].([]any)[0])
	var dc discordComp
	if err := json.Unmarshal(b, &dc); err != nil {
		t.Fatal(err)
	}
	return dc
}

func TestDiscordComponentsV2(t *testing.T) {
	srv, reqs, bodies := capture(t)
	m := sample
	m.Label, m.Subject, m.StatusURL = "Monitor down", "API", "https://status.test"
	m.Monitor = &MonRef{ID: 1, Name: "API", Type: "http"}
	m.Fields = []Field{{Name: "Check", Value: "HTTP"}, {Name: "Target", Value: "https://api.example.com/health"}}
	if err := sendDiscord(context.Background(), WebhookConfig{URL: srv.URL + "/api/webhooks/1/abc?thread_id=42"}, m); err != nil {
		t.Fatal(err)
	}
	r := <-reqs
	if q := r.URL.Query(); q.Get("with_components") != "true" || q.Get("thread_id") != "42" {
		t.Fatalf("query: %s", r.URL.RawQuery)
	}
	var d discordMsg
	if err := json.Unmarshal(<-bodies, &d); err != nil {
		t.Fatal(err)
	}
	if d.Flags != 1<<15 || d.Content != nil || d.Embeds != nil || d.AllowedMentions.Parse == nil || len(d.AllowedMentions.Parse) != 0 {
		t.Fatalf("envelope: %+v", d)
	}
	c := d.Components[0]
	if len(d.Components) != 1 || c.Type != 17 || c.AccentColor == nil || *c.AccentColor != 0xdc2626 {
		t.Fatalf("container: %+v", d.Components)
	}
	// Heading + badge, divider, details, gap, buttons.
	if got := cardShape(c); got != "10 divider1 10 gap1 1" {
		t.Fatalf("layout: %s", got)
	}
	if got, want := c.Components[0].Content, "### API\nDown · connection refused"; got != want {
		t.Fatalf("top:\n%s\nwant:\n%s", got, want)
	}
	if got, want := c.Components[2].Content, "**Check**  HTTP\n**Target**  api.example.com/health\n**Started**  <t:1700000000:f>"; got != want {
		t.Fatalf("details:\n%s\nwant:\n%s", got, want)
	}
	btn := c.Components[4].Components
	if len(btn) != 2 || btn[0].Label != "Open in Drix" || btn[0].URL != "https://admin.test/monitors/1" || btn[0].Style != 5 ||
		btn[1].Label != "Status page" || btn[1].URL != "https://status.test" {
		t.Fatalf("buttons: %+v", btn)
	}
}

func TestDiscordVariants(t *testing.T) {
	mon := &MonRef{ID: 1, Name: "API", Type: "tcp"}
	// Recovery: duration in the summary, not repeated in the details.
	rec := card(t, Message{Event: "recovered", Title: "API recovered", Subject: "API", Body: "connected", Status: "up", Monitor: mon,
		Fields: []Field{{Name: "Check", Value: "TCP"}, {Name: "Down for", Value: "18m"}}})
	if *rec.AccentColor != 0x16a34a || rec.Components[0].Content != "### API\nRecovered after 18m" || rec.Components[2].Content != "**Check**  TCP" {
		t.Fatalf("recovery: %+v", rec.Components)
	}
	// Degraded → up reads as operational.
	if c := card(t, Message{Event: "degraded", Subject: "API", Status: "up", Body: "ok"}).Components[0].Content; c != "### API\nOperational · ok" {
		t.Fatalf("back to normal: %q", c)
	}

	// Incident: name escaped, state badge, update text kept as written, type in the details.
	inc := card(t, Message{Event: "incident", Title: "Maintenance: DB *upgrade* (Scheduled)", Label: "Maintenance · Scheduled",
		Subject: "DB *upgrade*", Body: "Read-only for **5 minutes**.\n@everyone", Status: "maintenance",
		Fields: []Field{{Name: "Window", Value: "24 Nov 10:00 – 11:00"}}})
	if *inc.AccentColor != 0x2563eb || inc.Components[0].Content != "### DB \\*upgrade\\*\nScheduled\nRead-only for **5 minutes**.\n@everyone" ||
		inc.Components[2].Content != "**Type**  Maintenance\n**Window**  24 Nov 10:00 – 11:00" || cardShape(inc) != "10 divider1 10" {
		t.Fatalf("incident: %+v", inc.Components)
	}

	// Test alert: grey, TEST badge, no buttons for non-http URLs.
	tm := card(t, Message{Event: "test", Title: "Test alert from Drix Status", Status: "up", URL: "localhost:3001/alerts"})
	if *tm.AccentColor != 0x71717a || cardShape(tm) != "10" || tm.Components[0].Content != "### Test alert from Drix Status" {
		t.Fatalf("test alert: %+v", tm.Components)
	}

	// Error text can't inject markdown, and a huge one stays under Discord's limit.
	big := sample
	big.Body = "**bold** " + strings.Repeat("x", 10000)
	n := 0
	for _, s := range card(t, big).texts() {
		n += len([]rune(s))
		if strings.Contains(s, "**bold**") {
			t.Fatal("error text not escaped")
		}
	}
	if n > 4000 {
		t.Fatalf("text too long: %d", n)
	}
}

func TestTruncateKeepsRunesWhole(t *testing.T) {
	got := truncate("blåbærgrød", 4) // "å" is two bytes at index 2–3
	if !utf8.ValidString(got) || got != "bl…" {
		t.Fatalf("got %q", got)
	}
}

func TestSlackPayload(t *testing.T) {
	srv, _, bodies := capture(t)
	if err := sendSlack(context.Background(), WebhookConfig{URL: srv.URL}, sample); err != nil {
		t.Fatal(err)
	}
	body := string(<-bodies)
	if !strings.Contains(body, `"text":"API is down"`) || !strings.Contains(body, "#dc2626") || !strings.Contains(body, `\u003chttps://admin.test/monitors/1|API is down\u003e`) {
		t.Fatalf("slack payload: %s", body)
	}
}

func TestPostReportsHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("Unknown Webhook"))
	}))
	defer srv.Close()
	err := sendDiscord(context.Background(), WebhookConfig{URL: srv.URL}, sample)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404: Unknown Webhook") {
		t.Fatalf("got %v", err)
	}
}

func TestEmailBuild(t *testing.T) {
	e := alertEmail(sample)
	e.Headers = map[string]string{"List-Unsubscribe": "<https://x/unsub>"}
	raw := string(build(nil2(), "ops@example.com", e))
	for _, want := range []string{"Subject: API is down", "To: ops@example.com", "multipart/alternative", "List-Unsubscribe: <https://x/unsub>", "text/html"} {
		if !strings.Contains(raw, want) {
			t.Errorf("email missing %q", want)
		}
	}
}

func nil2() *mail.Address { return &mail.Address{Name: "Drix", Address: "status@drix.test"} }

func TestSampleMessagesCoverEveryKind(t *testing.T) {
	msgs := SampleMessages("https://admin.test/", "https://status.test/", time.Unix(1700000000, 0).UTC())
	wantStatus := []string{"Down · ", "Still down · ", "Recovered after 18m", "Degraded · ", "Operational · ", "Investigating", "Resolved", "Scheduled", "Certificate expiring · "}
	if len(msgs) != len(wantStatus) {
		t.Fatalf("got %d messages", len(msgs))
	}
	wantAccent := map[string]int{"down": 0xdc2626, "up": 0x16a34a, "degraded": 0xd97706, "maintenance": 0x2563eb}
	for i, m := range msgs {
		if !strings.HasSuffix(m.Label, " · Test") || !strings.HasPrefix(m.Title, "[Test] ") || !strings.HasPrefix(m.URL, "https://admin.test/") ||
			strings.Contains(m.URL, "//monitors") || m.StatusURL != "https://status.test" {
			t.Fatalf("message %d: %+v", i, m)
		}
		dc := card(t, m)
		lines := strings.Split(dc.Components[0].Content, "\n")
		if *dc.AccentColor != wantAccent[m.Status] || !strings.HasPrefix(lines[0], "### ") || !strings.HasPrefix(lines[1], wantStatus[i]) ||
			strings.Contains(dc.Components[0].Content, "Test") ||
			cardShape(dc) != "10 divider1 10 gap1 1" || len(dc.Components[4].Components) != 2 {
			t.Fatalf("message %d: accent %x shape %s\n%s", i, *dc.AccentColor, cardShape(dc), dc.Components[0].Content)
		}
	}
}

func TestIsRateLimited(t *testing.T) {
	if !IsRateLimited(errors.New("HTTP 429: {\"retry_after\": 1.2}")) || IsRateLimited(errors.New("HTTP 400: bad")) || IsRateLimited(nil) {
		t.Fatal("IsRateLimited")
	}
}

func TestEmailTemplates(t *testing.T) {
	noFmtErrors := func(name, h string) {
		t.Helper()
		if strings.Contains(h, "%!") || !strings.HasPrefix(h, "<!doctype html>") || !strings.HasSuffix(h, "</html>") {
			t.Fatalf("%s: broken output", name)
		}
	}

	// Alert: status line, title, error in a code box, rows, button, preview text.
	m := sample
	m.Fields = []Field{{Name: "Check", Value: "HTTP"}, {Name: "Target", Value: "api:443"}}
	h := alertEmail(m).HTML
	noFmtErrors("alert", h)
	for _, want := range []string{"></span>Down</span></p>", ">API is down</h1>", "monospace;font-size:13px;line-height:20px;color:#e4e4e7;word-break:break-word;\">connection refused</div>", ">Target</td>", ">api:443</td>",
		">Tue 14 Nov 2023, 22:13 UTC</td>", `href="https://admin.test/monitors/1"`, ">Open in Drix</a>", ">connection refused · api:443"} {
		if !strings.Contains(h, want) {
			t.Fatalf("alert email missing %q", want)
		}
	}

	// User text is escaped everywhere; a bad accent colour can't inject CSS.
	page := &store.Page{Name: "Acme <b>Status</b>", AccentColor: "red;background:url(x)"}
	start := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	end := start.Add(26 * time.Hour)
	inc := store.Incident{Title: "DB <upgrade>", ScheduledStart: &start, ScheduledEnd: &end}
	h = renderEmail(updateDoc(page, inc, "Read-only.\n<script>x</script>", "Maintenance", "Scheduled", "#2563eb", []string{"API"},
		"https://status.test/incidents/1", "https://status.test/unsub?token=t"))
	noFmtErrors("update", h)
	for _, bad := range []string{"<script>", "<b>Status</b>", "<upgrade>", "url(x)"} {
		if strings.Contains(h, bad) {
			t.Fatalf("update email contains %q", bad)
		}
	}
	for _, want := range []string{"Maintenance · Scheduled", "Read-only.<br>&lt;script&gt;", ">Tue 24 Nov, 10:00 – Wed 25 Nov, 12:00 UTC</td>",
		`href="https://status.test/unsub?token=t"`, ">Unsubscribe</a>", "background-color:#fafafa"} {
		if !strings.Contains(h, want) {
			t.Fatalf("update email missing %q", want)
		}
	}

	// Confirmation: page accent on the button, link fallback.
	h = renderEmail(confirmDoc(&store.Page{Name: "Acme", AccentColor: "#2563eb"}, "https://status.test/confirm?token=abc"))
	noFmtErrors("confirm", h)
	if strings.Contains(h, "#2563eb") || strings.Contains(h, "#3b82f6") || !strings.Contains(h, "background-color:#fafafa") || !strings.Contains(h, "Or copy this link") || strings.Count(h, "https://status.test/confirm?token=abc") != 3 {
		t.Fatal("confirm email")
	}

	// Invite and SMTP test.
	h = renderEmail(inviteDoc("Ann invited you to Drix Status", "editor", "https://admin.test/invite/x"))
	noFmtErrors("invite", h)
	if !strings.Contains(h, ">Editor</td>") || !strings.Contains(h, ">Accept invite</a>") {
		t.Fatal("invite email")
	}
	e := TestEmail("me@example.com", "now")
	noFmtErrors("smtp test", e.HTML)
	if e.To[0] != "me@example.com" || !strings.Contains(e.HTML, ">Email is working</h1>") {
		t.Fatal("smtp test email")
	}
}
