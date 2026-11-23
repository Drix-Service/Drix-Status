package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SMTPSettings configure outgoing email.
type SMTPSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"-"`
	From     string `json:"from"`
	TLS      string `json:"tls"` // starttls | tls | none
}

func (s SMTPSettings) Configured() bool { return s.Host != "" && s.From != "" }

// ParseSMTPURL reads smtp://user:pass@host:port?from=x@y&tls=none (used by
// SMTP_URL, e.g. for Mailpit in development).
func ParseSMTPURL(raw string) (SMTPSettings, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return SMTPSettings{}, errors.New("invalid SMTP_URL")
	}
	s := SMTPSettings{Host: u.Hostname(), From: u.Query().Get("from"), TLS: u.Query().Get("tls")}
	s.Port, _ = strconv.Atoi(u.Port())
	if u.User != nil {
		s.Username = u.User.Username()
		s.Password, _ = u.User.Password()
	}
	if s.TLS == "" {
		s.TLS = map[bool]string{true: "tls", false: "starttls"}[u.Scheme == "smtps"]
	}
	if s.Port == 0 {
		s.Port = map[string]int{"tls": 465, "starttls": 587, "none": 25}[s.TLS]
	}
	return s, nil
}

// Email is one message.
type Email struct {
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	Text    string            `json:"text"`
	HTML    string            `json:"html"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Send delivers an email; each recipient gets their own copy (no shared To).
func Send(ctx context.Context, s SMTPSettings, e Email) error {
	if !s.Configured() {
		return errors.New("email is not set up (Settings → Email)")
	}
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("invalid from address: %w", err)
	}
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if s.TLS == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: s.Host})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer c.Close()
	if err := c.Hello("drix-status"); err != nil {
		return err
	}
	if s.TLS == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("server doesn't support STARTTLS; choose TLS or none")
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.Host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("login failed: %w", err)
		}
	}
	for _, to := range e.To {
		if err := c.Mail(from.Address); err != nil {
			return err
		}
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("recipient %s: %w", to, err)
		}
		w, err := c.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write(build(from, to, e)); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
	}
	return c.Quit()
}

func build(from *mail.Address, to string, e Email) []byte {
	var b bytes.Buffer
	boundary := randHex(12)
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	h("From", from.String())
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", e.Subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", fmt.Sprintf("<%s@drix-status>", randHex(16)))
	h("MIME-Version", "1.0")
	for k, v := range e.Headers {
		h(k, v)
	}
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	part := func(ct, body string) {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, ct)
		qp := quotedprintable.NewWriter(&b)
		_, _ = qp.Write([]byte(body))
		_ = qp.Close()
		b.WriteString("\r\n")
	}
	part("text/plain", e.Text)
	if e.HTML != "" {
		part("text/html", e.HTML)
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// cleanList normalises a comma/space separated list of addresses.
func cleanList(in []string) []string {
	var out []string
	for _, v := range in {
		for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
			if a, err := mail.ParseAddress(p); err == nil {
				out = append(out, a.Address)
			}
		}
	}
	return out
}
