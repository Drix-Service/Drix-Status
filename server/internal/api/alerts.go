package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"drix/server/internal/notify"
	"drix/server/internal/secret"
	"drix/server/internal/store"
)

type channelOut struct {
	store.Channel
	Config any `json:"config"`
}

func (a *API) channelOut(c store.Channel) channelOut {
	out := channelOut{Channel: c}
	var cfg map[string]any
	if err := a.Notify.OpenConfig(c.ConfigEnc, &cfg); err == nil {
		out.Config = cfg
	} else {
		out.Config = map[string]any{"error": err.Error()}
	}
	return out
}

func (a *API) listChannels(w http.ResponseWriter, r *http.Request) {
	chs, err := a.Store.ListChannels(r.Context())
	if err != nil {
		a.serverErr(w, err)
		return
	}
	out := make([]channelOut, len(chs))
	for i, c := range chs {
		out[i] = a.channelOut(c)
	}
	writeJSON(w, http.StatusOK, out)
}

type channelInput struct {
	Name           string          `json:"name"`
	Type           string          `json:"type"`
	Config         json.RawMessage `json:"config"`
	Events         []string        `json:"events"`
	ApplyToAll     *bool           `json:"apply_to_all"`
	MonitorIDs     []int64         `json:"monitor_ids"`
	RemindEveryMin int             `json:"remind_every_min"`
	EscalateAfter  int             `json:"escalate_after_min"`
	Enabled        *bool           `json:"enabled"`
}

// validateChannelConfig checks a type's config and returns it normalised.
func validateChannelConfig(typ string, raw json.RawMessage) (any, error) {
	switch typ {
	case "email":
		var c notify.EmailConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fieldErr{"config", "Invalid email settings"}
		}
		var to []string
		for _, v := range c.To {
			for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
				addr, err := mail.ParseAddress(p)
				if err != nil {
					return nil, fieldErr{"config.to", fmt.Sprintf("%q isn't a valid email address", p)}
				}
				to = append(to, addr.Address)
			}
		}
		if len(to) == 0 || len(to) > 50 {
			return nil, fieldErr{"config.to", "Add between 1 and 50 email addresses"}
		}
		return notify.EmailConfig{To: to}, nil
	case "discord", "slack", "webhook":
		var c notify.WebhookConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fieldErr{"config", "Invalid settings"}
		}
		c.URL = strings.TrimSpace(c.URL)
		u, err := url.Parse(c.URL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, fieldErr{"config.url", "Enter the full webhook URL"}
		}
		switch {
		case typ == "discord" && !(strings.HasSuffix(u.Host, "discord.com") || strings.HasSuffix(u.Host, "discordapp.com")):
			return nil, fieldErr{"config.url", "Use a Discord webhook URL (https://discord.com/api/webhooks/…)"}
		case typ == "slack" && u.Host != "hooks.slack.com":
			return nil, fieldErr{"config.url", "Use a Slack incoming-webhook URL (https://hooks.slack.com/…)"}
		}
		if typ != "webhook" {
			c.Secret = ""
		} else if c.Secret == "" {
			c.Secret = newPushToken() + newPushToken()
		}
		return c, nil
	case "telegram":
		var c notify.TelegramConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fieldErr{"config", "Invalid settings"}
		}
		c.BotToken, c.ChatID = strings.TrimSpace(c.BotToken), strings.TrimSpace(c.ChatID)
		if !telegramToken.MatchString(c.BotToken) {
			return nil, fieldErr{"config.bot_token", "Paste the bot token from @BotFather (looks like 123456789:AA…)"}
		}
		if !telegramChat.MatchString(c.ChatID) {
			return nil, fieldErr{"config.chat_id", "Use a numeric chat ID (e.g. -1001234567890) or @channelname"}
		}
		return c, nil
	case "teams":
		var c notify.TeamsConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fieldErr{"config", "Invalid settings"}
		}
		c.URL = strings.TrimSpace(c.URL)
		if u, err := url.Parse(c.URL); err != nil || u.Scheme != "https" || u.Host == "" {
			return nil, fieldErr{"config.url", "Paste the workflow's HTTP POST URL (starts with https://)"}
		}
		return c, nil
	case "pagerduty":
		var c notify.PagerDutyConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fieldErr{"config", "Invalid settings"}
		}
		c.RoutingKey = strings.TrimSpace(c.RoutingKey)
		if !alnum32.MatchString(c.RoutingKey) {
			return nil, fieldErr{"config.routing_key", "Paste the 32-character integration key from an Events API v2 integration"}
		}
		return c, nil
	case "opsgenie":
		var c notify.OpsgenieConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fieldErr{"config", "Invalid settings"}
		}
		c.APIKey, c.Region = strings.TrimSpace(c.APIKey), strings.ToLower(strings.TrimSpace(c.Region))
		if c.Region == "" {
			c.Region = "us"
		}
		if len(c.APIKey) < 20 || len(c.APIKey) > 100 || strings.ContainsAny(c.APIKey, " /") {
			return nil, fieldErr{"config.api_key", "Paste the API key of an Opsgenie API integration"}
		}
		if c.Region != "us" && c.Region != "eu" {
			return nil, fieldErr{"config.region", "Choose US or EU"}
		}
		return c, nil
	case "ntfy":
		var c notify.NtfyConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fieldErr{"config", "Invalid settings"}
		}
		c.Server, c.Topic, c.Token = strings.TrimRight(strings.TrimSpace(c.Server), "/"), strings.TrimSpace(c.Topic), strings.TrimSpace(c.Token)
		if c.Server != "" {
			if u, err := url.Parse(c.Server); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
				return nil, fieldErr{"config.server", "Enter the server address, e.g. https://ntfy.sh"}
			}
		}
		if !ntfyTopic.MatchString(c.Topic) {
			return nil, fieldErr{"config.topic", "Use letters, numbers, - and _ (up to 64)"}
		}
		return c, nil
	case "pushover":
		var c notify.PushoverConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fieldErr{"config", "Invalid settings"}
		}
		c.AppToken, c.UserKey = strings.TrimSpace(c.AppToken), strings.TrimSpace(c.UserKey)
		if !alnum30.MatchString(c.AppToken) {
			return nil, fieldErr{"config.app_token", "Paste the 30-character API token of your Pushover application"}
		}
		if !alnum30.MatchString(c.UserKey) {
			return nil, fieldErr{"config.user_key", "Paste your 30-character user (or group) key"}
		}
		return c, nil
	}
	return nil, fieldErr{"type", "Choose a channel type"}
}

