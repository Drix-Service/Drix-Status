// drix-server is the Drix Status main system: the public status API, the
// admin API, and the gateway that check agents connect to. Run one or more
// instances behind a load balancer; they coordinate through Redis.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"drix/server/internal/api"
	"drix/server/internal/autoinc"
	"drix/server/internal/backup"
	"drix/server/internal/catalog"
	"drix/server/internal/cluster"
	"drix/server/internal/config"
	"drix/server/internal/db"
	"drix/server/internal/gateway"
	"drix/server/internal/jobs"
	"drix/server/internal/notify"
	"drix/server/internal/pipeline"
	"drix/server/internal/snapshot"
	"drix/server/internal/store"
)

var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load()
	log = log.With("server", cfg.ServerID)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, log); err != nil {
		return err
	}

	ropt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return err
	}
	ropt.PoolSize = 64
	rdb := redis.NewClient(ropt)
	defer rdb.Close()
	if err := waitRedis(ctx, rdb, log); err != nil {
		return err
	}

	st := store.New(pool)
	if created, err := api.EnsureOwner(ctx, st, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		log.Warn("owner bootstrap", "err", err)
	} else if created {
		log.Info("created first owner account", "email", cfg.AdminEmail)
	}

	cat := catalog.New(st, rdb, log)
	if err := cat.Start(ctx); err != nil {
		return err
	}
	reg := cluster.NewRegistry(rdb)
	leader := cluster.NewLeader(rdb, cfg.ServerID, log)
	pipe := pipeline.New(rdb, st, cat, log)
	gw := gateway.New(st, rdb, cat, reg, pipe, cfg.ServerID, log)
	snap := snapshot.NewBuilder(rdb, st, cat, log)
	hub := api.NewHub(rdb, cat, log)
	notifier := notify.New(st, rdb, cat, notify.Options{SecretKey: cfg.SecretKey, AdminURL: cfg.AdminURL,
		StatusURL: cfg.StatusURL, SMTPURL: cfg.SMTPURL, ServerID: cfg.ServerID}, log)
	if cfg.SecretKey == "" {
		log.Warn("SECRET_KEY is not set: alert channels and email passwords can't be stored")
	}
	pipe.OnChange = notifier.MonitorChanges
	backups := backup.New(st, rdb, notifier.Box(), cfg.BackupDir, cfg.DatabaseURL, log)
	backups.OnFailure = notifier.BackupFailed
	if why := backups.Unavailable(); why != "" {
		log.Info("database backups are off", "reason", why)
	}

	// Background loops.
	go leader.Run(ctx)
	go pipe.Run(ctx)
	go gw.Run(ctx)
	go hub.Run(ctx)
	go snap.Run(ctx, leader.IsLeader)
	go maintenance(ctx, leader, st, reg, cat, log)
	go jobs.Heartbeats(ctx, rdb, cat, pipe, leader.IsLeader, log)
	go jobs.Maintenance(ctx, st, rdb, leader.IsLeader, notifier, log)
	go notifier.Run(ctx)
	go notifier.Reminders(ctx, leader.IsLeader)
	go notifier.Escalations(ctx, leader.IsLeader)
	go notifier.CertWarnings(ctx, leader.IsLeader)
	go backups.Schedule(ctx, leader.IsLeader)
	autoInc := autoinc.New(st, rdb, cat, log)
	autoInc.OnIncident = notifier.OnIncident
	go autoInc.Run(ctx, leader.IsLeader)

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: api.New(api.Deps{
			Cfg: cfg, DB: pool, Store: st, Redis: rdb, Catalog: cat, Registry: reg,
			Gateway: gw, Snapshot: snap, Pipeline: pipe, Hub: hub, Log: log, Version: version,
			Notify: notifier, Mailer: notifier, Notifier: notifier, Backups: backups, AutoInc: autoInc,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("drix-server listening", "addr", cfg.HTTPAddr, "version", version)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	time.Sleep(200 * time.Millisecond) // let pipeline flush on ctx cancel
	return nil
}

func waitRedis(ctx context.Context, rdb *redis.Client, log *slog.Logger) error {
	for i := 1; ; i++ {
		err := rdb.Ping(ctx).Err()
		if err == nil {
			return nil
		}
		if i >= 30 {
			return err
		}
		log.Warn("waiting for redis", "attempt", i, "err", err)
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// maintenance runs cluster-wide housekeeping on the leader only.
func maintenance(ctx context.Context, leader *cluster.Leader, st *store.Store, reg *cluster.Registry, cat *catalog.Catalog, log *slog.Logger) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !leader.IsLeader() {
			continue
		}
		reg.Prune(ctx)
		if n, err := st.PruneUnconfirmed(ctx); err == nil && n > 0 {
			log.Info("removed unconfirmed subscriptions", "count", n)
		}
		if time.Since(lastPrune) > time.Hour {
			lastPrune = time.Now()
			days := cat.Get().Settings.RetentionDays
			if days < 1 {
				days = 30
			}
			n, err := st.PruneResults(ctx, time.Now().AddDate(0, 0, -days))
			if err != nil {
				log.Error("retention prune failed", "err", err)
			} else if n > 0 {
				log.Info("pruned old results", "rows", n, "retention_days", days)
			}
		}
	}
}
