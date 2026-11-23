package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"drix/server/internal/store"
)

// page returns the single status page.
func (a *API) page() *store.Page { return a.Catalog.Get().DefaultPage() }

// ---------- Branding ----------

type brandingInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LogoURL     string `json:"logo_url"`
	WebsiteURL  string `json:"website_url"`
	AccentColor string `json:"accent_color"`
	FooterText  string `json:"footer_text"`
	ShowLatency *bool  `json:"show_latency"`
	Language    string `json:"language"`
}

// PageLanguages are the languages the status page is translated into.
var PageLanguages = []string{"en", "es", "fr", "de", "pt", "it", "nl", "pl", "da", "sv", "nb", "fi", "ja", "zh", "ko"}

func (a *API) getBranding(w http.ResponseWriter, r *http.Request) {
	p := a.page()
	if p == nil {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"page": p, "url": strings.TrimRight(a.Cfg.StatusURL, "/") + "/"})
}

func (a *API) putBranding(w http.ResponseWriter, r *http.Request) {
	cur := a.page()
	if cur == nil {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	var in brandingInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	p := *cur
	p.Name, p.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	p.LogoURL, p.WebsiteURL = strings.TrimSpace(in.LogoURL), strings.TrimSpace(in.WebsiteURL)
	p.AccentColor, p.FooterText = strings.TrimSpace(in.AccentColor), strings.TrimSpace(in.FooterText)
	if in.ShowLatency != nil {
		p.ShowLatency = *in.ShowLatency
	}
	if in.Language != "" {
		p.Language = in.Language
	}
	if !slices.Contains(PageLanguages, p.Language) {
		writeValidation(w, fieldErr{"language", "Choose one of the supported languages"})
		return
	}
	if p.AccentColor == "" {
		p.AccentColor = "#2563eb"
	}
	switch {
	case p.Name == "" || len(p.Name) > 80:
		writeValidation(w, fieldErr{"name", "Name is required (max 80 characters)"})
		return
	case !hexColor.MatchString(p.AccentColor):
		writeValidation(w, fieldErr{"accent_color", "Use a hex color like #2563eb"})
		return
	case len(p.Description) > 300:
		writeValidation(w, fieldErr{"description", "Keep it under 300 characters"})
		return
	case len(p.FooterText) > 500:
		writeValidation(w, fieldErr{"footer_text", "Keep it under 500 characters"})
		return
	}
	for field, u := range map[string]string{"logo_url": p.LogoURL, "website_url": p.WebsiteURL} {
		if u == "" {
			continue
		}
		if pu, err := url.Parse(u); err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || pu.Host == "" {
			writeValidation(w, fieldErr{field, "Enter a full URL starting with https://"})
			return
		}
	}
	p.CustomDomain = nil
	out, err := a.Store.UpdatePage(r.Context(), p)
	if err != nil {
		a.serverErr(w, err)
		return
	}
	a.changed(r.Context())
	a.audit(r, "branding.update", "page", out.ID, "Updated status page branding")
	writeJSON(w, http.StatusOK, map[string]any{"page": out, "url": strings.TrimRight(a.Cfg.StatusURL, "/") + "/"})
}

// ---------- Groups (sections of the status page) ----------

func (a *API) getGroups(w http.ResponseWriter, r *http.Request) {
	p := a.page()
	if p == nil {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	groups := a.Catalog.Get().Sections[p.ID]
	if groups == nil {
		groups = []store.Section{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

type groupsInput struct {
	Groups []struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Collapsed   bool   `json:"collapsed"`
		Monitors    []struct {
			MonitorID   int64   `json:"monitor_id"`
			DisplayName *string `json:"display_name"`
		} `json:"monitors"`
	} `json:"groups"`
}

// saveGroups replaces all groups and their monitors in one go.
func (a *API) saveGroups(w http.ResponseWriter, r *http.Request) {
	p := a.page()
	if p == nil {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	var in groupsInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if len(in.Groups) > 100 {
		writeErr(w, http.StatusUnprocessableEntity, "too many groups")
		return
	}
	data := a.Catalog.Get()
	sections := make([]store.Section, 0, len(in.Groups))
	for _, g := range in.Groups {
		name := strings.TrimSpace(g.Name)
		if name == "" || len(name) > 80 {
			writeErr(w, http.StatusUnprocessableEntity, "every group needs a name (max 80 characters)")
			return
		}
		sec := store.Section{ID: g.ID, Name: name, Description: truncateStr(strings.TrimSpace(g.Description), 300), Collapsed: g.Collapsed}
		for _, m := range g.Monitors {
			if _, ok := data.ByID[m.MonitorID]; !ok {
				writeErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("monitor %d doesn't exist", m.MonitorID))
				return
			}
			pm := store.PageMonitor{MonitorID: m.MonitorID}
			if m.DisplayName != nil {
				if dn := strings.TrimSpace(*m.DisplayName); dn != "" {
					dn = truncateStr(dn, 100)
					pm.DisplayName = &dn
				}
			}
			sec.Monitors = append(sec.Monitors, pm)
		}
		sections = append(sections, sec)
	}
	if err := a.Store.SaveLayout(r.Context(), p.ID, sections); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusConflict, "the groups changed in another tab; reload and try again")
			return
		}
		a.serverErr(w, err)
		return
	}
	a.changed(r.Context())
	a.audit(r, "groups.update", "page", p.ID, fmt.Sprintf("Updated groups (%d)", len(sections)))
	a.getGroups(w, r)
}
