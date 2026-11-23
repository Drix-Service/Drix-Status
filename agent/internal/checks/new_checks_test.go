package checks

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"drix/shared/protocol"
)

// ---------- SSL ----------

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newCA(t *testing.T) testCA {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Drix Test CA", Organization: []string{"Drix Test"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return testCA{cert, key, pool}
}

func (ca testCA) serve(t *testing.T, host string, notAfter time.Time) string {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: host},
		DNSNames: []string{host}, NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: notAfter,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.(*tls.Conn).Handshake(); c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func TestSSL(t *testing.T) {
	ca := newCA(t)
	sslRoots = ca.pool
	defer func() { sslRoots = nil }()
	day := 24 * time.Hour
	cases := []struct {
		name     string
		host     string
		ask      string
		expires  time.Duration
		ok, deg  bool
		contains string
	}{
		{"healthy", "svc.test", "", 60 * day, true, false, "valid for 59 days, issued by Drix Test"},
		{"warning window", "svc.test", "", 10*day + time.Hour, true, true, "expires in 10 days"},
		{"critical window", "svc.test", "", 3*day + time.Hour, false, false, "expires in 3 days"},
		{"expired", "svc.test", "", -2 * day, false, false, "invalid"},
		{"wrong hostname", "svc.test", "other.test", 60 * day, false, false, "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := ca.serve(t, tc.host, time.Now().Add(tc.expires))
			sn := tc.ask
			if sn == "" {
				sn = tc.host
			}
			cfg, _ := jsonRaw(protocol.SSLConfig{ServerName: sn})
			r := Run(context.Background(), protocol.MonitorSpec{ID: 1, Type: protocol.TypeSSL, Target: addr, TimeoutMS: 2000, Config: cfg})
			if r.OK != tc.ok || r.Degraded != tc.deg || !strings.Contains(r.Message, tc.contains) {
				t.Fatalf("got ok=%v deg=%v msg=%q; want ok=%v deg=%v containing %q", r.OK, r.Degraded, r.Message, tc.ok, tc.deg, tc.contains)
			}
			if tc.ok && r.CertExpiry == nil {
				t.Fatal("cert expiry not reported")
			}
		})
	}
}

// ---------- JSON ----------

func TestJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			_, _ = w.Write([]byte("<html>"))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","version":"1.4.2","queue":{"depth":12,"workers":["a","b"]},"items":[{"state":"ready"}],"ratio":0.98}`))
	}))
	defer srv.Close()
	cases := []struct {
		name   string
		path   string
		as     []protocol.JSONAssertion
		ok     bool
		reason string
	}{
		{"equals string", "/", []protocol.JSONAssertion{{Path: "status", Op: "equals", Value: "ok"}}, true, ""},
		{"equals mismatch", "/", []protocol.JSONAssertion{{Path: "status", Op: "equals", Value: "degraded"}}, false, `status: expected "degraded", got "ok"`},
		{"nested array index", "/", []protocol.JSONAssertion{{Path: "items.0.state", Op: "equals", Value: "ready"}}, true, ""},
		{"numeric lt", "/", []protocol.JSONAssertion{{Path: "queue.depth", Op: "lt", Value: "100"}}, true, ""},
		{"numeric gt fails", "/", []protocol.JSONAssertion{{Path: "queue.depth", Op: "gt", Value: "100"}}, false, "is not greater than"},
		{"numeric equals float", "/", []protocol.JSONAssertion{{Path: "ratio", Op: "equals", Value: "0.98"}}, true, ""},
		{"contains in array", "/", []protocol.JSONAssertion{{Path: "queue.workers", Op: "contains", Value: "b"}}, true, ""},
		{"contains in string", "/", []protocol.JSONAssertion{{Path: "version", Op: "contains", Value: "1.4"}}, true, ""},
		{"exists", "/", []protocol.JSONAssertion{{Path: "queue", Op: "exists"}}, true, ""},
		{"missing path", "/", []protocol.JSONAssertion{{Path: "queue.missing", Op: "equals", Value: "x"}}, false, "queue.missing: missing"},
		{"not_exists", "/", []protocol.JSONAssertion{{Path: "error", Op: "not_exists"}}, true, ""},
		{"not json", "/bad", []protocol.JSONAssertion{{Path: "status", Op: "exists"}}, false, "not valid JSON"},
		{"all must pass", "/", []protocol.JSONAssertion{{Path: "status", Op: "equals", Value: "ok"}, {Path: "queue.depth", Op: "lt", Value: "5"}}, false, "not less than"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := jsonRaw(protocol.JSONConfig{Assertions: tc.as})
			r := Run(context.Background(), protocol.MonitorSpec{ID: 1, Type: protocol.TypeJSON, Target: srv.URL + tc.path, TimeoutMS: 2000, Config: cfg})
			if r.OK != tc.ok || !strings.Contains(r.Message, tc.reason) {
				t.Fatalf("ok=%v msg=%q; want ok=%v containing %q", r.OK, r.Message, tc.ok, tc.reason)
			}
		})
	}
}

