package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// svcCapture records every request a fake service receives.
type svcCapture struct {
	mu   sync.Mutex
	reqs []svcReq
}
type svcReq struct {
	Path, Auth string
	Body       map[string]any
}

func (c *svcCapture) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		c.mu.Lock()
		c.reqs = append(c.reqs, svcReq{Path: r.URL.RequestURI(), Auth: r.Header.Get("Authorization"), Body: m})
		c.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func downMsg() Message {
	ref := &MonRef{ID: 4, Name: "API", Type: "http", Target: "https://api.example.com/health"}
	return Message{Event: "down", Title: "API is down", Subject: "API", Status: "down", Body: "HTTP 503 Service Unavailable",
		At: time.Unix(1700000000, 0).UTC(), Monitor: ref, URL: "https://admin.example.com/monitors/4", StatusURL: "https://status.example.com",
		Fields: monitorFields(ref)}
}

func TestTelegramPayload(t *testing.T) {
	m := downMsg()
	m.Subject = "API <prod>"
	p := telegramPayload("-100123", m)
	text := p["text"].(string)
	if !strings.HasPrefix(text, "<b>API &lt;prod&gt;</b>\nDown · HTTP 503 Service Unavailable") || !strings.Contains(text, "<b>Target</b>  api.example.com/health") ||
		p["parse_mode"] != "HTML" || p["chat_id"] != "-100123" {
		t.Fatalf("%v", p)
	}
	kb := p["reply_markup"].(map[string]any)["inline_keyboard"].([][]map[string]string)
	if len(kb[0]) != 2 || kb[0][0]["url"] != "https://admin.example.com/monitors/4" {
		t.Fatal(kb)
	}
	// Localhost links can't be buttons.
	m.URL, m.StatusURL = "http://localhost:3001/monitors/4", "http://localhost:3000"
	if _, ok := telegramPayload("1", m)["reply_markup"]; ok {
		t.Fatal("localhost buttons")
	}

	var c svcCapture
	srv := c.server(t)
	old := telegramAPI
	telegramAPI = srv.URL
	defer func() { telegramAPI = old }()
	if err := sendTelegram(context.Background(), TelegramConfig{BotToken: "123:abc", ChatID: "5"}, downMsg()); err != nil {
		t.Fatal(err)
	}
	if c.reqs[0].Path != "/bot123:abc/sendMessage" {
		t.Fatal(c.reqs[0].Path)
	}
}

func TestTeamsPayload(t *testing.T) {
	p := teamsPayload(downMsg())
	att := p["attachments"].([]any)[0].(map[string]any)
	card := att["content"].(map[string]any)
	body := card["body"].([]any)
	if att["contentType"] != "application/vnd.microsoft.card.adaptive" || body[0].(map[string]any)["text"] != "API" ||
		body[1].(map[string]any)["color"] != "attention" || len(card["actions"].([]map[string]string)) != 2 {
		t.Fatalf("%v", card)
	}
}

func TestPageActions(t *testing.T) {
	m := downMsg()
	cases := []struct {
		event, status string
		want          pageAction
	}{
		{"down", "down", pageAction{Key: "drix-monitor-4", Severity: "critical"}},
		{"recovered", "up", pageAction{Key: "drix-monitor-4", Resolve: true}},
		{"reminder", "down", pageAction{Skip: true}},
		{"degraded", "degraded", pageAction{Key: "drix-monitor-4-degraded", Severity: "warning"}},
		{"degraded", "up", pageAction{Key: "drix-monitor-4-degraded", Resolve: true}},
		{"cert_expiring", "degraded", pageAction{Key: "drix-cert-4", Severity: "warning"}},
	}
	for _, c := range cases {
		m.Event, m.Status = c.event, c.status
		if got := pageActionFor(m); got != c.want {
			t.Errorf("%s/%s: %+v", c.event, c.status, got)
		}
	}
	inc := Message{Event: "incident", Status: "down", URL: "https://admin.example.com/incidents/9"}
	if a := pageActionFor(inc); a.Key != "drix-incident-9" || a.Severity != "critical" {
		t.Fatal(a)
	}
	inc.Status = "up"
	if a := pageActionFor(inc); !a.Resolve {
		t.Fatal(a)
	}
	inc.Status = "maintenance"
	if a := pageActionFor(inc); !a.Skip {
		t.Fatal(a)
	}
}

