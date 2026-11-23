package backup

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// AWS's published example: "GET Object" from the S3 Signature Version 4 docs.
func TestSignV4AWSExample(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	signV4(req, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", emptySHA256, now)
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization\n got %s\nwant %s", got, want)
	}
}

// AWS's "GET Bucket (List Objects)" example: query string signing.
func TestSignV4AWSListExample(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J", nil)
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	signV4(req, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", emptySHA256, now)
	if got := req.Header.Get("Authorization"); !strings.HasSuffix(got, "Signature=34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7") {
		t.Fatalf("authorization %s", got)
	}
}

func TestNamePattern(t *testing.T) {
	for name, want := range map[string]bool{
		"drix-20261122-030000.dump":            true,
		"drix-20261122-030000-pre-update.dump": true,
		"../drix-20261122-030000.dump":         false,
		"drix-20261122-030000.dump.partial":    false,
		"drix-latest.dump":                     false,
		"other.sql":                            false,
	} {
		if NamePattern.MatchString(name) != want {
			t.Errorf("%q: want %v", name, want)
		}
	}
}

func TestDue(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 22, h, m, 0, 0, time.UTC) }
	if Due(at(2, 59), "03:00") || !Due(at(3, 0), "03:00") || !Due(at(23, 0), "03:00") {
		t.Fatal("03:00 schedule")
	}
	if !Due(at(3, 5), "nonsense") {
		t.Fatal("bad time falls back to 03:00")
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"drix-20261118-030000.dump", "drix-20261119-030000.dump", "drix-20261120-030000-pre-update.dump",
		"drix-20261121-030000.dump", "drix-20261122-030000.dump", "notes.txt",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{dir: dir}
	if err := s.Prune(3); err != nil {
		t.Fatal(err)
	}
	files, _ := s.List()
	var got []string
	for _, f := range files {
		got = append(got, f.Name)
	}
	want := "drix-20261122-030000.dump drix-20261121-030000.dump drix-20261120-030000-pre-update.dump"
	if strings.Join(got, " ") != want {
		t.Fatalf("kept %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal("other files must be left alone")
	}
	if _, _, err := s.Open("../notes.txt"); err == nil {
		t.Fatal("open must refuse names that aren't backups")
	}
}
