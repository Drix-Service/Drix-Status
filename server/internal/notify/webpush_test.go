package notify

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// RFC 8291, Section 5 / Appendix A: fixed keys and salt give a known message.
func TestEncryptPayloadRFC8291(t *testing.T) {
	asPriv, err := ecdh.P256().NewPrivateKey(mustB64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	if got := b64.EncodeToString(asPriv.PublicKey().Bytes()); got != "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8" {
		t.Fatalf("as_public %s", got)
	}
	uaPublic := mustB64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	auth := mustB64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := mustB64(t, "DGv6ra1nlYgDCS1FRnbzlw")

	out, err := encryptPayload([]byte("When I grow up, I want to be a watermelon"), uaPublic, auth, asPriv, salt)
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if got := b64.EncodeToString(out); got != want {
		t.Fatalf("ciphertext\n got %s\nwant %s", got, want)
	}

	// And the receiver's side decrypts it.
	uaPriv, _ := ecdh.P256().NewPrivateKey(mustB64(t, "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"))
	plain, err := decryptPayload(out, uaPriv, auth)
	if err != nil || string(plain) != "When I grow up, I want to be a watermelon" {
		t.Fatalf("%q %v", plain, err)
	}
}

func TestVAPIDJWT(t *testing.T) {
	k, err := GenerateVAPID()
	if err != nil {
		t.Fatal(err)
	}
	keys := VAPIDKeys{Private: k, Subject: "https://admin.example.com"}
	tok, err := vapidJWT(keys, "https://web.push.apple.com", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !verifyVAPIDJWT(tok, &k.PublicKey) {
		t.Fatal("signature doesn't verify")
	}
	parts := strings.Split(tok, ".")
	var claims map[string]any
	_ = json.Unmarshal(mustB64(t, parts[1]), &claims)
	if claims["aud"] != "https://web.push.apple.com" || claims["sub"] != "https://admin.example.com" || claims["exp"] != 1700000000.0 {
		t.Fatal(claims)
	}
	// The stored form round-trips.
	k2, err := decodeVAPIDPrivate(encodeVAPIDPrivate(k))
	if err != nil || !k2.Equal(k) || len(mustB64(t, keys.PublicKey())) != 65 {
		t.Fatal("key encoding")
	}
}

// A fake push service: checks the headers, decrypts the body like a browser.
func TestSendPushEndToEnd(t *testing.T) {
	uaPriv, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	k, _ := GenerateVAPID()
	keys := VAPIDKeys{Private: k, Subject: "https://admin.example.com"}

	var got PushPayload
	var hdr http.Header
	status := http.StatusCreated
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		plain, err := decryptPayload(body, uaPriv, auth)
		if err != nil {
			t.Errorf("decrypt: %v", err)
		}
		_ = json.Unmarshal(plain, &got)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	old := pushClient
	pushClient = srv.Client()
	defer func() { pushClient = old }()

	sub := PushSubscription{Endpoint: srv.URL + "/push/abc", P256dh: b64.EncodeToString(uaPriv.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}
	p := PushPayload{Title: "API is down", Body: "HTTP 503", URL: "/m/monitors/4", Tag: "monitor-4"}
	if err := SendPush(context.Background(), keys, sub, p, true, "monitor-4 down!"); err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Fatalf("payload %+v", got)
	}
	if hdr.Get("Content-Encoding") != "aes128gcm" || hdr.Get("Urgency") != "high" || hdr.Get("Topic") != "monitor-4down" ||
		hdr.Get("TTL") == "" || !strings.HasPrefix(hdr.Get("Authorization"), "vapid t=") {
		t.Fatal(hdr)
	}
	tok := strings.TrimPrefix(strings.Split(hdr.Get("Authorization"), ",")[0], "vapid t=")
	if !verifyVAPIDJWT(tok, &k.PublicKey) {
		t.Fatal("jwt")
	}

	// 410 Gone means the subscription should be removed.
	status = http.StatusGone
	if err := SendPush(context.Background(), keys, sub, p, false, ""); err != ErrSubscriptionGone {
		t.Fatalf("got %v", err)
	}
}
