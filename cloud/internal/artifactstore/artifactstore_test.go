package artifactstore_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/artifactstore"
	"github.com/aoagents/agent-orchestrator/cloud/internal/artifactstore/s3fake"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func TestKey(t *testing.T) {
	key, err := artifactstore.Key("org", "session", "a/b.html")
	if err != nil || key != "orgs/org/sessions/session/a/b.html" {
		t.Fatalf("Key = %q, %v", key, err)
	}
	if _, err := artifactstore.Key("", "session", "a"); err == nil {
		t.Fatal("Key accepted an empty organization")
	}
	if _, err := artifactstore.Key("org", "", "a"); err == nil {
		t.Fatal("Key accepted an empty session")
	}
	if _, err := artifactstore.Key("org", "session", strings.Repeat("a", 1024)); !errors.Is(err, domain.ErrInvalidArtifactPath) {
		t.Fatalf("Key accepted an over-long key: %v", err)
	}
}

func TestKindAndContentType(t *testing.T) {
	cases := map[string][2]string{
		"a.HTML":   {artifactstore.KindHTML, "text/html; charset=utf-8"},
		"a.md":     {artifactstore.KindMarkdown, "text/markdown; charset=utf-8"},
		"a.png":    {artifactstore.KindFile, "image/png"},
		"a.pdf":    {artifactstore.KindFile, "application/pdf"},
		"a.txt":    {artifactstore.KindFile, "text/plain; charset=utf-8"},
		"noext":    {artifactstore.KindFile, "application/octet-stream"},
		"data.bin": {artifactstore.KindFile, "application/octet-stream"},
	}
	for path, want := range cases {
		if kind := artifactstore.KindOf(path); kind != want[0] {
			t.Errorf("KindOf(%q) = %q, want %q", path, kind, want[0])
		}
		if contentType := artifactstore.ContentType(path); contentType != want[1] {
			t.Errorf("ContentType(%q) = %q, want %q", path, contentType, want[1])
		}
	}
}

func TestStoreAgainstFake(t *testing.T) {
	fake := s3fake.New(t)
	exerciseStore(t, fake.Config(), func(key string) int { return fake.Versions(key) })
}

// TestStoreAgainstMinIO runs the same checks against a real S3-compatible
// server. Set AO_CLOUD_TEST_ARTIFACT_ENDPOINT (and AWS credentials) to a MinIO
// with AO_CLOUD_TEST_ARTIFACT_BUCKET created and versioning enabled.
func TestStoreAgainstMinIO(t *testing.T) {
	endpoint := os.Getenv("AO_CLOUD_TEST_ARTIFACT_ENDPOINT")
	bucket := os.Getenv("AO_CLOUD_TEST_ARTIFACT_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("AO_CLOUD_TEST_ARTIFACT_ENDPOINT and AO_CLOUD_TEST_ARTIFACT_BUCKET are not set")
	}
	exerciseStore(t, artifactstore.Config{Bucket: bucket, Region: "us-east-1", Endpoint: endpoint}, nil)
}

func exerciseStore(t *testing.T, cfg artifactstore.Config, versions func(string) int) {
	t.Helper()
	ctx := context.Background()
	store, err := artifactstore.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	key, err := artifactstore.Key("org-"+t.Name(), "session", "report.html")
	if err != nil {
		t.Fatal(err)
	}
	first := []byte("<html>v1</html>")
	second := []byte("<html>version two</html>")
	sum := func(body []byte) []byte {
		value := sha256.Sum256(body)
		return value[:]
	}

	firstVersion, err := store.Put(ctx, key, bytes.NewReader(first), int64(len(first)), sum(first))
	if err != nil {
		t.Fatalf("put first: %v", err)
	}
	if _, err := store.Put(ctx, key, bytes.NewReader(second), int64(len(second)), sum(first)); !errors.Is(err, artifactstore.ErrChecksumMismatch) {
		t.Fatalf("mismatched put err = %v, want ErrChecksumMismatch", err)
	}
	if versions != nil && versions(key) != 1 {
		t.Fatalf("mismatched put created a version: %d versions", versions(key))
	}
	secondVersion, err := store.Put(ctx, key, bytes.NewReader(second), int64(len(second)), sum(second))
	if err != nil {
		t.Fatalf("put second: %v", err)
	}
	if secondVersion == firstVersion {
		t.Fatal("second put reused the first version id")
	}

	object, err := store.Get(ctx, key, firstVersion)
	if err != nil {
		t.Fatalf("get first version: %v", err)
	}
	body, _ := io.ReadAll(object.Body)
	_ = object.Body.Close()
	if !bytes.Equal(body, first) {
		t.Fatalf("first version body = %q", body)
	}

	head, err := store.Head(ctx, key, secondVersion)
	if err != nil {
		t.Fatalf("head second version: %v", err)
	}
	if head.Size != int64(len(second)) || !bytes.Equal(head.SHA256, sum(second)) {
		t.Fatalf("head = %+v", head)
	}

	if err := store.DeleteVersion(ctx, key, firstVersion); err != nil {
		t.Fatalf("delete first version: %v", err)
	}
	if _, err := store.Get(ctx, key, firstVersion); !errors.Is(err, artifactstore.ErrNotFound) {
		t.Fatalf("get deleted version err = %v, want ErrNotFound", err)
	}
	if _, err := store.Head(ctx, key, firstVersion); !errors.Is(err, artifactstore.ErrNotFound) {
		t.Fatalf("head deleted version err = %v, want ErrNotFound", err)
	}
	if err := store.DeleteVersion(ctx, key, secondVersion); err != nil {
		t.Fatalf("delete second version: %v", err)
	}
}

func TestContentTypeUsesAFixedList(t *testing.T) {
	cases := map[string]string{
		"report.HTML":  "text/html; charset=utf-8",
		"notes.md":     "text/markdown; charset=utf-8",
		"chart.svg":    "image/svg+xml",
		"demo.mp4":     "video/mp4",
		"setup.exe":    "application/octet-stream",
		"archive.zip":  "application/octet-stream",
		"no-extension": "application/octet-stream",
	}
	for path, want := range cases {
		if got := artifactstore.ContentType(path); got != want {
			t.Errorf("ContentType(%q) = %q, want %q", path, got, want)
		}
	}
}
