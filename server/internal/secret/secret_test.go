package secret

import "testing"

func TestRoundTrip(t *testing.T) {
	b := New("test-key")
	s, err := b.Seal("https://discord.com/api/webhooks/123/abc")
	if err != nil {
		t.Fatal(err)
	}
	if s == "https://discord.com/api/webhooks/123/abc" {
		t.Fatal("not encrypted")
	}
	s2, _ := b.Seal("https://discord.com/api/webhooks/123/abc")
	if s == s2 {
		t.Fatal("nonce must make each seal unique")
	}
	got, err := b.Open(s)
	if err != nil || got != "https://discord.com/api/webhooks/123/abc" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := New("other-key").Open(s); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := b.Open(s[:len(s)-4] + "AAAA"); err == nil {
		t.Fatal("tampered value must fail")
	}
	var nilBox *Box
	if _, err := nilBox.Seal("x"); err != ErrNoKey {
		t.Fatal("nil box must report missing key")
	}
}
