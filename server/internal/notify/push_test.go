package notify

import (
	"testing"
	"time"
)

func TestPushPayload(t *testing.T) {
	n := &Notifier{adminURL: "https://admin.test"}
	ref := &MonRef{ID: 4, Name: "API", Type: "http", Target: "https://api.test/health"}
	p := n.pushPayload(Message{Event: "down", Title: "API is down", Body: "HTTP 503 Service Unavailable\nmore", Monitor: ref,
		URL: "https://admin.test/monitors/4", Status: "down"})
	if p.Title != "API is down" || p.Body != "HTTP 503 Service Unavailable · api.test/health" || p.URL != "/m/monitors/4" || p.Tag != "monitor-4" {
		t.Fatalf("%+v", p)
	}

	inc := n.pushPayload(Message{Event: "incident", Title: "Incident: Errors (Investigating)", Label: "Incident · Investigating",
		Body: "Looking into it.", URL: "https://admin.test/incidents/9", At: time.Now()})
	if inc.Body != "Incident · Investigating · Looking into it." || inc.URL != "/m/incidents/9" || inc.Tag != "incident-9" {
		t.Fatalf("%+v", inc)
	}

	for _, s := range SampleMessages("https://admin.test", "https://status.test", time.Now()) {
		p := n.pushPayload(s)
		if p.Title == "" || p.URL == "" || len(p.Title) > 125 {
			t.Fatalf("%+v", p)
		}
		if p.Title[:6] != "Test: " {
			t.Fatalf("sample not marked as test: %q", p.Title)
		}
	}
	if pushTopic("monitor-4 down!") != "monitor-4down" || len(pushTopic(string(make([]byte, 0))+"abcdefghijklmnopqrstuvwxyz0123456789")) != 32 {
		t.Fatal("topic")
	}
}
