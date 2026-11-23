// Package config loads server configuration from the environment.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
)

// Config holds runtime settings for the main server.
type Config struct {
	HTTPAddr      string
	DatabaseURL   string
	RedisURL      string
	ServerID      string
	AdminEmail    string
	AdminPassword string
	CookieSecure  bool
	// PublicURL is the address agents should use to reach this server; it's
	// shown in the admin panel's "add worker" instructions.
	PublicURL string
	// AdminURL and StatusURL are used to build links in emails (invites,
	// alerts, subscriber confirmations).
	AdminURL  string
	StatusURL string
	// SecretKey encrypts stored secrets (SMTP password, webhook URLs).
	SecretKey string
	// SMTPURL is a fallback mail server (smtp://user:pass@host:port?from=…),
	// used until email is configured in the admin panel.
	SMTPURL string
	// StaticStatusDir / StaticAdminDir optionally serve the built frontends.
	StaticStatusDir string
	StaticAdminDir  string
	BackupDir       string // where scheduled database backups go; empty = off
}

// Load reads configuration with sensible development defaults.
func Load() Config {
	host, _ := os.Hostname()
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return Config{
		HTTPAddr:        env("HTTP_ADDR", ":8080"),
		DatabaseURL:     env("DATABASE_URL", "postgres://drix:drix@localhost:5432/drix?sslmode=disable"),
		RedisURL:        env("REDIS_URL", "redis://localhost:6379/0"),
		ServerID:        env("SERVER_ID", host+"-"+hex.EncodeToString(b)),
		AdminEmail:      os.Getenv("ADMIN_EMAIL"),
		AdminPassword:   os.Getenv("ADMIN_PASSWORD"),
		CookieSecure:    strings.EqualFold(os.Getenv("COOKIE_SECURE"), "true"),
		PublicURL:       env("PUBLIC_URL", "http://localhost:8080"),
		AdminURL:        env("ADMIN_URL", "http://localhost:3001"),
		StatusURL:       env("STATUS_URL", "http://localhost:3000"),
		SecretKey:       os.Getenv("SECRET_KEY"),
		SMTPURL:         os.Getenv("SMTP_URL"),
		StaticStatusDir: os.Getenv("STATIC_STATUS_DIR"),
		StaticAdminDir:  os.Getenv("STATIC_ADMIN_DIR"),
		BackupDir:       os.Getenv("BACKUP_DIR"),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
