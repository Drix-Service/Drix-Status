// Package totp implements time-based one-time passwords (RFC 6238) as used by
// authenticator apps: HMAC-SHA1, 6 digits, 30-second steps.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	Digits = 6
	Period = 30
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a random 160-bit secret, base32-encoded.
func NewSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return b32.EncodeToString(b)
}

// Step is the time step a moment falls in.
func Step(t time.Time) int64 { return t.Unix() / Period }

// CodeAt returns the code for a step.
func CodeAt(secret string, step int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", Digits, n%1_000_000), nil
}

// Verify checks a code against the current step and one step either side
// (clock drift). It returns the matching step so callers can refuse reuse.
func Verify(secret, code string, now time.Time) (int64, bool) {
	code = Normalize(code)
	if len(code) != Digits {
		return 0, false
	}
	cur := Step(now)
	for _, s := range []int64{cur, cur - 1, cur + 1} {
		want, err := CodeAt(secret, s)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

// Normalize drops spaces and dashes people type or paste.
func Normalize(code string) string {
	return strings.NewReplacer(" ", "", "-", "", " ", "").Replace(strings.TrimSpace(code))
}

// URI is the otpauth:// link an authenticator app reads from the QR code.
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}
