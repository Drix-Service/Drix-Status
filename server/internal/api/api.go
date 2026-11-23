// Package api exposes the public status API, the admin API and the agent
// gateway endpoint over a single net/http server.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"drix/server/internal/autoinc"
	"drix/server/internal/backup"
	"drix/server/internal/catalog"
	"drix/server/internal/cluster"
	"drix/server/internal/config"
	"drix/server/internal/gateway"
	"drix/server/internal/notify"
	"drix/server/internal/pipeline"
	"drix/server/internal/snapshot"
	"drix/server/internal/store"
)

// Deps bundles everything handlers need.
type Deps struct {
	Cfg      config.Config
	DB       *pgxpool.Pool
	Store    *store.Store
	Redis    *redis.Client
	Catalog  *catalog.Catalog
	Registry *cluster.Registry
	Gateway  *gateway.Gateway
	Snapshot *snapshot.Builder
	Pipeline *pipeline.Pipeline
	Hub      *Hub
	Mailer   Mailer           // nil until email is configured
	Notifier IncidentNotifier // nil until notifications are configured
	Notify   *notify.Notifier
	Backups  *backup.Service
	AutoInc  *autoinc.Runner
	Log      *slog.Logger
	Version  string
}

// Mailer sends transactional email. Implemented by the notify package.
type Mailer interface {
	SendInvite(ctx context.Context, to string, from store.User, role, link string) error
}

type API struct{ Deps }

