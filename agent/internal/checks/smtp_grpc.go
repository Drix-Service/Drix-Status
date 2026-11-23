package checks

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"drix/shared/protocol"
)

// SMTP connects to a mail server, expects a 220 greeting, says EHLO and
// optionally upgrades with STARTTLS (verifying the certificate).
func SMTP(ctx context.Context, target string, c protocol.SMTPConfig) protocol.Result {
	addr := target
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(target, "25")
	}
	host, _, _ := net.SplitHostPort(addr)

	var d net.Dialer
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return protocol.Result{OK: false, LatencyMS: ms(time.Since(start)), Message: timeoutMessage(ctx, err)}
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	client, err := smtp.NewClient(conn, host) // reads and checks the 220 greeting
	lat := ms(time.Since(start))
	if err != nil {
		return protocol.Result{OK: false, LatencyMS: lat, Message: "greeting: " + timeoutMessage(ctx, err)}
	}
	defer client.Close()
	if err := client.Hello("drix-agent.local"); err != nil {
		return protocol.Result{OK: false, LatencyMS: lat, Message: "EHLO: " + err.Error()}
	}
	msg := "220 greeting received"
	if c.StartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return protocol.Result{OK: false, LatencyMS: lat, Message: "server does not offer STARTTLS"}
		}
		if err := client.StartTLS(&tls.Config{ServerName: host, InsecureSkipVerify: c.IgnoreTLS}); err != nil { //nolint:gosec // opt-in
			return protocol.Result{OK: false, LatencyMS: ms(time.Since(start)), Message: "STARTTLS: " + err.Error()}
		}
		msg = "220 greeting, STARTTLS ok"
		r := protocol.Result{OK: true, LatencyMS: ms(time.Since(start)), Message: msg}
		if st, ok := client.TLSConnectionState(); ok && len(st.PeerCertificates) > 0 {
			exp := st.PeerCertificates[0].NotAfter.UTC()
			r.CertExpiry = &exp
		}
		_ = client.Quit()
		return r
	}
	_ = client.Quit()
	return protocol.Result{OK: true, LatencyMS: lat, Message: msg}
}

// GRPC calls grpc.health.v1.Health/Check.
func GRPC(ctx context.Context, target string, c protocol.GRPCConfig) protocol.Result {
	if _, _, err := net.SplitHostPort(target); err != nil {
		return fail(fmt.Errorf("target must be host:port"))
	}
	creds := insecure.NewCredentials()
	if c.TLS {
		creds = credentials.NewTLS(&tls.Config{InsecureSkipVerify: c.IgnoreTLS}) //nolint:gosec // opt-in
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds), grpc.WithUserAgent("DrixStatus-Agent/1.0"))
	if err != nil {
		return fail(err)
	}
	defer conn.Close()

	start := time.Now()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: c.Service})
	lat := ms(time.Since(start))
	if err != nil {
		st, _ := status.FromError(err)
		switch st.Code() {
		case codes.Unimplemented:
			return protocol.Result{OK: false, LatencyMS: lat, Message: "server doesn't implement grpc.health.v1"}
		case codes.NotFound:
			return protocol.Result{OK: false, LatencyMS: lat, Message: fmt.Sprintf("unknown service %q", c.Service)}
		case codes.DeadlineExceeded:
			return protocol.Result{OK: false, LatencyMS: lat, Message: "timed out"}
		default:
			return protocol.Result{OK: false, LatencyMS: lat, Message: strings.ToLower(st.Code().String()) + ": " + st.Message()}
		}
	}
	s := resp.GetStatus()
	return protocol.Result{OK: s == healthpb.HealthCheckResponse_SERVING, LatencyMS: lat, Message: strings.ToLower(s.String())}
}
