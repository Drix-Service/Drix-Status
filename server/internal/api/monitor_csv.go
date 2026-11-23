package api

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"drix/server/internal/store"
)

// Monitors as CSV: one row per monitor, a header row naming the columns.
// Import and export use the same columns, so a file exported from one
// Drix imports into another unchanged.

const (
	csvMaxBytes = 1 << 20
	csvMaxRows  = 500
)

// baseColumns are the columns every monitor has, in export order.
var baseColumns = []string{"name", "type", "target", "group", "description", "interval_s", "timeout_ms", "retries", "degraded_ms", "enabled", "multi_location", "show_locations", "auto_incident"}

type optKind int

const (
	optString optKind = iota
	optUpper          // string, upper-cased
	optInt
	optBool
	optStatus     // expect_status: "200" or "200-299"
	optAssertions // "path op value; path op value"
	optHeaders    // "Name: value; Name: value"
)

// csvOption maps a column to a key of the monitor's config.
type csvOption struct {
	col   string
	kind  optKind
	types []string
}

// csvOptions are the type-specific columns, in export order.
var csvOptions = []csvOption{
	{"method", optUpper, []string{"http", "json"}},
	{"expect_status", optStatus, []string{"http", "json"}},
	{"keyword", optString, []string{"http", "json"}},
	{"keyword_absent", optString, []string{"http", "json"}},
	{"headers", optHeaders, []string{"http", "json"}},
	{"body", optString, []string{"http", "json"}},
	{"follow_redirects", optBool, []string{"http", "json"}},
	{"ignore_tls", optBool, []string{"http", "json", "smtp", "grpc"}},
	{"assertions", optAssertions, []string{"json"}},
	{"port", optInt, []string{"tcp"}},
	{"record_type", optUpper, []string{"dns"}},
	{"resolver", optString, []string{"dns"}},
	{"expect", optString, []string{"dns"}},
	{"warn_days", optInt, []string{"ssl"}},
	{"crit_days", optInt, []string{"ssl"}},
	{"server_name", optString, []string{"ssl"}},
	{"starttls", optBool, []string{"smtp"}},
	{"service", optString, []string{"grpc"}},
	{"tls", optBool, []string{"grpc"}},
	{"grace_s", optInt, []string{"push"}},
	{"count", optInt, []string{"ping"}},
}

// csvColumns is the full header, in export order.
func csvColumns() []string {
	cols := slices.Clone(baseColumns)
	for _, o := range csvOptions {
		cols = append(cols, o.col)
	}
	return cols
}

// csvRow is one parsed data row.
type csvRow struct {
	Line  int
	Input monitorInput
	Group string
	Err   error // a problem found while reading the row (before validation)
}

// parseMonitorCSV reads a CSV of monitors. A bad header fails the whole
// file; a bad row only marks that row.
func parseMonitorCSV(r io.Reader) ([]csvRow, error) {
	data, err := io.ReadAll(io.LimitReader(r, csvMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > csvMaxBytes {
		return nil, errors.New("the file is larger than 1 MB")
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // Excel's UTF-8 BOM

	cr := csv.NewReader(bytes.NewReader(data))
	cr.Comma = detectSeparator(data)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err == io.EOF {
		return nil, errors.New("the file is empty")
	}
	if err != nil {
		return nil, fmt.Errorf("couldn't read the header row: %w", err)
	}
	known := map[string]bool{}
	for _, c := range csvColumns() {
		known[c] = true
	}
	index := map[string]int{}
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if !known[h] {
			return nil, fmt.Errorf("unknown column %q in the header. Download the template to see the column names", h)
		}
		if _, dup := index[h]; dup {
			return nil, fmt.Errorf("column %q appears twice", h)
		}
		index[h] = i
	}
	for _, need := range []string{"name", "type"} {
		if _, ok := index[need]; !ok {
			return nil, fmt.Errorf("the header needs a %q column", need)
		}
	}

	var rows []csvRow
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		line, _ := cr.FieldPos(0)
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				return nil, fmt.Errorf("line %d: %v", pe.Line, pe.Err)
			}
			return nil, err
		}
		if isBlank(rec) {
			continue
		}
		if len(rows) == csvMaxRows {
			return nil, fmt.Errorf("the file has more than %d monitors", csvMaxRows)
		}
		get := func(col string) string {
			if i, ok := index[col]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		row := csvRow{Line: line, Group: get("group")}
		row.Input, row.Err = rowInput(get)
		rows = append(rows, row)
	}
	return rows, nil
}

