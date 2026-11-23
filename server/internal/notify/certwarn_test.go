package notify

import (
	"strings"
	"testing"
	"time"
)

func TestCertStep(t *testing.T) {
	cases := []struct {
		days, step int
		ok         bool
	}{
		{30, 0, false}, {15, 0, false}, {14, 14, true}, {10, 14, true}, {7, 7, true}, {5, 7, true},
		{3, 3, true}, {2, 3, true}, {1, 1, true}, {0, 1, true}, {-1, -1, true}, {-40, -1, true},
	}
	for _, c := range cases {
		if step, ok := certStep(c.days); step != c.step || ok != c.ok {
			t.Errorf("certStep(%d) = %d,%v want %d,%v", c.days, step, ok, c.step, c.ok)
		}
	}
}

func TestDaysLeft(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	if d := daysLeft(now.Add(7*24*time.Hour+time.Hour), now); d != 7 {
		t.Fatalf("got %d", d)
	}
	if d := daysLeft(now.Add(23*time.Hour), now); d != 0 {
		t.Fatalf("got %d", d)
	}
	if d := daysLeft(now.Add(-time.Hour), now); d != -1 {
		t.Fatalf("got %d", d)
	}
}

func TestCertMessage(t *testing.T) {
	n := &Notifier{adminURL: "https://admin.test", statusURL: "https://status.test"}
	ref := &MonRef{ID: 4, Name: "API", Type: "http", Target: "https://api.test"}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	m := n.certMessage(ref, now.Add(7*24*time.Hour+time.Hour), now)
	if m.Event != "cert_expiring" || m.Status != "degraded" || m.Title != "API: certificate expires in 7 days" ||
		m.URL != "https://admin.test/monitors/4" || m.Fields[len(m.Fields)-1].Name != "Expires" {
		t.Fatalf("%+v", m)
	}
	if m := n.certMessage(ref, now.Add(36*time.Hour), now); m.Status != "down" || !strings.HasSuffix(m.Title, "in 1 day") {
		t.Fatalf("%+v", m)
	}
	if m := n.certMessage(ref, now.Add(-time.Hour), now); m.Label != "Certificate expired" || m.Status != "down" {
		t.Fatalf("%+v", m)
	}
	// The email shows the explanation as text, not in the error box.
	e := alertEmail(m)
	if !strings.Contains(e.HTML, "Renew it before then") || strings.Contains(e.HTML, emailMono) {
		t.Fatal("cert email body")
	}
}
