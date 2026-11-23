package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Web Push (RFC 8030) with VAPID (RFC 8292) and aes128gcm payload
// encryption (RFC 8291), using only the standard library. Works with the
// push services behind Safari/iOS, Chrome and Firefox.

var b64 = base64.RawURLEncoding

// ErrSubscriptionGone means the push service no longer knows the
// subscription (the app was removed or notifications were turned off).
var ErrSubscriptionGone = errors.New("push subscription expired")

// PushSubscription is what the browser's PushManager hands out.
type PushSubscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"` // base64url, 65-byte uncompressed P-256 point
	Auth     string `json:"auth"`   // base64url, 16 bytes
}

// PushPayload is what the service worker shows.
type PushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"` // a newer notification with the same tag replaces the older one
	Ack   string `json:"ack,omitempty"` // acknowledge page (an action button where the device supports it)
}

// VAPIDKeys sign requests to push services.
type VAPIDKeys struct {
	Private *ecdsa.PrivateKey
	Subject string // mailto: or https: contact for the push service
}

// PublicKey is the application server key the browser subscribes with.
func (k VAPIDKeys) PublicKey() string {
	pub, _ := k.Private.PublicKey.ECDH()
	return b64.EncodeToString(pub.Bytes())
}

// GenerateVAPID makes a new P-256 key pair.
func GenerateVAPID() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// encodeVAPIDPrivate / decodeVAPIDPrivate store the key as base64url of the raw scalar.
func encodeVAPIDPrivate(k *ecdsa.PrivateKey) string {
	b, _ := k.Bytes()
	return b64.EncodeToString(b)
}

func decodeVAPIDPrivate(s string) (*ecdsa.PrivateKey, error) {
	raw, err := b64.DecodeString(s)
	if err != nil {
		return nil, err
	}
	return ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
}

// vapidJWT signs the ES256 token for one push service origin.
func vapidJWT(k VAPIDKeys, audience string, exp time.Time) (string, error) {
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"aud": audience, "exp": exp.Unix(), "sub": k.Subject})
	signing := header + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, k.Private, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + b64.EncodeToString(sig), nil
}

// verifyVAPIDJWT checks a token's ES256 signature (tests and sanity checks).
func verifyVAPIDJWT(token string, pub *ecdsa.PublicKey) bool {
	i := strings.LastIndexByte(token, '.')
	if i < 0 {
		return false
	}
	sig, err := b64.DecodeString(token[i+1:])
	if err != nil || len(sig) != 64 {
		return false
	}
	sum := sha256.Sum256([]byte(token[:i]))
	return ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
}

// encryptPayload encrypts plaintext for one subscriber (RFC 8291) as a
// single aes128gcm record. asPriv and salt are random per message; they're
// parameters so tests can use the RFC's fixed values.
func encryptPayload(plaintext []byte, uaPublic, authSecret []byte, asPriv *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	curve := ecdh.P256()
	ua, err := curve.NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("bad subscription key: %w", err)
	}
	if len(authSecret) != 16 {
		return nil, errors.New("bad subscription auth secret")
	}
	if len(salt) != 16 {
		return nil, errors.New("salt must be 16 bytes")
	}
	shared, err := asPriv.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPublic := asPriv.PublicKey().Bytes()

	// IKM = HKDF(auth_secret, ecdh_secret, "WebPush: info\0" || ua_public || as_public, 32)
	keyInfo := append(append([]byte("WebPush: info\x00"), uaPublic...), asPublic...)
	ikm, err := hkdf.Key(sha256.New, shared, authSecret, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// One record: the plaintext, then the 0x02 "last record" delimiter.
	record := append(append([]byte{}, plaintext...), 0x02)

	const rs = 4096
	if len(record)+16 > rs {
		return nil, errors.New("payload too large")
	}
	var header bytes.Buffer
	header.Write(salt)
	_ = binary.Write(&header, binary.BigEndian, uint32(rs))
	header.WriteByte(byte(len(asPublic)))
	header.Write(asPublic)
	return gcm.Seal(header.Bytes(), nonce, record, nil), nil
}

// decryptPayload reverses encryptPayload with the subscriber's private
// key (tests only: in real life the browser does this).
func decryptPayload(body []byte, uaPriv *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("too short")
	}
	salt := body[:16]
	idLen := int(body[20])
	if len(body) < 21+idLen {
		return nil, errors.New("too short")
	}
	as, err := ecdh.P256().NewPublicKey(body[21 : 21+idLen])
	if err != nil {
		return nil, err
	}
	shared, err := uaPriv.ECDH(as)
	if err != nil {
		return nil, err
	}
	keyInfo := append(append([]byte("WebPush: info\x00"), uaPriv.PublicKey().Bytes()...), as.Bytes()...)
	ikm, _ := hkdf.Key(sha256.New, shared, authSecret, string(keyInfo), 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idLen:], nil)
	if err != nil {
		return nil, err
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		return nil, errors.New("missing record delimiter")
	}
	return plain[:len(plain)-1], nil
}

var pushClient = &http.Client{Timeout: 20 * time.Second}

// SendPush delivers one encrypted notification. urgent asks the push
// service to deliver right away (down alerts); topic collapses older
// undelivered notifications with the same topic.
func SendPush(ctx context.Context, keys VAPIDKeys, sub PushSubscription, payload PushPayload, urgent bool, topic string) error {
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("invalid push endpoint")
	}
	uaPublic, err := b64.DecodeString(strings.TrimRight(sub.P256dh, "="))
	if err != nil {
		return fmt.Errorf("bad subscription key: %w", err)
	}
	auth, err := b64.DecodeString(strings.TrimRight(sub.Auth, "="))
	if err != nil {
		return fmt.Errorf("bad subscription auth: %w", err)
	}
	plain, _ := json.Marshal(payload)
	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	body, err := encryptPayload(plain, uaPublic, auth, asPriv, salt)
	if err != nil {
		return err
	}
	jwt, err := vapidJWT(keys, u.Scheme+"://"+u.Host, time.Now().Add(12*time.Hour))
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+keys.PublicKey())
	if urgent {
		req.Header.Set("Urgency", "high")
	} else {
		req.Header.Set("Urgency", "normal")
	}
	if topic = pushTopic(topic); topic != "" {
		req.Header.Set("Topic", topic)
	}
	resp, err := pushClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return ErrSubscriptionGone
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// pushTopic keeps a topic within the RFC 8030 rules: at most 32
// characters from the URL-safe base64 alphabet.
func pushTopic(s string) string {
	var b strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			b.WriteRune(c)
		}
		if b.Len() == 32 {
			break
		}
	}
	return b.String()
}