// detectSeparator picks ';' when the header line has more of them than
// commas (Excel in many European locales), else ','.
func detectSeparator(data []byte) rune {
	first, _, _ := bufio.NewReader(bytes.NewReader(data)).ReadLine()
	if bytes.Count(first, []byte(";")) > bytes.Count(first, []byte(",")) {
		return ';'
	}
	return ','
}

func isBlank(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// rowInput turns one row into the same input the monitor form sends.
func rowInput(get func(string) string) (monitorInput, error) {
	in := monitorInput{
		Name: get("name"), Description: get("description"),
		Type: strings.ToLower(get("type")), Target: get("target"),
	}
	ints := []struct {
		col string
		dst *int
	}{{"interval_s", &in.IntervalS}, {"timeout_ms", &in.TimeoutMS}, {"degraded_ms", &in.DegradedMS}}
	for _, f := range ints {
		if v := get(f.col); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return in, fieldErr{f.col, fmt.Sprintf("%s must be a whole number", f.col)}
			}
			*f.dst = n
		}
	}
	if v := get("retries"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return in, fieldErr{"retries", "retries must be a whole number"}
		}
		in.Retries = &n
	}
	if v := get("enabled"); v != "" {
		b, ok := parseBool(v)
		if !ok {
			return in, fieldErr{"enabled", "enabled must be true or false"}
		}
		in.Enabled = &b
	}
	if v := get("multi_location"); v != "" {
		b, ok := parseBool(v)
		if !ok {
			return in, fieldErr{"multi_location", "multi_location must be true or false"}
		}
		in.MultiLocation = b
	}
	if v := get("auto_incident"); v != "" {
		b, ok := parseBool(v)
		if !ok {
			return in, fieldErr{"auto_incident", "auto_incident must be true or false"}
		}
		in.AutoIncident = &b
	}
	if v := get("show_locations"); v != "" {
		b, ok := parseBool(v)
		if !ok {
			return in, fieldErr{"show_locations", "show_locations must be true or false"}
		}
		in.ShowLocations = &b
	}

	cfg := map[string]any{}
	for _, o := range csvOptions {
		v := get(o.col)
		if v == "" {
			continue
		}
		if !slices.Contains(o.types, in.Type) {
			return in, fieldErr{o.col, fmt.Sprintf("%s doesn't apply to %s monitors", o.col, typeLabel(in.Type))}
		}
		switch o.kind {
		case optString:
			cfg[o.col] = v
		case optUpper:
			cfg[o.col] = strings.ToUpper(v)
		case optInt:
			n, err := strconv.Atoi(v)
			if err != nil {
				return in, fieldErr{o.col, fmt.Sprintf("%s must be a whole number", o.col)}
			}
			cfg[o.col] = n
		case optBool:
			b, ok := parseBool(v)
			if !ok {
				return in, fieldErr{o.col, fmt.Sprintf("%s must be true or false", o.col)}
			}
			cfg[o.col] = b
		case optStatus:
			lo, hi, ok := parseStatusRange(v)
			if !ok {
				return in, fieldErr{o.col, "expect_status must be a code like 200 or a range like 200-299"}
			}
			cfg["expect_status_min"], cfg["expect_status_max"] = lo, hi
		case optAssertions:
			as, err := parseAssertions(v)
			if err != nil {
				return in, fieldErr{o.col, err.Error()}
			}
			cfg[o.col] = as
		case optHeaders:
			h, err := parseHeaders(v)
			if err != nil {
				return in, fieldErr{o.col, err.Error()}
			}
			cfg[o.col] = h
		}
	}
	if len(cfg) > 0 {
		in.Config, _ = json.Marshal(cfg)
	}
	return in, nil
}

func typeLabel(t string) string {
	if t == "" {
		return "these"
	}
	return t
}

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "y", "1", "on":
		return true, true
	case "false", "no", "n", "0", "off":
		return false, true
	}
	return false, false
}

func parseStatusRange(s string) (int, int, bool) {
	lo, hi, isRange := strings.Cut(s, "-")
	a, err1 := strconv.Atoi(strings.TrimSpace(lo))
	b := a
	var err2 error
	if isRange {
		b, err2 = strconv.Atoi(strings.TrimSpace(hi))
	}
	if err1 != nil || err2 != nil || a < 100 || b > 599 || a > b {
		return 0, 0, false
	}
	return a, b, true
}

