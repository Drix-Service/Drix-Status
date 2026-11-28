# Drix Status

A fast, self-hosted status page with a separate admin panel and a distributed checker. You can add as many check workers as you like. They split the work automatically, and if one dies its checks move to the others.

```
                    ┌──────────────────────────────┐
  Status page  ───▶ │                              │ ◀─── Admin panel
  (React, :3000)    │  drix-server  (Go)           │      (React, :3001)
                    │  • public API (1 Redis GET)  │
                    │  • admin API + SSE live feed │
                    │  • agent gateway (WebSocket) │
                    └──────┬──────────────┬────────┘
                           │              │
                    PostgreSQL         Redis
                    (source of truth)  (live state, cache, pub/sub,
                                        membership, leader lock)
                           ▲
          WebSocket, outbound from each agent (agents need no open ports)
        ┌──────────────┬───┴──────────┬──────────────┐
   drix-agent     drix-agent     drix-agent     …  (any number, any network)
```

| Part | Path | What it is |
|---|---|---|
| Main system | `server/` | API, both sites' backend, and the agent gateway. Run one or several instances; they coordinate through Redis. |
| Worker | `agent/` | A small static binary with **no database, Redis or UI**. It connects to the main system, receives its monitors, runs them and streams results back. |
| Protocol | `shared/` | Wire types used by both, so the contract can't drift. |
| Websites | `web/apps/status`, `web/apps/admin` | Two separate React apps that share one UI kit (`web/packages/ui`). |

## Quick start (development)

Requirements: Go 1.26+, Node 22+, pnpm, Docker.