func TestPagerDutySend(t *testing.T) {
	var c svcCapture
	srv := c.server(t)
	old := pagerDutyAPI
	pagerDutyAPI = srv.URL + "/v2/enqueue"
	defer func() { pagerDutyAPI = old }()
	cfg := PagerDutyConfig{RoutingKey: "R0UT1NGKEY0000000000000000000000"}

	if err := sendPagerDuty(context.Background(), cfg, downMsg()); err != nil {
		t.Fatal(err)
	}
	b := c.reqs[0].Body
	pl := b["payload"].(map[string]any)
	if b["event_action"] != "trigger" || b["dedup_key"] != "drix-monitor-4" || pl["severity"] != "critical" ||
		pl["summary"] != "API: Down · HTTP 503 Service Unavailable" || b["routing_key"] != cfg.RoutingKey {
		t.Fatal(b)
	}
	// A test triggers and resolves.
	c.reqs = nil
	if err := sendPagerDuty(context.Background(), cfg, Message{Event: "test", Title: "Test"}); err != nil {
		t.Fatal(err)
	}
	if len(c.reqs) != 2 || c.reqs[0].Body["event_action"] != "trigger" || c.reqs[1].Body["event_action"] != "resolve" ||
		c.reqs[0].Body["dedup_key"] != c.reqs[1].Body["dedup_key"] {
		t.Fatal(c.reqs)
	}
	// A reminder sends nothing.
	c.reqs = nil
	m := downMsg()
	m.Event = "reminder"
	if err := sendPagerDuty(context.Background(), cfg, m); err != nil || len(c.reqs) != 0 {
		t.Fatal("reminder should be skipped")
	}
}

func TestOpsgenieSend(t *testing.T) {
	var c svcCapture
	srv := c.server(t)
	old := opsgenieEUAPI
	opsgenieEUAPI = srv.URL
	defer func() { opsgenieEUAPI = old }()
	cfg := OpsgenieConfig{APIKey: "key-123", Region: "eu"}

	if err := sendOpsgenie(context.Background(), cfg, downMsg()); err != nil {
		t.Fatal(err)
	}
	if r := c.reqs[0]; r.Path != "/v2/alerts" || r.Auth != "GenieKey key-123" || r.Body["alias"] != "drix-monitor-4" || r.Body["priority"] != "P1" {
		t.Fatal(r)
	}
	m := downMsg()
	m.Event, m.Status = "recovered", "up"
	if err := sendOpsgenie(context.Background(), cfg, m); err != nil {
		t.Fatal(err)
	}
	if p := c.reqs[1].Path; p != "/v2/alerts/drix-monitor-4/close?identifierType=alias" {
		t.Fatal(p)
	}
}

func TestNtfyAndPushover(t *testing.T) {
	var c svcCapture
	srv := c.server(t)
	if err := sendNtfy(context.Background(), NtfyConfig{Server: srv.URL + "/", Topic: "drix-alerts", Token: "tk"}, downMsg()); err != nil {
		t.Fatal(err)
	}
	r := c.reqs[0]
	if r.Path != "/" || r.Auth != "Bearer tk" || r.Body["topic"] != "drix-alerts" || r.Body["title"] != "API" || r.Body["priority"] != 5.0 ||
		r.Body["click"] != "https://admin.example.com/monitors/4" || !strings.HasPrefix(r.Body["message"].(string), "Down · HTTP 503") {
		t.Fatal(r)
	}
	if ntfyServer(NtfyConfig{}) != "https://ntfy.sh" {
		t.Fatal("default server")
	}

	old := pushoverAPI
	pushoverAPI = srv.URL + "/1/messages.json"
	defer func() { pushoverAPI = old }()
	if err := sendPushover(context.Background(), PushoverConfig{AppToken: "a", UserKey: "u"}, downMsg()); err != nil {
		t.Fatal(err)
	}
	p := c.reqs[1].Body
	if p["token"] != "a" || p["user"] != "u" || p["priority"] != 1.0 || p["html"] != 1.0 || !strings.HasPrefix(p["message"].(string), "<b>Down · HTTP 503") {
		t.Fatal(p)
	}
}

func TestEverySampleRendersOnEveryChannel(t *testing.T) {
	for _, m := range SampleMessages("https://admin.test", "https://status.test", time.Now()) {
		if telegramPayload("1", m)["text"] == "" || teamsPayload(m) == nil || ntfyPayload(NtfyConfig{Topic: "x"}, m)["title"] == "" ||
			pushoverPayload(PushoverConfig{}, m)["title"] == "" {
			t.Fatalf("%+v", m)
		}
		if strings.Contains(plainAlert(m).Heading, "[Test]") {
			t.Fatal("heading keeps [Test]")
		}
	}
}
