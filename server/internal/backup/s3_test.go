package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeBucket is a minimal S3 stand-in: PUT, ListObjectsV2 and DELETE under /bucket.
type fakeBucket struct {
	mu      sync.Mutex
	objects map[string][]byte
	t       *testing.T
}

func (b *fakeBucket) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") {
		http.Error(w, "<Error><Code>AccessDenied</Code><Message>no signature</Message></Error>", http.StatusForbidden)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/bucket/")
	switch {
	case r.Method == http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		if r.Header.Get("x-amz-content-sha256") != hex.EncodeToString(sum[:]) {
			http.Error(w, "<Error><Code>XAmzContentSHA256Mismatch</Code></Error>", http.StatusBadRequest)
			return
		}
		b.objects[key] = body
	case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		prefix := r.URL.Query().Get("prefix")
		var keys []string
		for k := range b.objects {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		fmt.Fprint(w, "<ListBucketResult>")
		for _, k := range keys {
			fmt.Fprintf(w, "<Contents><Key>%s</Key></Contents>", k)
		}
		fmt.Fprint(w, "<IsTruncated>false</IsTruncated></ListBucketResult>")
	case r.Method == http.MethodDelete:
		delete(b.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unexpected", http.StatusBadRequest)
	}
}

func TestUploadAndPruneRemote(t *testing.T) {
	fb := &fakeBucket{objects: map[string][]byte{
		"backups/drix-20261101-030000.dump": []byte("old1"),
		"backups/drix-20261102-030000.dump": []byte("old2"),
		"backups/unrelated.txt":             []byte("keep me"),
	}, t: t}
	srv := httptest.NewServer(fb)
	defer srv.Close()

	dir := t.TempDir()
	name := "drix-20261122-030000.dump"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("dump bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Service{dir: dir}
	cfg := S3Settings{Endpoint: srv.URL, Region: "auto", Bucket: "bucket", Prefix: "backups", AccessKey: "AK", Secret: "SK"}
	ctx := context.Background()
	if err := s.TestS3(ctx, cfg); err != nil {
		t.Fatal("test connection:", err)
	}
	if err := s.upload(ctx, cfg, File{Name: name, Size: 10}); err != nil {
		t.Fatal("upload:", err)
	}
	if got := string(fb.objects["backups/"+name]); got != "dump bytes" {
		t.Fatalf("uploaded %q", got)
	}
	if err := s.pruneRemote(ctx, cfg, 2); err != nil {
		t.Fatal("prune:", err)
	}
	var keys []string
	for k := range fb.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := "backups/drix-20261102-030000.dump backups/drix-20261122-030000.dump backups/unrelated.txt"
	if strings.Join(keys, " ") != want {
		t.Fatalf("bucket now has %v", keys)
	}

	bad := cfg
	bad.AccessKey = "WRONG"
	if err := s.TestS3(ctx, bad); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("want AccessDenied, got %v", err)
	}
}

func TestAWSUsesVirtualHostedStyle(t *testing.T) {
	tg, err := newS3(S3Settings{Region: "eu-central-1", Bucket: "acme", AccessKey: "a", Secret: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if got := tg.objectURL("drix/x.dump").String(); got != "https://acme.s3.eu-central-1.amazonaws.com/drix/x.dump" {
		t.Fatal(got)
	}
	tg, _ = newS3(S3Settings{Endpoint: "https://acc.r2.cloudflarestorage.com", Region: "auto", Bucket: "acme", AccessKey: "a", Secret: "b"})
	if got := tg.objectURL("x.dump").String(); got != "https://acc.r2.cloudflarestorage.com/acme/x.dump" {
		t.Fatal(got)
	}
}
