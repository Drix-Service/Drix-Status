package notify

import (
	"strings"
	"time"
)

// SampleMessages returns one message of every kind a channel can receive
// (monitor down, still down, recovered, degraded, back to normal, incident,
// incident resolved, scheduled maintenance, certificate expiring), built the same way as real
// alerts but marked as tests. Used by "Send test" to preview a channel.
func SampleMessages(adminURL, statusURL string, now time.Time) []Message {
	adminURL, statusURL = strings.TrimRight(adminURL, "/"), strings.TrimRight(statusURL, "/")
	mon := &MonRef{ID: 0, Name: "Example API", Type: "http", Target: "https://api.example.com/health"}
	monURL := adminURL + "/monitors"
	fields := func(extra ...Field) []Field { return append(monitorFields(mon), extra...) }
	start := now.Add(48 * time.Hour).Truncate(time.Hour)
	window := start.Format("2 Jan 15:04 MST") + " – " + start.Add(time.Hour).Format("2 Jan 15:04 MST")

	msgs := []Message{
		{Event: "down", Title: mon.Name + " is down", Label: "Monitor down", Status: "down",
			Body: "HTTP 503 Service Unavailable", URL: monURL, Fields: fields()},
		{Event: "reminder", Title: mon.Name + " is still down", Label: "Still down", Status: "down",
			Body: "HTTP 503 Service Unavailable", URL: monURL, Fields: fields(Field{Name: "Down for", Value: human(15 * time.Minute)})},
		{Event: "recovered", Title: mon.Name + " recovered", Label: "Recovered", Status: "up",
			Body: "HTTP 200 OK", URL: monURL, Fields: fields(Field{Name: "Down for", Value: human(18 * time.Minute)})},
		{Event: "degraded", Title: mon.Name + " is degraded", Label: "Degraded", Status: "degraded",
			Body: "Response time 2400 ms is above the 1000 ms limit", URL: monURL, Fields: fields()},
		{Event: "degraded", Title: mon.Name + " is back to normal", Label: "Back to normal", Status: "up",
			Body: "HTTP 200 OK in 142 ms", URL: monURL, Fields: fields()},
		{Event: "incident", Title: "Incident: Elevated error rates (Investigating)", Label: "Incident · Investigating",
			Subject: "Elevated error rates", Status: "down", URL: adminURL + "/incidents",
			Body: "Some API requests are failing. We're looking into it and will post an update within 30 minutes."},
		{Event: "incident", Title: "Incident: Elevated error rates (Resolved)", Label: "Incident · Resolved",
			Subject: "Elevated error rates", Status: "up", URL: adminURL + "/incidents",
			Body: "A fix has been deployed and error rates are back to normal."},
		{Event: "incident", Title: "Maintenance: Database upgrade (Scheduled)", Label: "Maintenance · Scheduled",
			Subject: "Database upgrade", Status: "maintenance", URL: adminURL + "/maintenance",
			Body: "The API will be read-only for up to 5 minutes.", Fields: []Field{{Name: "Window", Value: window}}},
	}
	// Certificate expiring, built exactly like the real warning.
	n := &Notifier{adminURL: adminURL, statusURL: statusURL}
	msgs = append(msgs, n.certMessage(mon, now.Add(7*24*time.Hour+time.Hour), now))
	for i := range msgs {
		m := &msgs[i]
		m.At = now
		m.StatusURL = statusURL
		if m.Subject == "" {
			m.Subject = mon.Name
		}
		if m.Event != "incident" {
			m.Monitor = mon
		}
		// Clearly a test, so nobody mistakes it for a real outage.
		m.Label += " · Test"
		m.Title = "[Test] " + m.Title
	}
	return msgs
}

// IsRateLimited reports whether a delivery error was HTTP 429 (slow down).
func IsRateLimited(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "HTTP 429")
}
