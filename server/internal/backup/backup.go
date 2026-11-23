// Package backup makes scheduled pg_dump backups of the Drix database, keeps
// the newest few on disk and optionally uploads them to S3-compatible storage.
package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"drix/server/internal/secret"
	"drix/server/internal/store"
)

// Settings are stored under the "backup" settings key.
type Settings struct {
	Enabled bool       `json:"enabled"`
	Time    string     `json:"time"` // "HH:MM", UTC
	Keep    int        `json:"keep"`
	S3      S3Settings `json:"s3"`
}

type S3Settings struct {
	Enabled   bool   `json:"enabled"`
	Endpoint  string `json:"endpoint"` // https://s3.eu-central-1.amazonaws.com, https://<account>.r2.cloudflarestorage.com, …
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"` // folder inside the bucket, e.g. "drix/"
	AccessKey string `json:"access_key"`
	SecretEnc string `json:"secret_enc,omitempty"`
	Secret    string `json:"-"`
}

func DefaultSettings() Settings { return Settings{Enabled: true, Time: "03:00", Keep: 14} }

// Status is what the admin shows: the last result.
type Status struct {
	LastAt      *time.Time `json:"last_at"`
	LastFile    string     `json:"last_file"`
	LastSize    int64      `json:"last_size"`
	LastError   string     `json:"last_error"`
	LastErrorAt *time.Time `json:"last_error_at"`
	Uploaded    bool       `json:"uploaded"`
	UploadError string     `json:"upload_error"`
}

// File is one backup on disk.
type File struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Created  time.Time `json:"created_at"`
	Uploaded bool      `json:"uploaded"`
}

// NamePattern matches the files this package writes (and install.sh's
// pre-update backups). Anything else in the folder is left alone.
var NamePattern = regexp.MustCompile(`^drix-\d{8}-\d{6}(-[a-z-]+)?\.dump$`)

// Service runs backups. Dir empty or no pg_dump means backups are unavailable.
type Service struct {
	st    *store.Store
	rdb   *redis.Client
	box   *secret.Box
	dir   string
	dbURL string
	log   *slog.Logger

	// OnFailure is called (at most once a day) when a scheduled backup fails.
	OnFailure func(ctx context.Context, err error)

	pgDump string
	now    func() time.Time
}

func New(st *store.Store, rdb *redis.Client, box *secret.Box, dir, dbURL string, log *slog.Logger) *Service {
	s := &Service{st: st, rdb: rdb, box: box, dir: dir, dbURL: dbURL, log: log, now: time.Now}
	if p, err := exec.LookPath("pg_dump"); err == nil {
		s.pgDump = p
	}
	return s
}

// Unavailable says why backups can't run here, or "" if they can.
func (s *Service) Unavailable() string {
	switch {
	case s.dir == "":
		return "BACKUP_DIR is not set on the server. The production install sets it up."
	case s.pgDump == "":
		return "pg_dump is not installed on the server. The production install includes it."
	}
	return ""
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	out := DefaultSettings()
	if _, err := s.st.GetJSONSetting(ctx, "backup", &out); err != nil {
		return out, err
	}
	if out.S3.SecretEnc != "" && s.box != nil {
		sec, err := s.box.Open(out.S3.SecretEnc)
		if err != nil {
			return out, fmt.Errorf("open S3 secret: %w", err)
		}
		out.S3.Secret = sec
	}
	return out, nil
}

// SaveSettings stores settings; an empty secret keeps the saved one.
func (s *Service) SaveSettings(ctx context.Context, in Settings) error {
	var cur Settings
	_, _ = s.st.GetJSONSetting(ctx, "backup", &cur)
	in.S3.SecretEnc = cur.S3.SecretEnc
	if in.S3.Secret != "" {
		if s.box == nil {
			return errors.New("SECRET_KEY is not set")
		}
		enc, err := s.box.Seal(in.S3.Secret)
		if err != nil {
			return err
		}
		in.S3.SecretEnc = enc
	}
	in.S3.Secret = ""
	return s.st.SaveJSONSetting(ctx, "backup", in)
}

// HasS3Secret reports whether a secret is stored (it's never returned).
func (s *Service) HasS3Secret(ctx context.Context) bool {
	var cur Settings
	_, _ = s.st.GetJSONSetting(ctx, "backup", &cur)
	return cur.S3.SecretEnc != ""
}

func (s *Service) Status(ctx context.Context) Status {
	var st Status
	_, _ = s.st.GetJSONSetting(ctx, "backup_status", &st)
	return st
}