// Handler builds the HTTP handler tree.
func New(d Deps) http.Handler {
	a := &API{d}
	mux := http.NewServeMux()

	// Health.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	// Agents.
	mux.Handle("GET /api/agent/connect", a.Gateway)

	// Heartbeat (push) monitors.
	mux.HandleFunc("GET /api/push/{token}", a.push)
	mux.HandleFunc("POST /api/push/{token}", a.push)
	mux.HandleFunc("HEAD /api/push/{token}", a.push)

	// Public: the single status page.
	// The page behind an alert's "Acknowledge" button (signed link, no sign-in needed).
	mux.HandleFunc("GET /api/ack/{token}", a.getAckPage)
	mux.HandleFunc("POST /api/ack/{token}", a.postAckPage)

	mux.HandleFunc("GET /api/public/status", a.publicStatus)
	mux.HandleFunc("GET /api/public/stream", a.publicStream)
	mux.HandleFunc("GET /api/public/monitors/{id}/latency", a.publicLatency)
	mux.HandleFunc("GET /api/public/monitors/{id}/locations", a.publicLocations)
	mux.HandleFunc("GET /api/public/incidents/{incident}", a.publicIncident)
	mux.HandleFunc("GET /api/public/history", a.publicHistory)

	// Subscriptions and feeds.
	mux.HandleFunc("POST /api/public/subscribe", a.subscribe)
	mux.HandleFunc("GET /api/public/subscriptions/confirm", a.confirmSubscription)
	mux.HandleFunc("GET /api/public/subscriptions/unsubscribe", a.unsubscribe)
	mux.HandleFunc("POST /api/public/subscriptions/unsubscribe", a.unsubscribe)
	mux.HandleFunc("GET /api/public/feed.atom", a.feed)
	mux.HandleFunc("GET /api/public/feed.rss", a.feed)

	// Admin auth. Roles: viewer (read) < editor < admin < owner.
	mux.HandleFunc("POST /api/admin/auth/login", a.login)
	mux.HandleFunc("POST /api/admin/auth/logout", a.logout)
	mux.Handle("GET /api/admin/auth/me", a.accessFull(store.RoleViewer, true, true, a.me))
	// Two-factor sign-in: the second step, and passwordless passkey sign-in.
	mux.HandleFunc("POST /api/admin/auth/login/2fa", a.loginTwoFactor)
	mux.HandleFunc("POST /api/admin/auth/login/2fa/passkey", a.loginPasskeyBegin)
	mux.HandleFunc("POST /api/admin/auth/login/2fa/passkey/finish", a.loginPasskeyFinish)
	mux.HandleFunc("POST /api/admin/auth/passkey", a.passkeyLoginBegin)
	mux.HandleFunc("POST /api/admin/auth/passkey/finish", a.passkeyLoginFinish)
	// Your own two-factor settings (open while setup is required).
	mux.Handle("GET /api/admin/auth/two-factor", a.setupOK(a.getTwoFactor))
	mux.Handle("POST /api/admin/auth/two-factor/totp/setup", a.setupOK(a.totpSetup))
	mux.Handle("POST /api/admin/auth/two-factor/totp/enable", a.setupOK(a.totpEnable))
	mux.Handle("POST /api/admin/auth/two-factor/totp/disable", a.setupOK(a.totpDisable))
	mux.Handle("POST /api/admin/auth/two-factor/recovery-codes", a.setupOK(a.regenerateRecoveryCodes))
	mux.Handle("POST /api/admin/auth/passkeys/register", a.setupOK(a.passkeyRegisterBegin))
	mux.Handle("POST /api/admin/auth/passkeys/register/finish", a.setupOK(a.passkeyRegisterFinish))
	mux.Handle("PUT /api/admin/auth/passkeys/{id}", a.setupOK(a.renamePasskey))
	mux.Handle("DELETE /api/admin/auth/passkeys/{id}", a.setupOK(a.deletePasskey))
	mux.Handle("PUT /api/admin/auth/password", a.session(store.RoleViewer, a.changePassword))
	mux.HandleFunc("GET /api/admin/invites/accept", a.inviteInfo)
	mux.HandleFunc("POST /api/admin/invites/accept", a.acceptInvite)

	const viewer, editor, admin = store.RoleViewer, store.RoleEditor, store.RoleAdmin

	// Read access for everyone signed in.
	mux.Handle("GET /api/admin/overview", a.role(viewer, a.overview))
	mux.Handle("GET /api/admin/stream", a.role(viewer, a.Hub.ServeAdmin))

	mux.Handle("GET /api/admin/groups", a.role(viewer, a.getGroups))
	mux.Handle("PUT /api/admin/groups", a.role(editor, a.saveGroups))
	mux.Handle("GET /api/admin/branding", a.role(viewer, a.getBranding))
	mux.Handle("PUT /api/admin/branding", a.role(admin, a.putBranding))
	mux.Handle("GET /api/admin/auto-incidents", a.role(viewer, a.getAutoIncidents))
	mux.Handle("PUT /api/admin/auto-incidents", a.role(admin, a.putAutoIncidents))
	mux.Handle("GET /api/admin/subscribers", a.role(editor, a.listSubscribers))
	mux.Handle("GET /api/admin/subscribers.csv", a.role(editor, a.listSubscribers))
	mux.Handle("DELETE /api/admin/subscribers/{id}", a.role(editor, a.deleteSubscriber))

	mux.Handle("GET /api/admin/monitors", a.role(viewer, a.listMonitors))
	mux.Handle("POST /api/admin/monitors", a.role(editor, a.createMonitor))
	mux.Handle("POST /api/admin/monitors/test", a.role(editor, a.testMonitor))
	mux.Handle("POST /api/admin/monitors/bulk", a.role(editor, a.bulkMonitors))
	mux.Handle("POST /api/admin/monitors/import", a.role(editor, a.importMonitors))
	mux.Handle("GET /api/admin/monitors.csv", a.role(viewer, a.exportMonitors))
	mux.Handle("GET /api/admin/monitors/{id}", a.role(viewer, a.getMonitor))
	mux.Handle("PUT /api/admin/monitors/{id}", a.role(editor, a.updateMonitor))
	mux.Handle("DELETE /api/admin/monitors/{id}", a.role(editor, a.deleteMonitor))
	mux.Handle("POST /api/admin/monitors/{id}/push-token", a.role(editor, a.rotatePushToken))
	mux.Handle("GET /api/admin/monitors/{id}/results", a.role(viewer, a.monitorResults))
	mux.Handle("GET /api/admin/monitors/{id}/latency", a.role(viewer, a.adminLatency))
	mux.Handle("GET /api/admin/monitors/{id}/locations", a.role(viewer, a.adminLocations))
	mux.Handle("POST /api/admin/monitors/{id}/ack", a.role(editor, a.ackMonitor))

	mux.Handle("GET /api/admin/agents", a.role(viewer, a.listAgents))
	mux.Handle("POST /api/admin/agents", a.role(admin, a.createAgent))
	mux.Handle("PUT /api/admin/agents/{id}", a.role(admin, a.updateAgent))
	mux.Handle("POST /api/admin/agents/{id}/rotate", a.role(admin, a.rotateAgent))
	mux.Handle("DELETE /api/admin/agents/{id}", a.role(admin, a.deleteAgent))

	mux.Handle("GET /api/admin/events", a.role(viewer, a.adminEvents))
	mux.Handle("GET /api/admin/settings", a.role(viewer, a.getSettings))
	mux.Handle("PUT /api/admin/settings", a.role(admin, a.putSettings))

	// Incidents & maintenance.
	mux.Handle("GET /api/admin/incidents", a.role(viewer, a.listIncidents))
	mux.Handle("POST /api/admin/incidents", a.role(editor, a.createIncident))
	mux.Handle("GET /api/admin/incidents/{id}", a.role(viewer, a.getIncident))
	mux.Handle("PUT /api/admin/incidents/{id}", a.role(editor, a.updateIncident))
	mux.Handle("POST /api/admin/incidents/{id}/updates", a.role(editor, a.postIncidentUpdate))
	mux.Handle("DELETE /api/admin/incidents/{id}", a.role(admin, a.deleteIncident))

	// Alerts & email.
	mux.Handle("GET /api/admin/channels", a.role(admin, a.listChannels))
	mux.Handle("POST /api/admin/channels", a.role(admin, a.createChannel))
	mux.Handle("PUT /api/admin/channels/{id}", a.role(admin, a.updateChannel))
	mux.Handle("DELETE /api/admin/channels/{id}", a.role(admin, a.deleteChannel))
	mux.Handle("POST /api/admin/channels/{id}/test", a.role(admin, a.testChannel))
	mux.Handle("GET /api/admin/deliveries", a.role(admin, a.listDeliveries))
	mux.Handle("GET /api/admin/settings/email", a.role(admin, a.getEmailSettings))
	mux.Handle("PUT /api/admin/settings/email", a.role(admin, a.putEmailSettings))
	mux.Handle("POST /api/admin/settings/email/test", a.role(admin, a.testEmail))

	// Push notifications for the Drix app (per user and device).
	mux.Handle("GET /api/admin/push", a.role(viewer, a.pushInfo))
	mux.Handle("POST /api/admin/push/subscribe", a.role(viewer, a.pushSubscribe))
	mux.Handle("PUT /api/admin/push/subscriptions/{id}", a.role(viewer, a.pushUpdate))
	mux.Handle("DELETE /api/admin/push/subscriptions/{id}", a.role(viewer, a.pushDelete))
	mux.Handle("POST /api/admin/push/subscriptions/{id}/test", a.role(viewer, a.pushTest))

	// Team.
	mux.Handle("GET /api/admin/users", a.role(viewer, a.listUsers))
	mux.Handle("PUT /api/admin/users/{id}", a.session(admin, a.updateUser))
	mux.Handle("DELETE /api/admin/users/{id}", a.session(admin, a.deleteUser))
	mux.Handle("DELETE /api/admin/users/{id}/two-factor", a.session(store.RoleOwner, a.resetTwoFactor))
	mux.Handle("GET /api/admin/security", a.role(viewer, a.getSecurity))
	mux.Handle("PUT /api/admin/security", a.session(store.RoleOwner, a.putSecurity))
	mux.Handle("POST /api/admin/invites", a.session(admin, a.createInvite))
	mux.Handle("DELETE /api/admin/invites/{id}", a.session(admin, a.deleteInvite))

	// API keys (managing them always needs a person signed in).
	mux.Handle("GET /api/admin/api-keys", a.session(admin, a.listAPIKeys))
	mux.Handle("POST /api/admin/api-keys", a.session(admin, a.createAPIKey))
	mux.Handle("DELETE /api/admin/api-keys/{id}", a.session(admin, a.deleteAPIKey))
	mux.Handle("GET /api/admin/audit", a.role(admin, a.listAudit))

	// Backups: settings for admins; running and downloading need a signed-in
	// owner (a backup holds every secret, so API keys can't fetch one).
	mux.Handle("GET /api/admin/backups", a.role(admin, a.backupInfo))
	mux.Handle("PUT /api/admin/backups/settings", a.session(admin, a.putBackupSettings))
	mux.Handle("POST /api/admin/backups/test-s3", a.session(admin, a.testBackupS3))
	mux.Handle("POST /api/admin/backups/run", a.session(store.RoleOwner, a.runBackup))
	mux.Handle("GET /api/admin/backups/files/{name}", a.session(store.RoleOwner, a.downloadBackup))

	// Optional: serve built frontends (single-binary deployments).
	if d.Cfg.StaticAdminDir != "" {
		mux.Handle("/admin/", http.StripPrefix("/admin", spa(d.Cfg.StaticAdminDir)))
	}
	if d.Cfg.StaticStatusDir != "" {
		mux.Handle("/", spa(d.Cfg.StaticStatusDir))
	}

	return recoverer(d.Log, securityHeaders(csrf(mux)))
}

