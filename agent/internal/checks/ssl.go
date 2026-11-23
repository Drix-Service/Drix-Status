package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math"
	"net"
	"time"

	"drix/shared/protocol"
)

// sslRoots overrides the trusted roots (tests only); nil means the system pool.
var sslRoots *x509.CertPool

// SSL connects with TLS, verifies the certificate chain and hostname, and
// reports how long the certificate stays valid. It goes degraded below
// warn_days and down below crit_days or when the certificate is invalid.
func SSL(ctx context.Context, target string, c protocol.SSLConfig) protocol.Result {
	warn, crit := c.WarnDays, c.CritDays
	if warn <= 0 {
		warn = 14
	}
	if crit <= 0 {
		crit = 7
	}
	addr := target
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(target, "443")
	}
	host, _, _ := net.SplitHostPort(addr)
	serverName := c.ServerName
	if serverName == "" {
		serverName = host
	}

	// Handshake without verification so we can report *why* a cert is bad.
	d := &tls.Dialer{Config: &tls.Config{ServerName: serverName, InsecureSkipVerify: true}} //nolint:gosec // verified manually below
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	lat := ms(time.Since(start))
	if err != nil {
		return protocol.Result{OK: false, LatencyMS: lat, Message: timeoutMessage(ctx, err)}
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return protocol.Result{OK: false, LatencyMS: lat, Message: "server sent no certificate"}
	}
	leaf := certs[0]
	exp := leaf.NotAfter.UTC()
	r := protocol.Result{LatencyMS: lat, CertExpiry: &exp}

	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: serverName, Intermediates: inter, Roots: sslRoots}); err != nil {
		r.Message = "certificate invalid: " + err.Error()
		return r
	}

	days := int(math.Floor(time.Until(exp).Hours() / 24))
	switch {
	case days < 0:
		r.Message = fmt.Sprintf("certificate expired %d days ago", -days)
	case days < crit:
		r.Message = fmt.Sprintf("certificate expires in %d days (%s)", days, exp.Format("2 Jan 2006"))
	case days < warn:
		r.OK, r.Degraded = true, true
		r.Message = fmt.Sprintf("certificate expires in %d days (%s)", days, exp.Format("2 Jan 2006"))
	default:
		r.OK = true
		r.Message = fmt.Sprintf("valid for %d days, issued by %s", days, issuer(leaf))
	}
	return r
}

func issuer(c *x509.Certificate) string {
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return c.Issuer.CommonName
}
