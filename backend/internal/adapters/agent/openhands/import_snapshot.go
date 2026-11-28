package openhands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"
)

const (
	openHandsNativeBytes = int64(4 << 30)
	openHandsStateBytes  = int64(64 << 20)
)

type openHandsContextReader struct {
	ctx context.Context
	in  io.Reader
}

func (r openHandsContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.in.Read(p)
}

func openHandsFilesFingerprint(files map[string]string) string {
	raw, _ := json.Marshal(files) // String keys/values always marshal; keys are sorted.
	sum := sha256.Sum256(raw)
	return "openhands:v1:" + hex.EncodeToString(sum[:])
}

// ConversationFingerprint hashes all native state and events, including their
// relative names. It is selection evidence, not a stat-only cache key.
func ConversationFingerprint(ctx context.Context, root, nativeID string) (string, error) {
	paths, err := ConversationFiles(ctx, root, nativeID)
	if err != nil {
		return "", err
	}
	id, err := uuid.Parse(nativeID)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, strings.ReplaceAll(id.String(), "-", ""))
	provider, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = provider.Close() }()
	remaining := openHandsNativeBytes
	hashes := make(map[string]string, len(paths))
	for _, path := range paths {
		rel, err := filepath.Rel(dir, path)
		if err != nil || !filepath.IsLocal(rel) {
			return "", errors.New("invalid openhands native file")
		}
		digest, err := hashOpenHandsFile(ctx, provider, rel, &remaining)
		if err != nil {
			return "", err
		}
		hashes[rel] = digest
	}
	fresh, err := ConversationFiles(ctx, root, nativeID)
	if err != nil {
		return "", err
	}
	if !slices.Equal(paths, fresh) {
		return "", errors.New("openhands native event list changed; rescan")
	}
	// SDK events are append-only. Recheck the mutable saved head after hashing
	// those events, so a concurrent native turn cannot publish a mixed snapshot.
	remaining = openHandsStateBytes
	state, err := hashOpenHandsFile(ctx, provider, "base_state.json", &remaining)
	if err != nil {
		return "", err
	}
	if state != hashes["base_state.json"] {
		return "", errors.New("openhands native state changed; rescan")
	}
	return openHandsFilesFingerprint(hashes), nil
}

func hashOpenHandsFile(ctx context.Context, root *os.Root, name string, remaining *int64) (string, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("openhands native file must be regular")
	}
	file, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(openHandsContextReader{ctx: ctx, in: file}, *remaining+1))
	if err != nil {
		return "", err
	}
	if n > *remaining {
		return "", errors.New("openhands native conversation is too large")
	}
	*remaining -= n
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readOpenHandsState(ctx context.Context, root *os.Root) ([]byte, error) {
	file, err := root.Open("base_state.json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(openHandsContextReader{ctx: ctx, in: file}, openHandsStateBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > openHandsStateBytes {
		return nil, errors.New("openhands state is too large")
	}
	return raw, nil
}
