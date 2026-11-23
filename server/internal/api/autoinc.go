package api

import (
	"net/http"

	"drix/server/internal/autoinc"
)

func (a *API) getAutoIncidents(w http.ResponseWriter, r *http.Request) {
	s, err := a.AutoInc.Settings(r.Context())
	if err != nil {
		a.serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *API) putAutoIncidents(w http.ResponseWriter, r *http.Request) {
	var in autoinc.Settings
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if in.AfterMin < 1 || in.AfterMin > 60 {
		writeValidation(w, fieldErr{"after_min", "Between 1 and 60 minutes"})
		return
	}
	if err := a.AutoInc.SaveSettings(r.Context(), in); err != nil {
		a.serverErr(w, err)
		return
	}
	if in.Enabled {
		a.audit(r, "settings.auto_incidents", "settings", "auto_incidents", "Turned on automatic incidents")
	} else {
		a.audit(r, "settings.auto_incidents", "settings", "auto_incidents", "Turned off automatic incidents")
	}
	writeJSON(w, http.StatusOK, in)
}