var (
	telegramToken = regexp.MustCompile(`^\d{5,}:[A-Za-z0-9_-]{30,}$`)
	telegramChat  = regexp.MustCompile(`^(-?\d{3,20}|@[A-Za-z][A-Za-z0-9_]{4,31})$`)
	alnum32       = regexp.MustCompile(`^[A-Za-z0-9]{32}$`)
	alnum30       = regexp.MustCompile(`^[A-Za-z0-9]{30}$`)
	ntfyTopic     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

func (a *API) saveChannel(w http.ResponseWriter, r *http.Request, id int64) {
	var in channelInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	c := store.Channel{ID: id, Name: strings.TrimSpace(in.Name), Type: in.Type, RemindEveryMin: in.RemindEveryMin, EscalateAfterMin: in.EscalateAfter,
		ApplyToAll: true, Enabled: true, MonitorIDs: in.MonitorIDs}
	var cur store.Channel
	if id != 0 {
		var err error
		if cur, err = a.Store.GetChannel(r.Context(), id); err != nil {
			a.serverErr(w, err)
			return
		}
		c.Type, c.ConfigEnc = cur.Type, cur.ConfigEnc
	}
	if in.ApplyToAll != nil {
		c.ApplyToAll = *in.ApplyToAll
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	for _, e := range in.Events {
		if slices.Contains(store.AlertEvents, e) && !slices.Contains(c.Events, e) {
			c.Events = append(c.Events, e)
		}
	}
	switch {
	case c.Name == "" || len(c.Name) > 80:
		writeValidation(w, fieldErr{"name", "Give the channel a name (max 80 characters)"})
		return
	case len(c.Events) == 0:
		writeValidation(w, fieldErr{"events", "Pick at least one event"})
		return
	case c.RemindEveryMin < 0 || c.RemindEveryMin > 10080:
		writeValidation(w, fieldErr{"remind_every_min", "Between 0 (off) and 10080 minutes"})
		return
	case c.EscalateAfterMin < 0 || c.EscalateAfterMin > 1440:
		writeValidation(w, fieldErr{"escalate_after_min", "Between 0 (right away) and 1440 minutes"})
		return
	case !c.ApplyToAll && len(c.MonitorIDs) == 0:
		writeValidation(w, fieldErr{"monitor_ids", "Choose monitors, or alert for all monitors"})
		return
	}
	if len(in.Config) > 0 && string(in.Config) != "null" {
		cfg, err := validateChannelConfig(c.Type, in.Config)
		if err != nil {
			writeValidation(w, err)
			return
		}
		if c.ConfigEnc, err = a.Notify.SealConfig(cfg); err != nil {
			a.secretErr(w, err)
			return
		}
	} else if id == 0 {
		writeValidation(w, fieldErr{"config", "Settings are required"})
		return
	}
	out, err := a.Store.SaveChannel(r.Context(), c)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.Notify.InvalidateChannels()
	action := map[bool]string{true: "channel.create", false: "channel.update"}[id == 0]
	a.audit(r, action, "channel", out.ID, fmt.Sprintf("%s alert channel %s", out.Type, out.Name))
	writeJSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[id == 0], a.channelOut(out))
}

func (a *API) secretErr(w http.ResponseWriter, err error) {
	if errors.Is(err, secret.ErrNoKey) {
		writeErr(w, http.StatusFailedDependency, "Set SECRET_KEY on the server so secrets can be stored encrypted.")
		return
	}
	a.serverErr(w, err)
}

func (a *API) createChannel(w http.ResponseWriter, r *http.Request) { a.saveChannel(w, r, 0) }

func (a *API) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	a.saveChannel(w, r, id)
}

