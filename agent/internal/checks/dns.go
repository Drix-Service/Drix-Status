package checks

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"

	"drix/shared/protocol"
)

var recordTypes = map[string]uint16{
	"A":     dns.TypeA,
	"AAAA":  dns.TypeAAAA,
	"CNAME": dns.TypeCNAME,
	"MX":    dns.TypeMX,
	"TXT":   dns.TypeTXT,
	"NS":    dns.TypeNS,
}

// DNS resolves target against a specific resolver and optionally checks
// that an expected value is among the answers.
func DNS(ctx context.Context, target string, c protocol.DNSConfig) protocol.Result {
	rt := strings.ToUpper(c.RecordType)
	if rt == "" {
		rt = "A"
	}
	qtype, ok := recordTypes[rt]
	if !ok {
		return fail(fmt.Errorf("unsupported record type %q", rt))
	}
	resolver := c.Resolver
	if resolver == "" {
		resolver = "1.1.1.1:53"
	}
	if _, _, err := net.SplitHostPort(resolver); err != nil {
		resolver = net.JoinHostPort(resolver, "53")
	}

	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(target), qtype)
	msg.RecursionDesired = true

	client := &dns.Client{}
	if dl, ok := ctx.Deadline(); ok {
		client.Timeout = time.Until(dl)
	}
	start := time.Now()
	resp, rtt, err := client.ExchangeContext(ctx, msg, resolver)
	if err != nil {
		return protocol.Result{OK: false, LatencyMS: ms(time.Since(start)), Message: timeoutMessage(ctx, err)}
	}
	lat := ms(rtt)
	if resp.Rcode != dns.RcodeSuccess {
		return protocol.Result{OK: false, LatencyMS: lat, Message: "rcode " + dns.RcodeToString[resp.Rcode]}
	}

	answers := make([]string, 0, len(resp.Answer))
	for _, rr := range resp.Answer {
		switch v := rr.(type) {
		case *dns.A:
			answers = append(answers, v.A.String())
		case *dns.AAAA:
			answers = append(answers, v.AAAA.String())
		case *dns.CNAME:
			answers = append(answers, strings.TrimSuffix(v.Target, "."))
		case *dns.MX:
			answers = append(answers, strings.TrimSuffix(v.Mx, "."))
		case *dns.TXT:
			answers = append(answers, strings.Join(v.Txt, ""))
		case *dns.NS:
			answers = append(answers, strings.TrimSuffix(v.Ns, "."))
		}
	}
	if len(answers) == 0 {
		return protocol.Result{OK: false, LatencyMS: lat, Message: "no " + rt + " records"}
	}
	if c.Expect != "" {
		want := strings.TrimSuffix(strings.ToLower(c.Expect), ".")
		found := false
		for _, a := range answers {
			if strings.ToLower(a) == want || strings.Contains(strings.ToLower(a), want) {
				found = true
				break
			}
		}
		if !found {
			return protocol.Result{OK: false, LatencyMS: lat, Message: fmt.Sprintf("expected %q, got %s", c.Expect, strings.Join(answers, ", "))}
		}
	}
	return protocol.Result{OK: true, LatencyMS: lat, Message: strings.Join(answers, ", ")}
}
