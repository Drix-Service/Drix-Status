package checks

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"drix/shared/protocol"
)

func spec(t protocol.MonitorType, target string, cfg any) protocol.MonitorSpec {
	raw, _ := json.Marshal(cfg)
	return protocol.MonitorSpec{ID: 7, Type: t, Target: target, TimeoutMS: 2000, Config: raw}
}

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("hello healthy world"))
		case "/fail":
			w.WriteHeader(503)
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		}
	}))
	defer srv.Close()

	cases := []struct {
		name   string
		path   string
		cfg    protocol.HTTPConfig
		wantOK bool
		msg    string
	}{
		{"ok", "/ok", protocol.HTTPConfig{}, true, "200"},
		{"bad status", "/fail", protocol.HTTPConfig{}, false, "unexpected status 503"},
		{"accept 503", "/fail", protocol.HTTPConfig{ExpectStatusMin: 500, ExpectStatusMax: 599}, true, "503"},
		{"keyword hit", "/ok", protocol.HTTPConfig{Keyword: "healthy"}, true, ""},
		{"keyword miss", "/ok", protocol.HTTPConfig{Keyword: "nope"}, false, "not found"},
		{"keyword absent", "/ok", protocol.HTTPConfig{KeywordAbsent: "world"}, false, "forbidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Run(context.Background(), spec(protocol.TypeHTTP, srv.URL+tc.path, tc.cfg))
			if r.OK != tc.wantOK {
				t.Fatalf("ok=%v want %v (%s)", r.OK, tc.wantOK, r.Message)
			}
			if !strings.Contains(r.Message, tc.msg) {
				t.Fatalf("message %q missing %q", r.Message, tc.msg)
			}
			if r.MonitorID != 7 {
				t.Fatalf("monitor id not set")
			}
		})
	}

	t.Run("timeout", func(t *testing.T) {
		m := spec(protocol.TypeHTTP, srv.URL+"/slow", protocol.HTTPConfig{})
		m.TimeoutMS = 50
		r := Run(context.Background(), m)
		if r.OK || r.Message != "timed out" {
			t.Fatalf("want timeout, got ok=%v msg=%q", r.OK, r.Message)
		}
	})
}

func TestTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)

	if r := Run(context.Background(), spec(protocol.TypeTCP, ln.Addr().String(), nil)); !r.OK {
		t.Fatalf("expected open: %s", r.Message)
	}
	if r := Run(context.Background(), spec(protocol.TypeTCP, "127.0.0.1", protocol.TCPConfig{Port: addr.Port})); !r.OK {
		t.Fatalf("expected open with port cfg: %s", r.Message)
	}
	ln.Close()
	if r := Run(context.Background(), spec(protocol.TypeTCP, addr.String(), nil)); r.OK {
		t.Fatalf("expected closed port to fail")
	}
	if r := Run(context.Background(), spec(protocol.TypeTCP, "127.0.0.1", nil)); r.OK {
		t.Fatalf("expected missing port to fail")
	}
}

func TestUnknownType(t *testing.T) {
	r := Run(context.Background(), protocol.MonitorSpec{ID: 1, Type: "carrier-pigeon"})
	if r.OK || !strings.Contains(r.Message, "unknown") {
		t.Fatalf("got %+v", r)
	}
}

func jsonRaw(v any) ([]byte, error) { return json.Marshal(v) }
