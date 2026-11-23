package checks

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"drix/shared/protocol"
)

// TCP opens a TCP connection to target (host or host:port).
func TCP(ctx context.Context, target string, c protocol.TCPConfig) protocol.Result {
	addr := target
	if c.Port > 0 {
		host := target
		if h, _, err := net.SplitHostPort(target); err == nil {
			host = h
		}
		addr = net.JoinHostPort(host, strconv.Itoa(c.Port))
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fail(fmt.Errorf("target must be host:port or set a port"))
	}

	var d net.Dialer
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	lat := ms(time.Since(start))
	if err != nil {
		return protocol.Result{OK: false, LatencyMS: lat, Message: timeoutMessage(ctx, err)}
	}
	_ = conn.Close()
	return protocol.Result{OK: true, LatencyMS: lat, Message: "connected to " + addr}
}
