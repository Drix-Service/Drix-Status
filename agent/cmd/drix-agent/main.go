// drix-agent is the Drix Status check worker. It holds no database or UI: it
// connects to the main server, receives the monitors it owns, runs them and
// streams results back. Run as many as you like; the server rebalances work
// automatically when agents join or leave.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"drix/agent/internal/conn"
	"drix/agent/internal/runner"
)

var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	server := os.Getenv("DRIX_SERVER_URL")
	token := os.Getenv("DRIX_AGENT_TOKEN")
	if server == "" || token == "" {
		log.Error("DRIX_SERVER_URL and DRIX_AGENT_TOKEN are required")
		os.Exit(2)
	}
	concurrency, _ := strconv.Atoi(os.Getenv("DRIX_MAX_CONCURRENCY"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, results := conn.New(conn.Config{ServerURL: server, Token: token, Version: version}, log)
	r := runner.New(ctx, results, concurrency, log)
	client.SetRunner(r)

	log.Info("drix-agent starting", "version", version, "server", server)
	if err := client.Run(ctx); err != nil {
		log.Error("agent stopped", "err", err)
		os.Exit(1)
	}
	r.Stop()
	log.Info("drix-agent stopped")
}