// List returns the backups on disk, newest first.
func (s *Service) List() ([]File, error) {
	if s.dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range entries {
		if e.IsDir() || !NamePattern.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, File{Name: e.Name(), Size: info.Size(), Created: createdAt(e.Name(), info.ModTime())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

func createdAt(name string, fallback time.Time) time.Time {
	if len(name) >= 20 {
		if t, err := time.Parse("20060102-150405", name[5:20]); err == nil {
			return t
		}
	}
	return fallback.UTC()
}

// Open returns a backup for download. The name must be one of ours.
func (s *Service) Open(name string) (*os.File, os.FileInfo, error) {
	if s.dir == "" || !NamePattern.MatchString(name) {
		return nil, nil, os.ErrNotExist
	}
	f, err := os.Open(filepath.Join(s.dir, name))
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

const runLock = "drix:backup:running"

// ErrBusy means another backup is already running.
var ErrBusy = errors.New("a backup is already running")

// Run makes one backup now, prunes old ones and uploads it if S3 is set up.
func (s *Service) Run(ctx context.Context) (File, error) {
	if why := s.Unavailable(); why != "" {
		return File{}, errors.New(why)
	}
	ok, err := s.rdb.SetNX(ctx, runLock, "1", 30*time.Minute).Result()
	if err != nil {
		return File{}, err
	}
	if !ok {
		return File{}, ErrBusy
	}
	defer s.rdb.Del(context.WithoutCancel(ctx), runLock)

	set, err := s.Settings(ctx)
	if err != nil {
		return File{}, err
	}
	f, err := s.dump(ctx)
	st := s.Status(ctx)
	now := s.now().UTC()
	if err != nil {
		st.LastError, st.LastErrorAt = err.Error(), &now
		_ = s.st.SaveJSONSetting(ctx, "backup_status", st)
		return File{}, err
	}
	st.LastAt, st.LastFile, st.LastSize, st.LastError, st.LastErrorAt = &now, f.Name, f.Size, "", nil
	st.Uploaded, st.UploadError = false, ""

	if set.S3.Enabled {
		if err := s.upload(ctx, set.S3, f); err != nil {
			st.UploadError = err.Error()
			s.log.Warn("backup upload failed", "file", f.Name, "err", err)
		} else {
			st.Uploaded, f.Uploaded = true, true
			if err := s.pruneRemote(ctx, set.S3, set.Keep); err != nil {
				s.log.Warn("pruning uploaded backups", "err", err)
			}
		}
	}
	_ = s.st.SaveJSONSetting(ctx, "backup_status", st)
	if err := s.Prune(set.Keep); err != nil {
		s.log.Warn("pruning backups", "err", err)
	}
	s.log.Info("backup done", "file", f.Name, "bytes", f.Size, "uploaded", f.Uploaded)
	if st.UploadError != "" {
		return f, fmt.Errorf("saved on the server, but the upload failed: %s", st.UploadError)
	}
	return f, nil
}

// dump runs pg_dump into a temp file and renames it when complete.
func (s *Service) dump(ctx context.Context) (File, error) {
	cfg, err := pgconn.ParseConfig(s.dbURL)
	if err != nil {
		return File{}, fmt.Errorf("database address: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return File{}, err
	}
	name := "drix-" + s.now().UTC().Format("20060102-150405") + ".dump"
	final := filepath.Join(s.dir, name)
	tmp := final + ".partial"

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.pgDump, "--format=custom", "--no-owner", "--no-privileges", "--file", tmp)
	// Credentials go in the environment, not the command line.
	cmd.Env = append(os.Environ(),
		"PGHOST="+cfg.Host, "PGPORT="+strconv.Itoa(int(cfg.Port)), "PGUSER="+cfg.User,
		"PGPASSWORD="+cfg.Password, "PGDATABASE="+cfg.Database, "PGCONNECT_TIMEOUT=10")
	if cfg.TLSConfig == nil {
		cmd.Env = append(cmd.Env, "PGSSLMODE=disable")
	}
	var stderr strings.Builder
	cmd.Stderr = &limitWriter{w: &stderr, n: 4000}
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return File{}, fmt.Errorf("pg_dump: %s", msg)
	}
	_ = os.Chmod(tmp, 0o640) // a backup holds every secret: not world-readable
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return File{}, err
	}
	info, err := os.Stat(final)
	if err != nil {
		return File{}, err
	}
	return File{Name: name, Size: info.Size(), Created: createdAt(name, info.ModTime())}, nil
}

// Prune keeps the newest `keep` backups on disk.
func (s *Service) Prune(keep int) error {
	if keep < 1 {
		keep = 1
	}
	files, err := s.List()
	if err != nil {
		return err
	}
	for _, f := range files[min(keep, len(files)):] {
		if err := os.Remove(filepath.Join(s.dir, f.Name)); err != nil {
			return err
		}
	}
	// Leftovers from a crash mid-dump.
	if partials, _ := filepath.Glob(filepath.Join(s.dir, "drix-*.dump.partial")); len(partials) > 0 {
		for _, p := range partials {
			if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) > time.Hour {
				os.Remove(p)
			}
		}
	}
	return nil
}

// Schedule runs the daily backup on the leader. If the server was down at the
// scheduled time, it runs as soon as it's back the same day.
func (s *Service) Schedule(ctx context.Context, isLeader func() bool) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !isLeader() || s.Unavailable() != "" {
			continue
		}
		set, err := s.Settings(ctx)
		if err != nil || !set.Enabled {
			continue
		}
		now := s.now().UTC()
		if !Due(now, set.Time) {
			continue
		}
		day := "drix:backup:day:" + now.Format("2006-01-02")
		ok, err := s.rdb.SetNX(ctx, day, "1", 48*time.Hour).Result()
		if err != nil || !ok {
			continue
		}
		if _, err := s.Run(ctx); err != nil && !errors.Is(err, ErrBusy) {
			s.log.Error("scheduled backup failed", "err", err)
			// Try again in an hour.
			s.rdb.Expire(ctx, day, time.Hour)
			if ok, _ := s.rdb.SetNX(ctx, "drix:backup:alerted:"+now.Format("2006-01-02"), "1", 48*time.Hour).Result(); ok && s.OnFailure != nil {
				s.OnFailure(ctx, err)
			}
		}
	}
}

// Due reports whether today's backup time ("HH:MM", UTC) has passed.
func Due(now time.Time, at string) bool {
	h, m, ok := ParseTime(at)
	if !ok {
		h, m = 3, 0
	}
	return now.Hour()*60+now.Minute() >= h*60+m
}

// ParseTime parses "HH:MM".
func ParseTime(s string) (h, m int, ok bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, false
	}
	return t.Hour(), t.Minute(), true
}

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	q := p
	if len(q) > l.n {
		q = q[:l.n]
	}
	l.n -= len(q)
	_, err := l.w.Write(q)
	return len(p), err
}
