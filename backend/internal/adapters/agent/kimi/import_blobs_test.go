package kimi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKimiMediaTotalBudget(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "blobs"), 0o700); err != nil {
		t.Fatal(err)
	}
	var wire strings.Builder
	data := make([]byte, kimiBlobFileBudget)
	for i := 0; i <= kimiBlobTotalBudget/kimiBlobFileBudget; i++ {
		if i == kimiBlobTotalBudget/kimiBlobFileBudget {
			data = data[:1]
		}
		data[0] = byte(i)
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(base64.StdEncoding.EncodeToString(data))))
		if err := os.WriteFile(filepath.Join(dir, "blobs", hash), data, 0o600); err != nil {
			t.Fatal(err)
		}
		record, err := json.Marshal(map[string]string{"url": "blobref:image/png;" + hash})
		if err != nil {
			t.Fatal(err)
		}
		wire.Write(record)
		wire.WriteByte('\n')
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if _, err := readKimiBlobs(t.Context(), root, ".", []byte(wire.String())); err == nil || !strings.Contains(err.Error(), "media blob") {
		t.Fatalf("aggregate payload limit ignored: %v", err)
	}
}

func TestKimiMediaReferenceCountAndPathValidation(t *testing.T) {
	refs := map[string]struct{}{}
	for i := 0; i <= kimiBlobCountBudget; i++ {
		url := fmt.Sprintf("blobref:image/png;%064x", i)
		err := collectKimiBlobRefs(map[string]any{"imageUrl": map[string]any{"url": url}}, refs)
		if (err != nil) != (i == kimiBlobCountBudget) {
			t.Fatalf("reference count %d: %v", i+1, err)
		}
	}
	for _, url := range []string{"blobref:image/png;../../outside", "blobref:image/png;", "blobref:invalid;" + strings.Repeat("a", 64), "blobref:image/png;" + strings.Repeat("A", 64)} {
		if err := collectKimiBlobRefs(map[string]any{"url": url}, map[string]struct{}{}); err == nil {
			t.Fatalf("unsafe blob reference accepted: %q", url)
		}
	}
}
