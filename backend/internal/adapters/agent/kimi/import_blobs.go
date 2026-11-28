package kimi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

const (
	kimiBlobFileBudget  = 16 << 20
	kimiBlobTotalBudget = 64 << 20
	kimiBlobCountBudget = 1024
)

func readKimiBlobs(ctx context.Context, root *os.Root, agentDir string, wire []byte) (map[string][]byte, error) {
	refs := map[string]struct{}{}
	scan := bufio.NewScanner(bytes.NewReader(wire))
	scan.Buffer(make([]byte, 4096), kimiImportBudget)
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(scan.Bytes())) == 0 {
			continue
		}
		var payload any
		decoder := json.NewDecoder(bytes.NewReader(scan.Bytes()))
		decoder.UseNumber() // Opaque numeric tokens need not fit in float64.
		if err := decoder.Decode(&payload); err != nil {
			return nil, err
		}
		if err := collectKimiBlobRefs(payload, refs); err != nil {
			return nil, err
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	blobs := make(map[string][]byte, len(refs))
	remaining := int64(kimiBlobTotalBudget)
	for key := range refs {
		data, err := readKimiFile(ctx, root, filepath.Join(agentDir, "blobs", key), min(int64(kimiBlobFileBudget), remaining))
		if err != nil {
			return nil, fmt.Errorf("kimi: invalid media blob: %w", err)
		}
		// 2.4.1 hashes the base64 text, then stores decoded bytes. Recreate its
		// canonical encoding without holding a second full payload in memory.
		sum := sha256.New()
		encoder := base64.NewEncoder(base64.StdEncoding, sum)
		_, _ = encoder.Write(data)
		_ = encoder.Close()
		if hex.EncodeToString(sum.Sum(nil)) != key {
			return nil, errors.New("kimi: media blob hash mismatch")
		}
		remaining -= int64(len(data))
		blobs[key] = data
	}
	return blobs, nil
}

func collectKimiBlobRefs(value any, refs map[string]struct{}) error {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if url, ok := child.(string); key == "url" && ok && strings.HasPrefix(url, "blobref:") {
				mediaType, hash, found := strings.Cut(strings.TrimPrefix(url, "blobref:"), ";")
				if !found || len(mediaType) > 255 || len(hash) != 64 {
					return errors.New("kimi: invalid media blob reference")
				}
				_, _, mediaErr := mime.ParseMediaType(mediaType)
				_, hashErr := hex.DecodeString(hash)
				if mediaErr != nil || !strings.Contains(mediaType, "/") || hashErr != nil || hash != strings.ToLower(hash) {
					return errors.New("kimi: invalid media blob reference")
				}
				refs[hash] = struct{}{}
				if len(refs) > kimiBlobCountBudget {
					return errors.New("kimi: too many media blob references")
				}
			}
			if err := collectKimiBlobRefs(child, refs); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := collectKimiBlobRefs(child, refs); err != nil {
				return err
			}
		}
	}
	return nil
}
