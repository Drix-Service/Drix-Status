package api

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"drix/server/internal/store"
)

func parseOne(t *testing.T, data string) csvRow {
	t.Helper()
	rows, err := parseMonitorCSV(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	return rows[0]
}

func cfgOf(t *testing.T, r csvRow) map[string]any {
	t.Helper()
	m := map[string]any{}
	if len(r.Input.Config) > 0 {
		if err := json.Unmarshal(r.Input.Config, &m); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func TestParseMonitorCSVBasics(t *testing.T) {
	// Header in any order and case, a BOM, quoted commas, blank lines skipped.
	data := "\xef\xbb\xbfType,NAME,Target,Group,interval_s,retries,enabled,keyword\n" +
		"http,\"Site, main\",https://example.com,Website & API,15,2,false,Example Domain\n\n"
	r := parseOne(t, data)
	if r.Err != nil || r.Input.Name != "Site, main" || r.Input.Type != "http" || r.Group != "Website & API" ||
		r.Input.IntervalS != 15 || *r.Input.Retries != 2 || *r.Input.Enabled || r.Line != 2 {
		t.Fatalf("%+v", r)
	}
	if cfgOf(t, r)["keyword"] != "Example Domain" {
		t.Fatal(string(r.Input.Config))
	}
}

func TestParseMonitorCSVSemicolons(t *testing.T) {
	r := parseOne(t, "name;type;target;record_type;resolver\ngithub.com MX;dns;github.com;mx;8.8.8.8\n")
	c := cfgOf(t, r)
	if r.Err != nil || c["record_type"] != "MX" || c["resolver"] != "8.8.8.8" {
		t.Fatalf("%+v %v", r, c)
	}
}

func TestParseMonitorCSVHeaderErrors(t *testing.T) {
	for _, data := range []string{
		"", "name,type,colour\n", "name,target\n", "name,type,name\n",
	} {
		if _, err := parseMonitorCSV(strings.NewReader(data)); err == nil {
			t.Errorf("%q: want an error", data)
		}
	}
	big := "name,type,target\n" + strings.Repeat("a,ping,1.1.1.1\n", csvMaxRows+1)
	if _, err := parseMonitorCSV(strings.NewReader(big)); err == nil {
		t.Error("too many rows")
	}
}

func TestParseMonitorCSVOptions(t *testing.T) {
	r := parseOne(t, "name,type,target,expect_status,assertions,headers,follow_redirects\n"+
		"API,json,https://api.test/health,200-299,status equals ok; data.db exists; latency lt 500,Authorization: Bearer x; X-A: 1,no\n")
	c := cfgOf(t, r)
	as, _ := c["assertions"].([]any)
	h, _ := c["headers"].(map[string]any)
	if r.Err != nil || c["expect_status_min"] != 200.0 || c["expect_status_max"] != 299.0 || len(as) != 3 ||
		h["Authorization"] != "Bearer x" || c["follow_redirects"] != false {
		t.Fatalf("%v", c)
	}
	if a := as[2].(map[string]any); a["path"] != "latency" || a["op"] != "lt" || a["value"] != "500" {
		t.Fatal(a)
	}

	// An option on the wrong type, a bad number, a bad bool, a bad status.
	for _, row := range []string{
		"x,tcp,db:5432,Example\n", "x,http,https://a.test,,abc\n", "x,http,https://a.test,,,maybe\n", "x,http,https://a.test,,,,700\n",
	} {
		r := parseOne(t, "name,type,target,keyword,interval_s,ignore_tls,expect_status\n"+row)
		if r.Err == nil {
			t.Errorf("%q: want a row error", row)
		}
	}
}

func TestMonitorCSVRoundTrip(t *testing.T) {
	retries := 1
	monitors := []store.Monitor{
		{Name: "Marketing site", Type: "http", Target: "https://example.com", IntervalS: 15, TimeoutMS: 10000, Retries: retries, Enabled: true,
			Config: json.RawMessage(`{"keyword":"Example Domain","expect_status_min":200,"expect_status_max":299,"headers":{"X-A":"1"}}`)},
		{Name: "github.com MX", Type: "dns", Target: "github.com", IntervalS: 30, TimeoutMS: 10000, Retries: retries, Enabled: true, MultiLocation: true, ShowLocations: true,
			Config: json.RawMessage(`{"record_type":"MX","resolver":"8.8.8.8"}`)},
		{Name: "Health", Type: "json", Target: "https://api.test/health", IntervalS: 60, TimeoutMS: 5000, Retries: 2, DegradedMS: 800, Enabled: false,
			Config: json.RawMessage(`{"assertions":[{"path":"status","op":"equals","value":"ok"},{"path":"db","op":"exists"}]}`)},
		{Name: "Backup", Type: "push", IntervalS: 86400, TimeoutMS: 10000, Retries: 0, Enabled: true, Config: json.RawMessage(`{"grace_s":3600}`)},
	}
	var b strings.Builder
	cw := csv.NewWriter(&b)
	_ = cw.Write(csvColumns())
	for i, m := range monitors {
		_ = cw.Write(monitorRecord(m, []string{"Website & API", "EU servers", "", "Jobs"}[i]))
	}
	cw.Flush()

	rows, err := parseMonitorCSV(strings.NewReader(b.String()))
	if err != nil || len(rows) != len(monitors) {
		t.Fatal(err, len(rows))
	}
	for i, r := range rows {
		m := monitors[i]
		if r.Err != nil || r.Input.Name != m.Name || r.Input.Type != string(m.Type) || r.Input.Target != m.Target ||
			r.Input.IntervalS != m.IntervalS || r.Input.TimeoutMS != m.TimeoutMS || *r.Input.Retries != m.Retries ||
			r.Input.DegradedMS != m.DegradedMS || *r.Input.Enabled != m.Enabled ||
			r.Input.MultiLocation != m.MultiLocation || *r.Input.ShowLocations != m.ShowLocations {
			t.Fatalf("row %d: %+v", i, r)
		}
		var want, got map[string]any
		_ = json.Unmarshal(m.Config, &want)
		_ = json.Unmarshal(r.Input.Config, &got)
		wb, _ := json.Marshal(want)
		gb, _ := json.Marshal(got)
		if string(wb) != string(gb) {
			t.Fatalf("row %d config: %s != %s", i, gb, wb)
		}
	}
	if rows[0].Group != "Website & API" || rows[2].Group != "" {
		t.Fatal("groups")
	}
}