The quickest way is `./start.sh`. It starts Postgres, Redis and Mailpit, the server, 5 test workers and both websites, and stops everything on Ctrl+C. It also generates a `SECRET_KEY` in `.env` on first run and sends all email to Mailpit (inbox at http://localhost:8025). `./start.sh --help` lists the options.

Or step by step:

```bash
cp .env.example .env          # set ADMIN_EMAIL / ADMIN_PASSWORD and SECRET_KEY
make infra                    # Postgres, Redis and Mailpit in Docker
make server                   # main server on :8080 (applies migrations, creates the admin)
make web-install
make status                   # status page   → http://localhost:3000
make admin                    # admin panel   → http://localhost:3001
```

Then open the admin panel and go to **Workers → Add worker**. Copy the token and start an agent:

```bash
make agent DRIX_AGENT_TOKEN=drx_...
```

Start more agents with other tokens, in other terminals or on other machines, and watch the **Workers** page rebalance.

## How work is split between workers

- **Membership:** each connected agent is registered in Redis with a 15s TTL, refreshed by heartbeats every 5s.
- **Ownership:** every monitor belongs to exactly one live agent, and the split is exactly even. With M monitors and N agents, every agent gets ⌊M/N⌋ or ⌈M/N⌉, so 8 monitors on 4 workers is 2 each and 25 on 5 is 5 each. It uses rendezvous hashing with capacities (`server/internal/cluster/hrw.go`). Every server instance computes the same answer on its own, so there's no central scheduler. When an agent joins or leaves, almost only its share moves: removing 1 of 5 agents with 1,000 monitors moves its 200 plus 7 to re-level.
- **Failover:**
  - A clean disconnect or crash that closes the socket is handled immediately.
  - A frozen or partitioned agent is handled after its TTL (≈15s).
  - Both paths were measured end to end in development.
- **No double checks:** checks run at a deterministic phase per monitor (`shared/protocol/slot.go`), and the server drops any second result for the same slot with a Redis `SET NX`. Overlaps during rebalancing, and results an agent buffered while offline, never double count.
- **Offline buffering:** agents keep checking during short disconnects and flush buffered results when they reconnect.

### Checking from every location

A worker's **region** (Workers page) is its location; a worker without one counts as its own location. Turn on **Check from every location** on a monitor and one worker in each location checks it, every interval. Several workers in one region share that location's work, so a monitor is still checked once per location.

- **Status:** each location keeps its own state and retries. The monitor is **down** when more than half of the locations that reported recently are down, and **degraded** when any of them is down or slow, with messages like "Down from Frankfurt (1 of 3): HTTP 503". A location whose worker went away stops counting after three missed intervals.
- **Alerts and uptime** follow that overall status, so one location's network trouble doesn't wake anyone up.
- **Where you see it:** a Locations table on the monitor page (status, response time now and averaged over 24h, uptime per location), the phone app, and optionally "Checked from" on the status page.

## Why it's fast

- **Go everywhere on the backend:** stdlib HTTP, `pgx`, `go-redis`.
- **Public status from the cache:** the status payload is precomputed (JSON and gzip) by the cluster leader at most once a second, and served with a single Redis `GET`. It measures about 1.5 ms locally, with ETag support.
- **Fast result handling:** each result is applied with one atomic Lua script (dedupe, retry logic, state transition, sparkline). Writes to Postgres are batched every second with `COPY`.
- **Cheap uptime bars:** they come from pre-aggregated `daily_stats` and never scan raw rows.
- **Live updates:** pushed over Server-Sent Events, with one Redis subscription per server instance however many viewers there are.
- **Small first load:** the status page loads charts lazily, so its initial JS is about 115 KB gzipped.

## Check types

| Type | Checks |
|---|---|
| HTTP(S) | Method, expected status range, must or must-not contain text, headers, body, redirects, TLS errors. The TLS expiry date is recorded. |
| TCP | Port is open (`host:port`). |
| Ping | ICMP echo with packet loss. Falls back to unprivileged ICMP. |
| DNS | A / AAAA / CNAME / MX / TXT / NS against any resolver, with an optional expected value. |
| SSL certificate | Valid chain and hostname. Degraded below *warn days* (14), down below *critical days* (7) or when invalid. |
| JSON API | HTTP request plus assertions on the JSON body, e.g. `data.items.0.status equals ok`, `queue.depth less than 100`. |
| SMTP | Mail server greeting (220), EHLO, optional STARTTLS with certificate check. |
| gRPC | `grpc.health.v1` check, optionally for one service, with or without TLS. |
| Heartbeat (push) | Your job calls its URL; no call within *interval + grace* means down. See below. |

Each monitor has an interval, timeout, **retries** (failures before it's marked down) and a **slow threshold** (marks it degraded). Whether it's public depends on the status pages it's placed on.

### Heartbeat URLs

Every heartbeat monitor gets a secret URL. Call it when the job finishes:

```bash
0 3 * * * /usr/local/bin/backup.sh && curl -fsS -m 10 https://drix.example.com/api/push/<token>
curl -fsS "https://drix.example.com/api/push/<token>?status=down&msg=disk%20full"   # report a failure
curl -fsS "https://drix.example.com/api/push/<token>?ping=1840"                     # report how long it took (ms)
```

### Importing and exporting monitors

**Monitors → Import CSV** adds many monitors at once. The dialog has a template; after you choose a file it shows a preview, and nothing is created until you press Import. A file with any bad row imports nothing, and rows whose name and target already exist are skipped, so importing the same file twice is safe. **Export CSV** writes every monitor in the same format, ordered like the status page, so you can copy a setup from one Drix to another.

- **Columns:** `name`, `type` and `target` (empty for heartbeats), then optionally `group` (created if missing; empty = private), `description`, `interval_s`, `timeout_ms`, `retries`, `degraded_ms`, `enabled`, `multi_location`, `show_locations`, `auto_incident`.
- **Type options:** `method`, `expect_status` (`200` or `200-299`), `keyword`, `keyword_absent`, `headers` (`Name: value; Name: value`), `body`, `follow_redirects`, `ignore_tls`, `assertions` (`status equals ok; db exists`), `port`, `record_type`, `resolver`, `expect`, `warn_days`, `crit_days`, `server_name`, `starttls`, `service`, `tls`, `grace_s`, `count`.
- **Format:** comma or semicolon separated, header row first, columns in any order. Up to 500 monitors per file. Heartbeat monitors get a new push URL when imported.

## Status page and groups

There is one public status page. Under **Groups** in the admin panel you arrange monitors into sections such as "Website & API" or "EU servers".

- Drag monitors between groups; a monitor can be in more than one group.
- Give a monitor a different public name.
- Collapse groups by default.

**Settings → Status page** holds the name, logo, accent colour and footer. The page shows open incidents, upcoming maintenance, a 90-day uptime history, an incident history page and RSS/Atom feeds (`/api/public/feed.atom`, `/api/public/feed.rss`).

To serve it on your own domain (for example `status.example.com`), point the domain at the status web container or proxy.

### Languages

The status page is translated into English, Spanish, French, German, Portuguese, Italian, Dutch, Polish, Danish, Swedish, Norwegian, Finnish, Japanese, Chinese (Simplified) and Korean. Visitors get their browser's language and can switch in the footer (remembered in their browser). **Settings → Status page → Default language** is used when a visitor's language isn't available. Text you write yourself (names, groups, incident updates) is shown as written.

## Incidents and maintenance

- **Incidents:** declare one with an impact (minor/major/critical) and the affected monitors, then post updates (investigating → identified → monitoring → resolved). Affected monitors show as degraded or down while it's open, and the day is marked on their uptime bars.
- **Maintenance windows:** they start and finish on schedule. During a window, affected monitors show **Maintenance** and alerts pause. Failed checks are counted separately, so they never lower uptime.


### Automatic incidents

Turn them on under **Settings → Automatic incidents**. When a monitor on the status page has been down for a few minutes (5 by default), Drix posts an incident there by itself: "Problems with *API*" and "We're investigating…", in the status page's default language. Outages at the same time share one incident.

- **Resolving:** it's resolved when everything on it has been back for 2 minutes. If you post an update or change its status, you're in charge and it's left to you.
- **Skipped:** monitors already in an incident someone wrote, in maintenance, or with **Post automatic incidents** turned off in the monitor's settings.
- **Who gets told:** subscribers are emailed only if you turn that on. Alert channels aren't sent these, since they already got the monitor's down alert.

## Alerts

- **Channels:** email, Discord, Slack, Microsoft Teams, Telegram, PagerDuty, Opsgenie, ntfy, Pushover or webhook. PagerDuty and Opsgenie open an alert when a monitor goes down and resolve it when it recovers; their "Send test" opens and resolves one right away. Each channel chooses its events (down, recovered, degraded, incidents & maintenance, certificate expiring), applies to all or selected monitors, and can repeat a "still down" reminder.
- **Certificate expiry:** HTTP, SMTP and gRPC checks record when the target's TLS certificate expires. Channels with "Certificate expiring" are warned 14, 7, 3 and 1 day(s) before, and once it has expired; each warning is sent once per certificate. SSL-type monitors already alert by going degraded and then down.
- **Acknowledge:** down alerts in Discord, Slack, Teams, Telegram, ntfy and email have an **Acknowledge** button. It opens a page that confirms first, because chat apps open links to preview them. You can also acknowledge on the monitor's page, in the Overview's "Needs attention" list and in the phone app.
  - **What it does:** "someone is on it". Reminders and escalations stop for that outage, and the chat channels that got the alert are told who took it.
  - **The link** works for that one outage only.
- **Escalation:** a channel can be set to send down alerts **only if nobody acknowledges the outage within 5, 10, 15, 30 or 60 minutes**. For example: Discord right away, PagerDuty after 15 minutes. It then gets the recovery too, but only for outages that reached it.
- **Delivery:** alerts go through a Redis Stream consumer group, so each one is sent once even with several server instances. Failures are retried up to 5 times, and every result appears in the delivery log.
- **Email:** configure SMTP in **Settings → Email**, or set `SMTP_URL`.
- **Encryption:** channel settings and the SMTP password are encrypted with `SECRET_KEY`.

Webhooks receive the alert as JSON with `X-Drix-Event`, `X-Drix-Delivery` and `X-Drix-Signature: sha256=<hex>`, which is HMAC-SHA256 of the raw body with the channel's signing secret. To verify in Node:

```js
const expected = "sha256=" + crypto.createHmac("sha256", secret).update(rawBody).digest("hex");
const ok = crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(req.headers["x-drix-signature"]));
```

## Subscribers

- **Signing up:** visitors subscribe by email from the **Subscribe** button and confirm through a link (double opt-in). The endpoint answers the same way either way, so it doesn't reveal who is subscribed.
- **What they get:** incidents and maintenance with "Notify subscribers" on are emailed to confirmed subscribers, with one-click unsubscribe headers.
- **Admin:** **Settings → Subscribers** lets you search, remove and export subscribers as CSV.

## Team

Roles: **Owner** (everything), **Admin** (everything except changing owners: team, workers, alerts, settings), **Editor** (monitors, pages, incidents, maintenance, subscribers) and **Viewer** (read-only).

- **Enforcement:** the server enforces roles on every API route.
- **Instant sign-out:** changing someone's role or removing them signs them out immediately.
- **Invites:** invite links are single-use and expire after 7 days.

### Two-factor sign-in

Everyone can turn it on under **Settings → Two-factor sign-in**. Signing in then needs the password plus one of these:

- **Passkeys** use Face ID, Touch ID, Windows Hello or a security key. A passkey also works on its own: **Sign in with a passkey** on the login page skips the password, which is the quickest way in on the iPhone app.
  - Passkeys belong to the admin's domain (`ADMIN_URL`). If that domain changes, add them again.
- **Authenticator app:** 6-digit codes (RFC 6238) from 1Password, Google Authenticator, Authy or the iPhone's Passwords app. Each code works once. The secret is encrypted with `SECRET_KEY`, so the app option needs it set; passkeys don't.
- **Recovery codes:** 10 one-time codes, shown when two-factor is turned on. You can make a new set any time; that needs your password.

**Owner controls (Team page):**
- **Require two-factor sign-in** makes everyone set it up before they can use the admin. API keys aren't affected.
- **Reset two-factor** removes someone's passkeys, app and codes and signs them out, for when they've lost their phone.

Five wrong codes end a sign-in attempt, and the usual login rate limit also applies. Turning a method on or off, using a recovery code and resets all appear in the audit log.
- **Audit log:** **Team → Audit log** records who changed what, and from where.

## Apps for phone and computer

The admin site can be installed as an app. Each device gets the screens made for it:

- **iPhone / Android phone:** open your admin address in Safari (or Chrome), tap **Share → Add to Home Screen**, and open **Drix** from the Home Screen. It opens full screen on the phone screens (`/m`).
- **Mac, PC or iPad:** install it the usual way, and it opens the full admin in its own window with a Dock or taskbar icon.
  - On a Mac: Safari → File → **Add to Dock**.
  - In Chrome or Edge: the **Install** icon in the address bar.

  Notifications for that computer are under **Settings → Notifications on this device**.
- **Links between them:** phone-app links on a computer (for example from a notification) open the matching full-admin page. To try the phone screens on a computer, open `/m?phone`.

The phone screens update live:

- **Status:** what's down or degraded first, then every group with response times.
- **Monitor:** status, response-time chart and recent changes, with **Pause/Resume**, **Maintenance** (starts now for 15 minutes to 4 hours, pausing alerts) and **Incident**.
- **Incidents:** open incidents and maintenance. Post an update, resolve, or end maintenance early.
- **Settings:** turn on notifications for this device, choose which alerts it gets, send a test, change the theme or sign out.

**Opening fast:** the app keeps its files and the last known status on the phone. It opens straight to that saved status, marked "Updating…" until fresh data arrives, or "Offline" if the server can't be reached. After an update to Drix, the new version shows from the next launch. Signing out, or a session ending, clears the saved data.

**Push notifications** use standard Web Push. iOS 16.4 or newer is needed, and the app must be opened from the Home Screen, not Safari. Each person turns them on per device, and alerts pause during maintenance like other channels. The signing keys are created on first use and stored encrypted with `SECRET_KEY`. Android and desktop Chrome, Edge and Firefox work too.

## API keys

Scripts and CI can use the admin API with a key from **Settings → API keys** (admins only). A key acts as the person who made it, with the role chosen for it (viewer, editor or admin, never more than their own), and stops working if that person is removed. Only a hash is stored, so the key is shown once.

```sh
export DRIX_API_KEY=drix_…

# List monitors
curl -H "Authorization: Bearer $DRIX_API_KEY" https://status.example.com/api/admin/monitors

# Pause monitors 1 and 2 during a deploy ("resume" works the same way)
curl -X POST -H "Authorization: Bearer $DRIX_API_KEY" -H "Content-Type: application/json" \
  -d '{"action":"pause","ids":[1,2]}' https://status.example.com/api/admin/monitors/bulk
```

Keys can't manage the team, passwords or other keys. Actions taken with a key show up in the audit log with the key's name.

## Configuration

See `.env.example`. Main server variables:

| Variable | Default | Purpose |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Listen address |
| `DATABASE_URL` | local Postgres | PostgreSQL connection |
| `REDIS_URL` | `redis://localhost:6379/0` | Redis connection |
| `PUBLIC_URL` | `http://localhost:8080` | Address agents use; shown in "Add worker" |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | — | First admin, created only if none exists |
| `COOKIE_SECURE` | `false` | Set `true` behind HTTPS |
| `SECRET_KEY` | — | Encrypts stored secrets (alert channels, SMTP password). Required for alerts. Never change it once set |
| `STATUS_URL`, `ADMIN_URL` | `http://localhost:3000`, `:3001` | Public addresses of the two sites, used for links in emails |
| `SMTP_URL` | — | Fallback mail server, e.g. `smtps://user:pass@smtp.example.com?from=Status%20<status@example.com>` |
| `SERVER_ID` | hostname + random | Unique per instance |
| `STATIC_STATUS_DIR`, `STATIC_ADMIN_DIR` | — | Optionally serve the built sites from the Go binary |

Agent variables: `DRIX_SERVER_URL`, `DRIX_AGENT_TOKEN`, `DRIX_MAX_CONCURRENCY` (default 512).

## Production install

On a Linux server (Debian or Ubuntu; other distros need Docker already installed):

```bash
curl -fsSL https://raw.githubusercontent.com/drix-studio/drix-status/main/install.sh | sudo bash
```

The installer:
1. installs Docker if needed;
2. downloads the code to `/opt/drix`;
3. asks a few questions;
4. builds and starts everything, set to restart on boot.

It asks for:
- **Two domains**, e.g. `status.example.com` and `admin.example.com`, both pointing at the server. Envoy then serves the two sites over HTTPS, with free Let's Encrypt certificates that renew themselves. Leave the domains empty to use the server's IP address instead. It then asks whether to serve HTTPS there with Drix's own certificate (see below), or plain ports only (3000, 3001, 8080).
- **The admin login.** Leave the password empty and one is generated and shown once.
- **An SMTP URL** (optional) for email.

Settings are saved in `/opt/drix/.env` (readable by root only). Database passwords and the encryption key are generated, and re-running the installer never changes them.

**Add workers** in the admin panel under **Workers → Add worker**. It shows a command to run on each machine that should run checks:

```bash
curl -fsSL https://raw.githubusercontent.com/drix-studio/drix-status/main/install.sh \
  | sudo bash -s -- worker --server https://status.example.com --token drx_...
```

**Maintenance:**

```bash
sudo /opt/drix/install.sh update        # back up the database, pull the latest code and rebuild
sudo /opt/drix/install.sh backup        # back up the database now
sudo /opt/drix/install.sh restore FILE  # replace the database with a backup (asks first)
sudo /opt/drix/install.sh renew-tls     # renew the certificate from Drix's own CA now (also weekly from cron)
sudo /opt/drix/install.sh status        # container status
sudo /opt/drix/install.sh logs server   # follow a service's logs (server, envoy, certbot, …)
sudo /opt/drix/install.sh uninstall     # stop; keeps the data (--purge deletes it)
```

**Options:** every question has a flag, so the installer can run without prompts, e.g. `--status-domain … --admin-domain … --email … --admin-email … --yes`. Other useful flags:
- `--staging` uses Let's Encrypt test certificates while you try things out. To switch to a real certificate afterwards, set `DRIX_LE_STAGING=''` in `/opt/drix/.env` and run `sudo /opt/drix/install.sh update`; the test certificate is replaced automatically.
- `--tls local` gives HTTPS without a domain, or with domains that Let's Encrypt can't reach. It uses Drix's own certificate authority (see below). `--tls self-signed` does the same.
- `--branch` installs a different branch.

See `install.sh --help` for the full list.

**Files:**

| File | What it is |
|---|---|
| `deploy/docker-compose.prod.yml` | Postgres, Redis, server and both sites |
| `deploy/docker-compose.tls.yml` | Envoy and certbot (used with domains) |
| `deploy/docker-compose.ports.yml` | Plain ports (used without domains) |
| `deploy/docker-compose.tls-ip.yml` | The admin panel's HTTPS port for `--tls local` without domains |
| `deploy/envoy/envoy-ip.yaml.tmpl` | Envoy config for HTTPS on the IP address (one port per site) |
| `deploy/envoy/envoy.yaml.tmpl` | Envoy config template, rendered to `data/envoy.yaml` |

Envoy sends `/api` on both domains to the server, including the workers' WebSocket and the live-update streams, and everything else to the matching site. It reloads renewed certificates without restarting.

### HTTPS without a domain

Phones only allow push notifications, the installable app's offline start and passkeys over HTTPS. If the server has no domain, for example on a school or office network or only reachable through a VPN such as Twingate, use:

```bash
sudo /opt/drix/install.sh --tls local      # also converts an existing plain install
```

- **Drix makes its own certificate authority** (`/opt/drix/data/tls/ca.crt`, valid 10 years) and a certificate for every address of the server.
- **Addresses:**

  | Address | What |
  |---|---|
  | `https://<ip>` (`--https-port`, default 443) | Status page |
  | `https://<ip>:8443` (`--admin-https-port`) | Admin panel and phone app |
  | `http://<ip>/drix-ca.crt` | The certificate authority, for devices to download |

  The plain ports (3000, 3001, 8080) keep working, and workers keep connecting to `http://<ip>:8080`.
- **Each device trusts the certificate authority once:**
  1. Open `http://<ip>/drix-ca.crt` and allow the download.
  2. On an iPhone, go to Settings → Profile Downloaded → Install, then Settings → General → About → Certificate Trust Settings and turn on **Drix Status CA**. On a Mac, open the file and set it to *Always Trust* in Keychain Access.
  3. On the iPhone, open `https://<ip>:8443/m` in Safari and add it to the Home Screen.
- **Renewal:** the server certificate lasts 397 days, the longest Apple devices accept. It's renewed automatically: weekly from cron (`renew-tls`) and on every `update`. Devices don't need to do anything again.
- **Passkeys don't work on a bare IP address.** Use an authenticator app for two-factor sign-in, or give the server a domain.
- **Keep it private:** `/opt/drix/data/tls/ca.key` is the authority's private key. Anyone holding it could make certificates that your devices trust.

### Backups

The database (monitors, history, users, settings and alert channels) is backed up **every night at 03:00 UTC** to `/opt/drix/backups` on the server. The 14 newest backups are kept. Every `install.sh update` also saves one first.

- **Settings → Backups** shows when the last one ran. There you can change the time and how many to keep, make one now, or download one (owners only).
- **Failures** are sent to the alert channels that get "down" alerts.
- **Off-site copies:** add S3-compatible storage (AWS S3, Cloudflare R2, Backblaze B2, MinIO…) under Settings → Backups. Each backup is then uploaded too, and the same number is kept there. Use a bucket that only Drix writes to.
- **A backup contains everything**, including password hashes and the encrypted alert settings. Keep downloads private. `/opt/drix/.env` holds `SECRET_KEY`, so keep a copy of it as well: without it, a restored backup's alert channels and SMTP password can't be decrypted.

**To restore:**

```bash
ls /opt/drix/backups
sudo /opt/drix/install.sh restore drix-20261122-030000.dump
```

It asks for confirmation and first saves the current data as a `-pre-restore` backup. It then replaces the database in one transaction, so a failed restore changes nothing. To move Drix to a new server, install it there, copy over the backup and `/opt/drix/.env`, and restore.

Backups are made with `pg_dump`. Outside the production install, set `BACKUP_DIR` and have `pg_dump` 17 on the server's path.

### Running it yourself

`make docker` builds and starts everything with `deploy/docker-compose.yml` (profile `full`): the server, the status site on :3000 and the admin site on :3001 (nginx, proxying `/api` to the server), and one agent. Agents connect to whatever URL you set as `PUBLIC_URL`. The agent image is built from `deploy/Dockerfile.agent`.

## Tests

```bash
make test   # Go unit tests (checkers, even split and stability) + go vet + TypeScript typecheck
```
