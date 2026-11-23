package checks

import (
	"context"
	"fmt"
	"runtime"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"drix/shared/protocol"
)

// Ping sends ICMP echo requests. It tries privileged raw sockets first (Linux
// with CAP_NET_RAW, root), then falls back to unprivileged UDP-ICMP which
// works on macOS and Linux with net.ipv4.ping_group_range configured.
func Ping(ctx context.Context, target string, c protocol.PingConfig) protocol.Result {
	count := c.Count
	if count <= 0 {
		count = 3
	}

	run := func(privileged bool) (*probing.Statistics, error) {
		p, err := probing.NewPinger(target)
		if err != nil {
			return nil, err
		}
		p.Count = count
		p.Interval = 200 * time.Millisecond
		if dl, ok := ctx.Deadline(); ok {
			p.Timeout = time.Until(dl)
		}
		p.SetPrivileged(privileged)
		if err := p.RunWithContext(ctx); err != nil {
			return nil, err
		}
		return p.Statistics(), nil
	}

	stats, err := run(runtime.GOOS == "linux" || runtime.GOOS == "windows")
	if err != nil && runtime.GOOS == "linux" {
		stats, err = run(false)
	}
	if err != nil {
		return protocol.Result{OK: false, Message: timeoutMessage(ctx, err)}
	}
	if stats.PacketsRecv == 0 {
		return protocol.Result{OK: false, Message: fmt.Sprintf("100%% packet loss (%d sent)", stats.PacketsSent)}
	}
	r := protocol.Result{
		OK:        true,
		LatencyMS: ms(stats.AvgRtt),
		Message:   fmt.Sprintf("%d/%d replies, %.0f%% loss", stats.PacketsRecv, stats.PacketsSent, stats.PacketLoss),
	}
	return r
}
