package checks

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"drix/shared/protocol"
)

const maxBody = 1 << 20 // 1 MiB is plenty for keyword matching

// Shared transports keep connection setup cost predictable; we deliberately
// disable keep-alives so every check measures a full fresh request.
var (
	secureTransport   = newTransport(false)
	insecureTransport = newTransport(true)
)

func newTransport(insecure bool) *http.Transport {
	return &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   true,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        0,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // opt-in per monitor
	}
}

// HTTP performs an HTTP(S) request and validates status and body.
func HTTP(ctx context.Context, target string, c protocol.HTTPConfig) protocol.Result {
	method := strings.ToUpper(c.Method)
	if method == "" {
		method = http.MethodGet
	}
	lo, hi := c.ExpectStatusMin, c.ExpectStatusMax
	if lo == 0 {
		lo = 200
	}
	if hi == 0 {
		hi = 399
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

	r := protocol.Result{Code: resp.StatusCode}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		exp := resp.TLS.PeerCertificates[0].NotAfter.UTC()
		r.CertExpiry = &exp
	}

	needBody := c.Keyword != "" || c.KeywordAbsent != ""
	var buf []byte
	if needBody {
		buf, err = io.ReadAll(io.LimitReader(resp.Body, maxBody))
		if err != nil {
			r.LatencyMS = ms(time.Since(start))
			r.Message = "reading body: " + timeoutMessage(ctx, err)
			return r
		}
	} else {
		// Drain a little so the timing includes the first body bytes.
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	}
	r.LatencyMS = ms(time.Since(start))

	switch {
	case resp.StatusCode < lo || resp.StatusCode > hi:
		r.Message = fmt.Sprintf("unexpected status %d (want %d-%d)", resp.StatusCode, lo, hi)
	case c.Keyword != "" && !bytes.Contains(buf, []byte(c.Keyword)):
		r.Message = fmt.Sprintf("keyword %q not found", c.Keyword)
	case c.KeywordAbsent != "" && bytes.Contains(buf, []byte(c.KeywordAbsent)):
		r.Message = fmt.Sprintf("forbidden keyword %q present", c.KeywordAbsent)
	default:
		r.OK = true
		r.Message = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return r
}