func (a *API) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := a.Store.DeleteChannel(r.Context(), id); err != nil {
		a.serverErr(w, err)
		return
	}
	a.Notify.InvalidateChannels()
	a.audit(r, "channel.delete", "channel", id, "Deleted alert channel")
	w.WriteHeader(http.StatusNoContent)
}

// testChannel sends a sample alert right away and reports the result.
func (a *API) testChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	c, err := a.Store.GetChannel(r.Context(), id)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if notify.RichPreviewTypes[c.Type] {
		a.testDiscordAll(w, r, c)
		return
	}
	m := notify.Message{Event: "test", Title: "Test alert from Drix Status", Label: "Test alert", Subject: c.Name, Status: "up", At: time.Now().UTC(),
		Body: "If you can read this, the " + c.Name + " channel works.", URL: strings.TrimRight(a.Cfg.AdminURL, "/") + "/alerts", StatusURL: strings.TrimRight(a.Cfg.StatusURL, "/"),
		Fields: []notify.Field{{Name: "Sent by", Value: userFrom(r.Context()).Email}}}
	err = a.Notify.Deliver(r.Context(), c, m, "test-"+newPushToken()[:8])
	d := store.Delivery{ChannelID: &c.ID, Event: "test", Title: m.Title, Status: "sent", Attempts: 1}
	if err != nil {
		d.Status, d.Error = "failed", truncateStr(err.Error(), 500)
	}
	_ = a.Store.LogDelivery(r.Context(), d)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Sending failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// testDiscordAll previews a Discord channel with one message of every
