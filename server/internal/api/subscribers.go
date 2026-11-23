package api

import (
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"drix/server/internal/snapshot"
	"drix/server/internal/store"
)

// subscribe starts a double opt-in. It always answers the same way so it
// can't be used to learn who is subscribed.
func (a *API) subscribe(w http.ResponseWriter, r *http.Request) {
	page, ok := a.pageFromRequest(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(in.Email))
	if err != nil || len(addr.Address) > 254 {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Enter a valid email address", Field: map[string]string{"email": "Enter a valid email address"}})
		return
	}
	email := strings.ToLower(addr.Address)
	ctx := r.Context()
	if !a.throttle(ctx, "drix:subrl:ip:"+remoteIP(r), 10, time.Hour) || !a.throttle(ctx, "drix:subrl:em:"+email, 3, time.Hour) {
		writeErr(w, http.StatusTooManyRequests, "Too many attempts. Try again later.")
		return
	}
	sub, err := a.Store.Subscribe(ctx, page.ID, email, randomToken())
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if sub.ConfirmedAt == nil && a.Notify != nil {
		a.Notify.SendConfirmation(ctx, page, sub)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *API) pageRedirect(w http.ResponseWriter, r *http.Request, pageID int64, query string) {
	target := strings.TrimRight(a.Cfg.StatusURL, "/") + "/"
	if p := a.Catalog.Get().PageByID[pageID]; p != nil && a.Notify != nil {
		target = strings.TrimRight(a.Notify.PageURL(p), "/") + "/"
		if !p.IsDefault && (p.CustomDomain == nil || *p.CustomDomain == "") {
			target = strings.TrimRight(target, "/")
		}
	}
	http.Redirect(w, r, target+"?"+query, http.StatusSeeOther)
}

func (a *API) confirmSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := a.Store.ConfirmSubscriber(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		http.Redirect(w, r, strings.TrimRight(a.Cfg.StatusURL, "/")+"/?subscription=invalid", http.StatusSeeOther)
		return
	}
	a.pageRedirect(w, r, sub.PageID, "subscription=confirmed")
}

// unsubscribe handles the email link (GET) and one-click List-Unsubscribe (POST).
func (a *API) unsubscribe(w http.ResponseWriter, r *http.Request) {
	sub, err := a.Store.Unsubscribe(r.Context(), r.URL.Query().Get("token"))
	if r.Method == http.MethodPost {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		http.Redirect(w, r, strings.TrimRight(a.Cfg.StatusURL, "/")+"/?subscription=unsubscribed", http.StatusSeeOther)
		return
	}
	a.pageRedirect(w, r, sub.PageID, "subscription=unsubscribed")
}

// ---------- feeds ----------

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Link    []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}
type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
}
type atomEntry struct {
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Link    atomLink `xml:"link"`
	Content struct {
		Type string `xml:"type,attr"`
		Body string `xml:",chardata"`
	} `xml:"content"`
}

type rssFeed struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel struct {
		Title       string    `xml:"title"`
		Link        string    `xml:"link"`
		Description string    `xml:"description"`
		Items       []rssItem `xml:"item"`
	} `xml:"channel"`
}
type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

func (a *API) feed(w http.ResponseWriter, r *http.Request) {
	page, ok := a.pageFromRequest(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	list, err := a.Store.ListIncidents(r.Context(), store.IncidentFilter{PageID: page.ID, Limit: 50})
	if err != nil {
		a.serverErr(w, err)
		return
	}
	data := a.Catalog.Get()
	base := strings.TrimRight(a.Cfg.StatusURL, "/")
	if a.Notify != nil {
		base = strings.TrimRight(a.Notify.PageURL(page), "/")
	}
	var items []snapshot.Incident
	for _, in := range list {
		if in.Status != "cancelled" {
			items = append(items, snapshot.PublicIncident(in, data, page.ID, true))
		}
	}
	describe := func(in snapshot.Incident) (string, string, time.Time) {
		kind := map[string]string{"incident": "Incident", "maintenance": "Maintenance"}[in.Kind]
		title := fmt.Sprintf("%s: %s", kind, in.Title)
		body, at := "", in.CreatedAt
		if len(in.Updates) > 0 {
			body = in.Updates[0].Body
			at = in.Updates[0].CreatedAt
		}
		return title, fmt.Sprintf("[%s] %s", in.Status, body), at
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	if strings.HasSuffix(r.URL.Path, ".rss") {
		var f rssFeed
		f.Version = "2.0"
		f.Channel.Title, f.Channel.Link, f.Channel.Description = page.Name+" status", base, "Incidents and maintenance for "+page.Name
		for _, in := range items {
			title, body, at := describe(in)
			link := fmt.Sprintf("%s/incidents/%d", base, in.ID)
			f.Channel.Items = append(f.Channel.Items, rssItem{Title: title, Link: link, GUID: link + "#" + strconv.FormatInt(at.Unix(), 10), PubDate: at.Format(time.RFC1123Z), Description: body})
		}
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		_, _ = w.Write([]byte(xml.Header))
		_ = xml.NewEncoder(w).Encode(f)
		return
	}
	f := atomFeed{Title: page.Name + " status", ID: base + "/", Updated: time.Now().UTC().Format(time.RFC3339),
		Link: []atomLink{{Href: base}, {Href: base + r.URL.Path, Rel: "self"}}}
	for _, in := range items {
		title, body, at := describe(in)
		link := fmt.Sprintf("%s/incidents/%d", base, in.ID)
		e := atomEntry{Title: title, ID: link, Updated: at.UTC().Format(time.RFC3339), Link: atomLink{Href: link}}
		e.Content.Type, e.Content.Body = "text", body
		f.Entries = append(f.Entries, e)
	}
	if len(items) > 0 {
		_, _, at := describe(items[0])
		f.Updated = at.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(f)
}

// ---------- admin ----------

func (a *API) listSubscribers(w http.ResponseWriter, r *http.Request) {
	p := a.page()
	if p == nil {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	id := p.ID
	subs, err := a.Store.ListSubscribers(r.Context(), id, strings.TrimSpace(r.URL.Query().Get("q")), queryInt(r, "limit", 200, 1, 10000))
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if strings.HasSuffix(r.URL.Path, ".csv") {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="subscribers.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"email", "status", "subscribed_at"})
		for _, s := range subs {
			status := "pending"
			if s.ConfirmedAt != nil {
				status = "confirmed"
			}
			_ = cw.Write([]string{s.Email, status, s.CreatedAt.UTC().Format(time.RFC3339)})
		}
		cw.Flush()
		return
	}
	counts, _ := a.Store.SubscriberCounts(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"subscribers": subs, "confirmed": counts[id][0], "pending": counts[id][1]})
}

func (a *API) deleteSubscriber(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := a.Store.DeleteSubscriber(r.Context(), id); err != nil {
		a.serverErr(w, err)
		return
	}
	a.audit(r, "subscriber.remove", "subscriber", id, "Removed a subscriber")
	w.WriteHeader(http.StatusNoContent)
}
