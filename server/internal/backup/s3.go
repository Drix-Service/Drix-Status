package backup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A tiny S3 client: PUT, list and DELETE with AWS Signature Version 4.
// It works with AWS S3, Cloudflare R2, Backblaze B2, MinIO and others.

var s3Client = &http.Client{Timeout: 30 * time.Minute}

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

type s3Target struct {
	cfg  S3Settings
	base *url.URL // scheme://host[/bucket]
}

func newS3(cfg S3Settings) (*s3Target, error) {
	if cfg.Bucket == "" || cfg.AccessKey == "" || cfg.Secret == "" {
		return nil, errors.New("S3 bucket, access key and secret are required")
	}
	ep := strings.TrimRight(cfg.Endpoint, "/")
	if ep == "" {
		region := cfg.Region
		if region == "" {
			region = "us-east-1"
		}
		ep = "https://s3." + region + ".amazonaws.com"
	}
	u, err := url.Parse(ep)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("invalid S3 endpoint %q", cfg.Endpoint)
	}
	u.Path = ""
	if strings.HasSuffix(u.Hostname(), ".amazonaws.com") {
		u.Host = cfg.Bucket + "." + u.Host // AWS: virtual-hosted style
	} else {
		u.Path = "/" + cfg.Bucket // others: path style
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1" // R2 uses "auto", MinIO accepts anything
	}
	return &s3Target{cfg: cfg, base: u}, nil
}

func (t *s3Target) key(name string) string {
	p := strings.Trim(t.cfg.Prefix, "/")
	if p == "" {
		return name
	}
	return p + "/" + name
}

func (t *s3Target) objectURL(key string) *url.URL {
	u := *t.base
	u.Path = strings.TrimRight(u.Path, "/") + "/" + key
	return &u
}

func (t *s3Target) do(ctx context.Context, method string, u *url.URL, body io.Reader, size int64, payloadHash string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
	}
	signV4(req, t.cfg.AccessKey, t.cfg.Secret, t.cfg.Region, payloadHash, time.Now().UTC())
	res, err := s3Client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("couldn't reach %s: %w", u.Host, err)
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		return nil, s3Error(res)
	}
	return res, nil
}

func s3Error(res *http.Response) error {
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<10))
	if xml.Unmarshal(raw, &e) == nil && e.Code != "" {
		return fmt.Errorf("S3 %d %s: %s", res.StatusCode, e.Code, e.Message)
	}
	return fmt.Errorf("S3 answered %s", res.Status)
}

func (s *Service) upload(ctx context.Context, cfg S3Settings, f File) error {
	t, err := newS3(cfg)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, f.Name)
	hash, err := fileSHA256(path)
	if err != nil {
		return err
	}
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	res, err := t.do(ctx, http.MethodPut, t.objectURL(t.key(f.Name)), fh, f.Size, hash)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

// TestS3 lists the bucket to check the settings work.
func (s *Service) TestS3(ctx context.Context, cfg S3Settings) error {
	t, err := newS3(cfg)
	if err != nil {
		return err
	}
	_, err = t.list(ctx)
	return err
}

func (t *s3Target) list(ctx context.Context) ([]string, error) {
	var keys []string
	token := ""
	for {
		u := *t.base
		if u.Path == "" {
			u.Path = "/"
		}
		q := url.Values{"list-type": {"2"}, "prefix": {t.key("drix-")}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		u.RawQuery = q.Encode()
		res, err := t.do(ctx, http.MethodGet, &u, nil, 0, emptySHA256)
		if err != nil {
			return nil, err
		}
		var out struct {
			Contents []struct {
				Key string `xml:"Key"`
			} `xml:"Contents"`
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
		}
		err = xml.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&out)
		res.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading S3 listing: %w", err)
		}
		for _, c := range out.Contents {
			keys = append(keys, c.Key)
		}
		if !out.IsTruncated || out.NextContinuationToken == "" {
			return keys, nil
		}
		token = out.NextContinuationToken
	}
}

func (s *Service) pruneRemote(ctx context.Context, cfg S3Settings, keep int) error {
	t, err := newS3(cfg)
	if err != nil {
		return err
	}
	keys, err := t.list(ctx)
	if err != nil {
		return err
	}
	var ours []string
	for _, k := range keys {
		if NamePattern.MatchString(k[strings.LastIndex(k, "/")+1:]) {
			ours = append(ours, k)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ours)))
	if keep < 1 {
		keep = 1
	}
	for _, k := range ours[min(keep, len(ours)):] {
		res, err := t.do(ctx, http.MethodDelete, t.objectURL(k), nil, 0, emptySHA256)
		if err != nil {
			return err
		}
		res.Body.Close()
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// signV4 adds AWS Signature Version 4 headers. Every header already on the
// request is signed, plus host, x-amz-date and x-amz-content-sha256.
func signV4(req *http.Request, accessKey, secretKey, region, payloadHash string, now time.Time) {
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	headers := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = strings.TrimSpace(strings.Join(v, ","))
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	signed := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.EscapedPath()),
		canonicalQuery(req.URL.Query()),
		canonHeaders.String(),
		signed,
		payloadHash,
	}, "\n")
	scope := day + "/" + region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hexSHA256(canonical)

	k := hmacSHA256([]byte("AWS4"+secretKey), day)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, "s3")
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func canonicalURI(p string) string {
	if p == "" {
		return "/"
	}
	parts := strings.Split(p, "/")
	for i, s := range parts {
		dec, err := url.PathUnescape(s)
		if err != nil {
			dec = s
		}
		parts[i] = awsEscape(dec)
	}
	return strings.Join(parts, "/")
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, awsEscape(k)+"="+awsEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// awsEscape percent-encodes everything except A-Z a-z 0-9 - _ . ~
func awsEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func hexSHA256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