// ---------- helpers ----------

type apiError struct {
	Error string            `json:"error"`
	Code  string            `json:"code,omitempty"`
	Field map[string]string `json:"fields,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, apiError{Error: msg})
}

func (a *API) serverErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	a.Log.Error("request failed", "err", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func queryInt(r *http.Request, key string, def, lo, hi int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	return max(lo, min(hi, n))
}

func rangeParams(rng string) (time.Duration, time.Duration, string) {
	switch rng {
	case "1h":
		return time.Hour, time.Minute, "1h"
	case "7d":
		return 7 * 24 * time.Hour, time.Hour, "7d"
	case "30d":
		return 30 * 24 * time.Hour, 4 * time.Hour, "30d"
	default:
		return 24 * time.Hour, 10 * time.Minute, "24h"
	}
}

// ---------- middleware ----------

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic", "err", rec, "path", r.URL.Path)
				writeErr(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if strings.HasPrefix(r.URL.Path, "/api/admin") {
			h.Set("Cache-Control", "no-store")
			h.Set("X-Frame-Options", "DENY")
		}
		if strings.HasPrefix(r.URL.Path, "/api/public") {
			// Public data may be embedded anywhere.
			h.Set("Access-Control-Allow-Origin", "*")
		}
		next.ServeHTTP(w, r)
	})
}

// csrf rejects cross-site state-changing admin requests. Admin calls must be
// JSON (which browsers can't send cross-origin without a preflight we never
// approve) and, when an Origin header is present, it must match the Host.
func csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/admin") && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				host := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
				fwd := r.Header.Get("X-Forwarded-Host")
				if host != r.Host && host != fwd {
					writeErr(w, http.StatusForbidden, "cross-origin request rejected")
					return
				}
			}
			if r.ContentLength > 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeErr(w, http.StatusUnsupportedMediaType, "expected application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// spa serves a built single-page app with index.html fallback.
func spa(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fs.ServeHTTP(w, r)
	})
}
