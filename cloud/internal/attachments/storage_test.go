package attachments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func testImage(t *testing.T) ([]byte, Metadata) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	sum := sha256.Sum256(data)
	return data, Metadata{Filename: "image.png", Size: int64(len(data)), MIMEType: "image/png", SHA256: hex.EncodeToString(sum[:])}
}
func TestVerifyImage(t *testing.T) {
	data, m := testImage(t)
	if _, err := Verify(bytes.NewReader(data), m); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		data []byte
		m    Metadata
	}{
		"checksum": {append([]byte{}, data...), m}, "size": {append(data, 0), m},
		"mime": {data, m}, "svg": {[]byte("<svg/>"), m}, "truncated": {data[:33], m},
	}
	c := cases["checksum"]
	c.m.SHA256 = strings.Repeat("0", 64)
	cases["checksum"] = c
	c = cases["mime"]
	c.m.MIMEType = "image/jpeg"
	cases["mime"] = c
	for _, name := range []string{"svg", "truncated"} {
		c = cases[name]
		sum := sha256.Sum256(c.data)
		c.m.SHA256 = hex.EncodeToString(sum[:])
		c.m.Size = int64(len(c.data))
		cases[name] = c
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Verify(bytes.NewReader(c.data), c.m); err == nil {
				t.Fatal("accepted invalid image")
			}
		})
	}
	for _, mime := range []string{"image/svg+xml", "application/pdf", "text/plain"} {
		m.MIMEType = mime
		if ValidateMetadata(m) == nil {
			t.Fatal("accepted", mime)
		}
	}
	m.MIMEType = "image/png"
	m.Size = MaxBytes + 1
	if ValidateMetadata(m) == nil {
		t.Fatal("accepted oversized image")
	}
}
func TestFilesystemHTTPGrants(t *testing.T) {
	ctx := context.Background()
	data, m := testImage(t)
	f, err := NewFilesystem("test", t.TempDir(), bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	server := httptest.NewServer(f)
	defer server.Close()
	upload, err := f.Upload(ctx, "upload-id", m)
	if err != nil {
		t.Fatal(err)
	}
	post := func(url string, body []byte) int {
		t.Helper()
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		part, _ := w.CreateFormFile("file", "image.png")
		part.Write(body)
		w.Close()
		r, err := http.Post(server.URL+url, w.FormDataContentType(), &b)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		return r.StatusCode
	}
	if status := post(upload.URL, data); status != 204 {
		t.Fatal("upload", status)
	}
	if status := post(upload.URL, append(data, 0)); status != 422 {
		t.Fatal("size constraint", status)
	}
	if err := f.PutVerified(ctx, "image-id", m, data); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(ctx, "upload-id"); err != nil {
		t.Fatal(err)
	}
	// A grant can only recreate its temporary key, never the canonical object.
	if status := post(upload.URL, data); status != 204 {
		t.Fatal("retry", status)
	}
	read, _ := f.Read(ctx, "image-id")
	r, err := http.Get(server.URL + read.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("download differs")
	}
	expired, _ := f.token(fileGrant{Key: "upload-id", Metadata: m, Expires: time.Now().Add(-time.Minute).Unix()})
	if status := post(StorageRoute+expired, data); status != 403 {
		t.Fatal("expired", status)
	}
	if status := post(upload.URL+"tamper", data); status != 403 {
		t.Fatal("tampering", status)
	}
	if _, err := NewFilesystem("production", t.TempDir(), bytes.Repeat([]byte{7}, 32)); err == nil {
		t.Fatal("production enabled filesystem")
	}
	if err := f.PutVerified(ctx, "../escape", m, data); err == nil {
		t.Fatal("key escaped")
	}
}

type testCredentials struct{}

func (testCredentials) Retrieve(context.Context) (aws.Credentials, error) {
	return aws.Credentials{AccessKeyID: "test-access", SecretAccessKey: "test-secret", SessionToken: "test-session"}, nil
}
func TestS3POSTPolicy(t *testing.T) {
	_, m := testImage(t)
	client := s3.NewFromConfig(aws.Config{Region: "eu-north-1", Credentials: testCredentials{}})
	storage := &S3{client: client, presign: s3.NewPresignClient(client), bucket: "private-images"}
	grant, err := storage.Upload(context.Background(), "upload-id", m)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := base64.StdEncoding.DecodeString(grant.Fields["policy"])
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Expiration time.Time         `json:"expiration"`
		Conditions []json.RawMessage `json:"conditions"`
	}
	if err := json.Unmarshal(policy, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Expiration.Before(time.Now().Add(9*time.Minute)) || decoded.Expiration.After(time.Now().Add(11*time.Minute)) {
		t.Fatal("upload expiry")
	}
	if !grant.ExpiresAt.Equal(decoded.Expiration) {
		t.Fatalf("reported deadline differs from signed policy: grant=%v policy=%v", grant.ExpiresAt, decoded.Expiration)
	}
	text := string(policy)
	for _, field := range []string{"content-length-range", `"key":"upload-id"`, `"Content-Type":"image/png"`, `"x-amz-checksum-sha256"`, `"x-amz-server-side-encryption":"AES256"`} {
		if !strings.Contains(text, field) {
			t.Errorf("policy lacks %s: %s", field, text)
		}
	}
	if grant.Fields["key"] != "upload-id" || grant.Fields["x-amz-checksum-sha256"] == "" || grant.Fields["X-Amz-Signature"] == "" {
		t.Fatal("missing signed fields")
	}
	read, err := storage.Read(context.Background(), "image-id")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read.URL, "X-Amz-Expires=300") {
		t.Fatal("read expiry")
	}
}
