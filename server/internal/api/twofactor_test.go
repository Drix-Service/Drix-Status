package api

import (
	"regexp"
	"testing"
)

func TestRecoveryCodes(t *testing.T) {
	codes, hashes := newRecoveryCodes()
	if len(codes) != recoveryCount || len(hashes) != recoveryCount {
		t.Fatalf("got %d codes", len(codes))
	}
	re := regexp.MustCompile(`^[a-z2-9]{4}-[a-z2-9]{4}$`)
	seen := map[string]bool{}
	for i, c := range codes {
		if !re.MatchString(c) {
			t.Errorf("code %q has the wrong shape", c)
		}
		if seen[c] {
			t.Errorf("duplicate code %q", c)
		}
		seen[c] = true
		if hashes[i] != recoveryHash(c) {
			t.Errorf("hash mismatch for %q", c)
		}
	}
	// People type codes in other ways; they must still match.
	if recoveryHash("ABCD-EFGH") != recoveryHash(" abcdefgh ") || recoveryHash("abcd efgh") != recoveryHash("abcd-efgh") {
		t.Error("recovery codes must ignore case, dashes and spaces")
	}
	if recoveryHash("abcd-efgh") == recoveryHash("abcd-efgi") {
		t.Error("different codes must not match")
	}
}

func TestUserHandleIsStable(t *testing.T) {
	if string(userHandle(42)) != string(userHandle(42)) || string(userHandle(42)) == string(userHandle(43)) {
		t.Fatal("user handles must be stable and unique")
	}
}
