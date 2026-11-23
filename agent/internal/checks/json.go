package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"drix/shared/protocol"
)

// JSON performs an HTTP request and checks assertions against the JSON body.
func JSON(ctx context.Context, target string, c protocol.JSONConfig) protocol.Result {
	method := strings.ToUpper(c.Method)
	if method == "" {
		method = http.MethodGet
	}
	lo, hi := c.ExpectStatusMin, c.ExpectStatusMax
	if lo == 0 {
		lo = 200
	}
	if hi == 0 {
		hi = 299
	}
	var body io.Reader
	if c.Body != "" {
		body = strings.NewReader(c.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fail(err)
	}
	req.Header.Set("User-Agent", "DrixStatus-Agent/1.0")
	req.Header.Set("Accept", "application/json")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	tr := secureTransport
	if c.IgnoreTLS {
		tr = insecureTransport
	}
	client := &http.Client{Transport: tr}
	if c.FollowRedirects != nil && !*c.FollowRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return protocol.Result{OK: false, LatencyMS: ms(time.Since(start)), Message: timeoutMessage(ctx, err)}
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	r := protocol.Result{Code: resp.StatusCode, LatencyMS: ms(time.Since(start))}
	if err != nil {
		r.Message = "reading body: " + timeoutMessage(ctx, err)
		return r
	}
	if resp.StatusCode < lo || resp.StatusCode > hi {
		r.Message = fmt.Sprintf("unexpected status %d (want %d-%d)", resp.StatusCode, lo, hi)
		return r
	}

	var doc any
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		r.Message = "response is not valid JSON"
		return r
	}
	for _, a := range c.Assertions {
		if msg := evalAssertion(doc, a); msg != "" {
			r.Message = msg
			return r
		}
	}
	r.OK = true
	r.Message = fmt.Sprintf("%d OK, %d assertion(s) passed", resp.StatusCode, len(c.Assertions))
	return r
}

// lookup follows a dot path like "data.items.0.status".
func lookup(doc any, path string) (any, bool) {
	cur := doc
	if path == "" || path == "." {
		return cur, true
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "."), ".") {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[part]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func display(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(x)
		s := string(b)
		if len(s) > 60 {
			s = s[:60] + "…"
		}
		return s
	}
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func equal(v any, want string) bool {
	if f, ok := asFloat(v); ok {
		if w, err := strconv.ParseFloat(want, 64); err == nil {
			return f == w
		}
	}
	return display(v) == want
}

// evalAssertion returns "" when the assertion holds, else a readable reason.
func evalAssertion(doc any, a protocol.JSONAssertion) string {
	v, found := lookup(doc, a.Path)
	switch a.Op {
	case "exists":
		if !found {
			return fmt.Sprintf("%s: missing", a.Path)
		}
		return ""
	case "not_exists":
		if found {
			return fmt.Sprintf("%s: should not exist, got %q", a.Path, display(v))
		}
		return ""
	}
	if !found {
		return fmt.Sprintf("%s: missing", a.Path)
	}
	switch a.Op {
	case "equals", "":
		if !equal(v, a.Value) {
			return fmt.Sprintf("%s: expected %q, got %q", a.Path, a.Value, display(v))
		}
	case "not_equals":
		if equal(v, a.Value) {
			return fmt.Sprintf("%s: should not be %q", a.Path, a.Value)
		}
	case "contains":
		if arr, ok := v.([]any); ok {
			for _, el := range arr {
				if equal(el, a.Value) {
					return ""
				}
			}
			return fmt.Sprintf("%s: list doesn't contain %q", a.Path, a.Value)
		}
		if !strings.Contains(display(v), a.Value) {
			return fmt.Sprintf("%s: %q doesn't contain %q", a.Path, display(v), a.Value)
		}
	case "gt", "lt":
		f, ok := asFloat(v)
		w, err := strconv.ParseFloat(a.Value, 64)
		if !ok || err != nil {
			return fmt.Sprintf("%s: %q is not a number", a.Path, display(v))
		}
		if a.Op == "gt" && !(f > w) {
			return fmt.Sprintf("%s: %s is not greater than %s", a.Path, display(v), a.Value)
		}
		if a.Op == "lt" && !(f < w) {
			return fmt.Sprintf("%s: %s is not less than %s", a.Path, display(v), a.Value)
		}
	default:
		return fmt.Sprintf("unknown assertion %q", a.Op)
	}
	return ""
}