// kind it can receive. Discord allows about 5 webhook messages per 2
// seconds, so they're spaced out, with one retry if Discord says to wait.
func (a *API) testDiscordAll(w http.ResponseWriter, r *http.Request, c store.Channel) {
	msgs := notify.SampleMessages(a.Cfg.AdminURL, a.Cfg.StatusURL, time.Now().UTC())
	sent := 0
	var err error
	for i, m := range msgs {
		if i > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(450 * time.Millisecond):
			}
		}
		id := "test-" + newPushToken()[:8]
		if err = a.Notify.Deliver(r.Context(), c, m, id); notify.IsRateLimited(err) {
			time.Sleep(2 * time.Second)
			err = a.Notify.Deliver(r.Context(), c, m, id)
		}
		if err != nil {
			break
		}
		sent++
	}
	d := store.Delivery{ChannelID: &c.ID, Event: "test", Title: fmt.Sprintf("Test: %d of %d sample alerts", sent, len(msgs)), Status: "sent", Attempts: 1}
	if err != nil {
		d.Status, d.Error = "failed", truncateStr(err.Error(), 500)
	}
	_ = a.Store.LogDelivery(r.Context(), d)
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("Sent %d of %d, then failed: %s", sent, len(msgs), err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sent": sent})
}

func (a *API) listDeliveries(w http.ResponseWriter, r *http.Request) {
	cid, _ := strconv.ParseInt(r.URL.Query().Get("channel_id"), 10, 64)
	ds, err := a.Store.ListDeliveries(r.Context(), cid, queryInt(r, "limit", 50, 1, 200))
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

// ---------- email settings ----------

type emailSettingsOut struct {
	notify.SMTPSettings
	HasPassword bool   `json:"has_password"`
	Source      string `json:"source"` // saved | env | none
	CanStore    bool   `json:"can_store_secrets"`
}

func (a *API) getEmailSettings(w http.ResponseWriter, r *http.Request) {
	s, saved, err := a.Notify.SMTP(r.Context())
	if err != nil && !errors.Is(err, secret.ErrNoKey) {
		a.serverErr(w, err)
		return
	}
	src := "none"
	switch {
	case saved:
		src = "saved"
	case s.Configured():
		src = "env"
	}
	writeJSON(w, http.StatusOK, emailSettingsOut{SMTPSettings: s, HasPassword: a.Notify.HasSMTPPassword(r.Context()) || (src == "env" && s.Password != ""),
		Source: src, CanStore: a.Notify.Box() != nil})
}

func (a *API) putEmailSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host     string  `json:"host"`
		Port     int     `json:"port"`
		Username string  `json:"username"`
		Password *string `json:"password"` // nil = keep
		From     string  `json:"from"`
		TLS      string  `json:"tls"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	s := notify.SMTPSettings{Host: strings.TrimSpace(in.Host), Port: in.Port, Username: strings.TrimSpace(in.Username), From: strings.TrimSpace(in.From), TLS: in.TLS}
	if s.Host != "" {
		switch {
		case !validHost(s.Host):
			writeValidation(w, fieldErr{"host", "Enter the mail server's hostname"})
			return
		case s.Port < 1 || s.Port > 65535:
			writeValidation(w, fieldErr{"port", "Port must be 1-65535"})
			return
		case !slices.Contains([]string{"starttls", "tls", "none"}, s.TLS):
			writeValidation(w, fieldErr{"tls", "Choose STARTTLS, TLS or none"})
			return
		}
		if _, err := mail.ParseAddress(s.From); err != nil {
			writeValidation(w, fieldErr{"from", "Enter a sender like Status <status@example.com>"})
			return
		}
	}
	keep := in.Password == nil
	if !keep {
		s.Password = *in.Password
	}
	if err := a.Notify.SaveSMTP(r.Context(), s, keep); err != nil {
		a.secretErr(w, err)
		return
	}
	a.audit(r, "settings.email", "settings", nil, "Updated email settings")
	a.getEmailSettings(w, r)
}

func (a *API) testEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To string `json:"to"`
	}
	_ = readJSON(r, &in)
	to := strings.TrimSpace(in.To)
	if to == "" {
		to = userFrom(r.Context()).Email
	}
	if _, err := mail.ParseAddress(to); err != nil {
		writeValidation(w, fieldErr{"to", "Enter a valid email address"})
		return
	}
	err := a.Notify.SendEmailNow(r.Context(), notify.TestEmail(to, time.Now().UTC().Format("Mon 2 Jan 2006, 15:04 MST")))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "to": to})
}