// ---------- SMTP ----------

func fakeSMTP(t *testing.T, greeting string, starttls bool) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				w := bufio.NewWriter(c)
				r := bufio.NewReader(c)
				w.WriteString(greeting + "\r\n")
				w.Flush()
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
					case strings.HasPrefix(cmd, "EHLO"):
						if starttls {
							w.WriteString("250-mail.test\r\n250 STARTTLS\r\n")
						} else {
							w.WriteString("250-mail.test\r\n250 SIZE 1000\r\n")
						}
					case cmd == "QUIT":
						w.WriteString("221 bye\r\n")
						w.Flush()
						return
					default:
						w.WriteString("502 not implemented\r\n")
					}
					w.Flush()
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestSMTP(t *testing.T) {
	cfg := func(c protocol.SMTPConfig) []byte { b, _ := jsonRaw(c); return b }
	run := func(addr string, c protocol.SMTPConfig) protocol.Result {
		return Run(context.Background(), protocol.MonitorSpec{ID: 1, Type: protocol.TypeSMTP, Target: addr, TimeoutMS: 2000, Config: cfg(c)})
	}
	if r := run(fakeSMTP(t, "220 mail.test ESMTP ready", false), protocol.SMTPConfig{}); !r.OK {
		t.Fatalf("healthy server: %s", r.Message)
	}
	if r := run(fakeSMTP(t, "554 go away", false), protocol.SMTPConfig{}); r.OK || !strings.Contains(r.Message, "greeting") {
		t.Fatalf("rejecting server should fail on greeting, got ok=%v %q", r.OK, r.Message)
	}
	if r := run(fakeSMTP(t, "220 mail.test ESMTP", false), protocol.SMTPConfig{StartTLS: true}); r.OK || !strings.Contains(r.Message, "STARTTLS") {
		t.Fatalf("missing STARTTLS should fail, got ok=%v %q", r.OK, r.Message)
	}
	if r := run("127.0.0.1:1", protocol.SMTPConfig{}); r.OK {
		t.Fatal("closed port should fail")
	}
}

// ---------- gRPC ----------

func TestGRPC(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	hs.SetServingStatus("billing", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
	go srv.Serve(ln)
	defer srv.Stop()

	run := func(service string) protocol.Result {
		cfg, _ := jsonRaw(protocol.GRPCConfig{Service: service})
		return Run(context.Background(), protocol.MonitorSpec{ID: 1, Type: protocol.TypeGRPC, Target: ln.Addr().String(), TimeoutMS: 2000, Config: cfg})
	}
	if r := run(""); !r.OK || r.Message != "serving" {
		t.Fatalf("server health: ok=%v %q", r.OK, r.Message)
	}
	if r := run("billing"); r.OK || r.Message != "not_serving" {
		t.Fatalf("not serving: ok=%v %q", r.OK, r.Message)
	}
	if r := run("nope"); r.OK || !strings.Contains(r.Message, "unknown service") {
		t.Fatalf("unknown service: ok=%v %q", r.OK, r.Message)
	}
}