// parseAssertions reads "path op value; path op value".
func parseAssertions(s string) ([]map[string]string, error) {
	var out []map[string]string
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		f := strings.SplitN(part, " ", 3)
		if len(f) < 2 {
			return nil, fmt.Errorf("assertion %q needs a path and a comparison, like \"status equals ok\"", part)
		}
		a := map[string]string{"path": f[0], "op": strings.ToLower(f[1])}
		if len(f) == 3 {
			a["value"] = strings.TrimSpace(f[2])
		}
		out = append(out, a)
	}
	return out, nil
}

// parseHeaders reads "Name: value; Name: value".
func parseHeaders(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, ":")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("header %q should look like \"Name: value\"", part)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

// ---------- export ----------

// monitorRecord is the CSV row for a monitor (same order as csvColumns).
func monitorRecord(m store.Monitor, group string) []string {
	cfg := map[string]any{}
	_ = json.Unmarshal(m.Config, &cfg)
	str := func(k string) string {
		switch v := cfg[k].(type) {
		case string:
			return v
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		}
		return ""
	}
	rec := []string{m.Name, string(m.Type), m.Target, group, m.Description,
		strconv.Itoa(m.IntervalS), strconv.Itoa(m.TimeoutMS), strconv.Itoa(m.Retries), strconv.Itoa(m.DegradedMS), strconv.FormatBool(m.Enabled),
		strconv.FormatBool(m.MultiLocation), strconv.FormatBool(m.ShowLocations), strconv.FormatBool(m.AutoIncident)}
	for _, o := range csvOptions {
		v := ""
		switch o.kind {
		case optStatus:
			lo, hi := str("expect_status_min"), str("expect_status_max")
			switch {
			case lo == "" && hi == "":
			case lo == hi:
				v = lo
			default:
				v = cmpOr(lo, "200") + "-" + cmpOr(hi, "399")
			}
		case optAssertions:
			if as, ok := cfg["assertions"].([]any); ok {
				var parts []string
				for _, x := range as {
					a, _ := x.(map[string]any)
					p := fmt.Sprint(a["path"]) + " " + fmt.Sprint(a["op"])
					if val, _ := a["value"].(string); val != "" {
						p += " " + val
					}
					parts = append(parts, p)
				}
				v = strings.Join(parts, "; ")
			}
		case optHeaders:
			if hs, ok := cfg["headers"].(map[string]any); ok {
				keys := make([]string, 0, len(hs))
				for k := range hs {
					keys = append(keys, k)
				}
				slices.Sort(keys)
				var parts []string
				for _, k := range keys {
					parts = append(parts, k+": "+fmt.Sprint(hs[k]))
				}
				v = strings.Join(parts, "; ")
			}
		default:
			v = str(o.col)
		}
		rec = append(rec, v)
	}
	return rec
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// exportMonitors serves every monitor as CSV, with its first group.
func (a *API) exportMonitors(w http.ResponseWriter, r *http.Request) {
	data := a.Catalog.Get()
	groupOf := map[int64]string{}
	if p := a.page(); p != nil {
		for _, sec := range data.Sections[p.ID] {
			for _, pm := range sec.Monitors {
				if _, ok := groupOf[pm.MonitorID]; !ok {
					groupOf[pm.MonitorID] = sec.Name
				}
			}
		}
	}
	monitors, err := a.Store.ListMonitors(r.Context())
	if err != nil {
		a.serverErr(w, err)
		return
	}
	// Rows follow the status page: groups in order, their monitors in
	// order, then private monitors. Importing the file rebuilds the same page.
	byID := map[int64]store.Monitor{}
	for _, m := range monitors {
		byID[m.ID] = m
	}
	var ordered []store.Monitor
	done := map[int64]bool{}
	if p := a.page(); p != nil {
		for _, sec := range data.Sections[p.ID] {
			for _, pm := range sec.Monitors {
				if m, ok := byID[pm.MonitorID]; ok && !done[m.ID] {
					done[m.ID] = true
					ordered = append(ordered, m)
				}
			}
		}
	}
	for _, m := range monitors {
		if !done[m.ID] {
			ordered = append(ordered, m)
		}
	}

	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write(csvColumns())
	for _, m := range ordered {
		_ = cw.Write(monitorRecord(m, groupOf[m.ID]))
	}
	cw.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="drix-monitors-%s.csv"`, time.Now().Format("2006-01-02")))
	_, _ = w.Write(buf.Bytes())
}

// ---------- import ----------

type importRow struct {
	Line   int    `json:"line"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Target string `json:"target"`
	Group  string `json:"group"`
	Status string `json:"status"` // new | skip | error
	Error  string `json:"error,omitempty"`
	Field  string `json:"field,omitempty"`
}

type importResult struct {
	Rows      []importRow `json:"rows"`
	New       int         `json:"new"`
	Skipped   int         `json:"skipped"`
	Errors    int         `json:"errors"`
	NewGroups []string    `json:"new_groups"`
	Created   int         `json:"created"`
}

func (a *API) importMonitors(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CSV    string `json:"csv"`
		DryRun bool   `json:"dry_run"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*csvMaxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	rows, err := parseMonitorCSV(strings.NewReader(in.CSV))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if len(rows) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "the file has no monitors, only a header")
		return
	}
	page := a.page()
	if page == nil {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}

	data := a.Catalog.Get()
	existing := map[string]bool{}
	for _, m := range data.Monitors {
		existing[monitorKey(m.Name, m.Target)] = true
	}
	groups := map[string]bool{}
	for _, sec := range data.Sections[page.ID] {
		groups[strings.ToLower(sec.Name)] = true
	}

	res := importResult{Rows: []importRow{}, NewGroups: []string{}}
	var toCreate []store.Monitor
	var toGroup []string
	seen := map[string]bool{}
	for _, row := range rows {
		ir := importRow{Line: row.Line, Name: row.Input.Name, Type: row.Input.Type, Target: row.Input.Target, Group: row.Group, Status: "new"}
		m, verr := store.Monitor{}, row.Err
		if verr == nil {
			m, verr = a.validateMonitor(row.Input)
		}
		key := monitorKey(m.Name, m.Target)
		switch {
		case verr != nil:
			ir.Status, ir.Error = "error", verr.Error()
			var fe fieldErr
			if errors.As(verr, &fe) {
				ir.Field = fe.field
				// Name the CSV column for a type option, e.g. "assertions: …".
				if col, ok := strings.CutPrefix(fe.field, "config."); ok {
					col = strings.TrimSuffix(strings.TrimSuffix(col, "_min"), "_max")
					if col == "expect_status" || slices.ContainsFunc(csvOptions, func(o csvOption) bool { return o.col == col }) {
						ir.Error = col + ": " + fe.msg
					}
				}
			}
		case len(row.Group) > 100:
			ir.Status, ir.Error, ir.Field = "error", "Group name is too long (max 100 characters)", "group"
		case existing[key]:
			ir.Status, ir.Error = "skip", "Already exists"
		case seen[key]:
			ir.Status, ir.Error = "skip", "Listed twice in this file"
		}
		switch ir.Status {
		case "error":
			res.Errors++
		case "skip":
			res.Skipped++
		default:
			res.New++
			seen[key] = true
			toCreate = append(toCreate, m)
			toGroup = append(toGroup, row.Group)
			if g := strings.ToLower(row.Group); g != "" && !groups[g] {
				groups[g] = true
				res.NewGroups = append(res.NewGroups, row.Group)
			}
		}
		res.Rows = append(res.Rows, ir)
	}

	if in.DryRun {
		writeJSON(w, http.StatusOK, res)
		return
	}
	if res.Errors > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, struct {
			apiError
			importResult
		}{apiError{Error: fmt.Sprintf("Fix the %d row(s) with errors first; nothing was imported.", res.Errors)}, res})
		return
	}
	if len(toCreate) > 0 {
		created, err := a.Store.ImportMonitors(r.Context(), page.ID, toCreate, toGroup)
		if err != nil {
			a.serverErr(w, err)
			return
		}
		res.Created = len(created)
		a.changed(r.Context())
		summary := fmt.Sprintf("Imported %d monitor(s)", res.Created)
		if n := len(res.NewGroups); n > 0 {
			summary += fmt.Sprintf(" and %d new group(s)", n)
		}
		a.audit(r, "monitor.import", "monitor", nil, summary)
	}
	writeJSON(w, http.StatusOK, res)
}

func monitorKey(name, target string) string {
	return strings.ToLower(strings.TrimSpace(name)) + "\x00" + strings.TrimSpace(target)
}
