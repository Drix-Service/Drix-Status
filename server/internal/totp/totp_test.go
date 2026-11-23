package totp

import (
	"encoding/base32"
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA1 rows (8 digits there; the last 6 here).
func TestRFC6238Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for unix, want8 := range map[int64]string{
		59: "94287082", 1111111109: "07081804", 1111111111: "14050471",
		1234567890: "89005924", 2000000000: "69279037", 20000000000: "65353130",
	} {
		got, err := CodeAt(secret, unix/Period)
		if err != nil {
			t.Fatal(err)
		}
		if got != want8[2:] {
			t.Errorf("t=%d: got %s want %s", unix, got, want8[2:])
		}
	}
}

func TestVerifyWindow(t *testing.T) {
	s := NewSecret()
	now := time.Unix(1_800_000_000, 0)
	for drift, ok := range map[int64]bool{0: true, -1: true, 1: true, -2: false, 2: false} {
		code, _ := CodeAt(s, Step(now)+drift)
		if _, got := Verify(s, code, now); got != ok {
			t.Errorf("drift %d: got %v want %v", drift, got, ok)
		}
	}
	code, _ := CodeAt(s, Step(now))
	if _, ok := Verify(s, code[:3]+" "+code[3:], now); !ok {
		t.Error("a space in the middle should be accepted")
	}
	if _, ok := Verify(s, "12345", now); ok {
		t.Error("short code accepted")
	}
}
