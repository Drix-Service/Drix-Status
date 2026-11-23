package api

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"drix/server/internal/backup"
)

type backupS3Out struct {
	Enabled   bool   `json:"enabled"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	HasSecret bool   `json:"has_secret"`
}

type backupInfo struct {
	Enabled     bool          `json:"enabled"`
	Time        string        `json:"time"`
	Keep        int           `json:"keep"`
	S3          backupS3Out   `json:"s3"`
	Unavailable string        `json:"unavailable,omitempty"`
	CanStore    bool          `json:"can_store_secrets"`
	Status      backup.Status `json:"status"`
	Files       []backup.File `json:"files"`
}

func (a *API) backupInfo(w http.ResponseWriter, r *http.Request) {
	b := a.Backups
	set, err := b.Settings(r.Context())
	if err != nil {
		a.serverErr(w, err)
		return
	}
	files, err := b.List()
	if err != nil {
		a.serverErr(w, err)
		return
	}
	if files == nil {
		files = []backup.File{}
	}
	st := b.Status(r.Context())
	if st.Uploaded {
		for i := range files {
			if files[i].Name == st.LastFile {
				files[i].Uploaded = true
			}
		}
	}
	writeJSON(w, http.StatusOK, backupInfo{
		Enabled: set.Enabled, Time: set.Time, Keep: set.Keep,
		S3: backupS3Out{Enabled: set.S3.Enabled, Endpoint: set.S3.Endpoint, Region: set.S3.Region, Bucket: set.S3.Bucket,
			Prefix: set.S3.Prefix, AccessKey: set.S3.AccessKey, HasSecret: b.HasS3Secret(r.Context())},
		Unavailable: b.Unavailable(), CanStore: a.Notify.Box() != nil, Status: st, Files: files,
	})
}

type backupSettingsIn struct {
	Enabled bool   `json:"enabled"`
	Time    string `json:"time"`
	Keep    int    `json:"keep"`
	S3      struct {
		Enabled   bool    `json:"enabled"`
		Endpoint  string  `json:"endpoint"`
		Region    string  `json:"region"`
		Bucket    string  `json:"bucket"`
		Prefix    string  `json:"prefix"`
		AccessKey string  `json:"access_key"`
		Secret    *string `json:"secret"` // nil = keep the saved one
	} `json:"s3"`
}

// parseBackupSettings validates input. The secret is filled from the saved
// settings when not given, so a test can use it.
func (a *API) parseBackupSettings(r *http.Request) (backup.Settings, map[string]string, error) {
	var in backupSettingsIn
	if err := readJSON(r, &in); err != nil {
		return backup.Settings{}, nil, err
	}
	fields := map[string]string{}
	if _, _, ok := backup.ParseTime(in.Time); !ok {
		fields["time"] = "Use a time like 03:00"
	}
	if in.Keep < 1 || in.Keep > 365 {
		fields["keep"] = "Keep between 1 and 365 backups"
	}
	out := backup.Settings{Enabled: in.Enabled, Time: in.Time, Keep: in.Keep}
	s3 := in.S3
	out.S3 = backup.S3Settings{Enabled: s3.Enabled, Endpoint: strings.TrimSpace(s3.Endpoint), Region: strings.TrimSpace(s3.Region),
		Bucket: strings.TrimSpace(s3.Bucket), Prefix: strings.Trim(strings.TrimSpace(s3.Prefix), "/"), AccessKey: strings.TrimSpace(s3.AccessKey)}
	if s3.Secret != nil {
		out.S3.Secret = strings.TrimSpace(*s3.Secret)
	} else if cur, err := a.Backups.Settings(r.Context()); err == nil {
		out.S3.Secret = cur.S3.Secret
	}
	if out.S3.Endpoint != "" {
		if u, err := url.Parse(out.S3.Endpoint); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			fields["s3.endpoint"] = "Use a full address like https://s3.eu-central-1.amazonaws.com"
		}
	}
	if s3.Enabled {
		if out.S3.Bucket == "" {
			fields["s3.bucket"] = "Required"
		}
		if out.S3.AccessKey == "" {
			fields["s3.access_key"] = "Required"
		}
		if out.S3.Secret == "" {
			fields["s3.secret"] = "Required"
		}
		if out.S3.Endpoint == "" && out.S3.Region == "" {
			fields["s3.region"] = "Give a region, or an endpoint for other providers"
		}
	}
	return out, fields, nil
}

func (a *API) putBackupSettings(w http.ResponseWriter, r *http.Request) {
	set, fields, err := a.parseBackupSettings(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if len(fields) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Please fix the highlighted fields", Field: fields})
		return
	}
	if err := a.Backups.SaveSettings(r.Context(), set); err != nil {
		if strings.Contains(err.Error(), "SECRET_KEY") {
			writeErr(w, http.StatusBadRequest, "Set SECRET_KEY on the server to store the S3 secret")
			return
		}
		a.serverErr(w, err)
		return
	}
	a.audit(r, "backup.settings", "settings", "backup", "Changed backup settings")
	a.backupInfo(w, r)
}

func (a *API) testBackupS3(w http.ResponseWriter, r *http.Request) {
	set, fields, err := a.parseBackupSettings(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	set.S3.Enabled = true
	for k := range fields {
		if !strings.HasPrefix(k, "s3.") {
			delete(fields, k)
		}
	}
	if len(fields) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "Please fix the highlighted fields", Field: fields})
		return
	}
	if err := a.Backups.TestS3(r.Context(), set.S3); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) runBackup(w http.ResponseWriter, r *http.Request) {
	f, err := a.Backups.Run(r.Context())
	if errors.Is(err, backup.ErrBusy) {
		writeErr(w, http.StatusConflict, "A backup is already running")
		return
	}
	if err != nil && f.Name == "" {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	a.audit(r, "backup.run", "backup", f.Name, "Made a backup")
	out := map[string]any{"file": f}
	if err != nil {
		out["warning"] = err.Error() // saved, upload failed
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) downloadBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	f, info, err := a.Backups.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		a.serverErr(w, err)
		return
	}
	defer f.Close()
	a.audit(r, "backup.download", "backup", name, "Downloaded a backup")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}
