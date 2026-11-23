package notify

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAckTokens(t *testing.T) {
	n := &Notifier{ackSecret: []byte("0123456789abcdef0123456789abcdef"), adminURL: "https://admin.example.com"}
	tok := n.AckToken(42, 1_800_000_000_000, 7)
	id, since, ch, ok := n.ParseAckToken(tok)
	if !ok || id != 42 || since != 1_800_000_000_000 || ch != 7 {
		t.Fatalf("round trip: %v %d %d %d", ok, id, since, ch)
	}
	// Changing any part breaks the signature.
	parts := strings.Split(tok, ".")
	for i, alt := range []string{"43", "1800000000001", "8"} {
		p := append([]string(nil), parts...)
		p[i] = alt
		if _, _, _, ok := n.ParseAckToken(strings.Join(p, ".")); ok {
			t.Errorf("tampered part %d accepted", i)
		}
	}
	other := &Notifier{ackSecret: []byte("another-secret-another-secret-xx")}
	if _, _, _, ok := other.ParseAckToken(tok); ok {
		t.Error("a token from another install was accepted")
	}
	if _, _, _, ok := (&Notifier{}).ParseAckToken(tok); ok {
		t.Error("without SECRET_KEY no token is valid")
	}
	if got := n.ackURL(42, 1_800_000_000_000, 7); got != "https://admin.example.com/ack/"+tok {
		t.Errorf("ack url %q", got)
	}
	if (&Notifier{adminURL: "https://x"}).ackURL(1, 2, 3) != "" {
		t.Error("no link without a secret")
	}
}

func TestAckButtons(t *testing.T) {
	m := Message{Event: "down", Title: "API is down", Subject: "API", Body: "HTTP 503", Status: "down",
		URL: "https://admin.example.com/monitors/1", AckURL: "https://admin.example.com/ack/x"}
	raw, _ := json.Marshal(discordPayload(m))
	if !strings.Contains(string(raw), `"label":"Acknowledge","style":5,"type":2,"url":"https://admin.example.com/ack/x"`) {
		t.Errorf("discord has no acknowledge button: %s", raw)
	}
	if links := plainAlert(m).Links; len(links) == 0 || links[0][0] != "Acknowledge" {
		t.Errorf("plain alert links: %v", links)
	}
	e := alertEmail(m)
	if !strings.Contains(e.HTML, "https://admin.example.com/ack/x") || !strings.Contains(e.HTML, ">Acknowledge</a>") {
		t.Error("email has no acknowledge button")
	}
	ack := Message{Event: "acknowledged", Subject: "API", Body: "Acknowledged by Lucas", Status: "degraded", Label: "Acknowledged"}
	raw, _ = json.Marshal(discordPayload(ack))
	if !strings.Contains(string(raw), "Acknowledged by Lucas") {
		t.Errorf("acknowledged message: %s", raw)
	}
	if strings.Contains(string(raw), "Acknowledge\"") {
		t.Error("an acknowledged message must not offer the button again")
	}
}
