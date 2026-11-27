package memcode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const memcodePreparationFile = ".ao-import.json"

type memcodePreparation struct {
	Source      string `json:"source"`
	ID          string `json:"id"`
	CWD         string `json:"cwd"`
	Fingerprint string `json:"fingerprint"`
}

func memcodeImportOrigin(source string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(source)))
	return hex.EncodeToString(sum[:]) + "\n"
}

func memcodePreparationFor(ctx context.Context, r ports.ImportedSessionRelocation) (memcodePreparation, error) {
	if err := ctx.Err(); err != nil {
		return memcodePreparation{}, err
	}
	cwd, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return memcodePreparation{}, err
	}
	return memcodePreparation{Source: filepath.Clean(r.TranscriptPath), ID: r.NativeSessionID, CWD: cwd, Fingerprint: r.SourceFingerprint}, nil
}

// Recovery reads only the private workspace; original availability is irrelevant
// once its exact selection, identity and native bytes have durable private proof.
func recoverMemcodePreparation(ctx context.Context, root *os.Root, r ports.ImportedSessionRelocation) (bool, error) {
	if r.SourceFingerprint == "" {
		return false, nil
	}
	dir := filepath.Join(".memcode", "sessions", r.NativeSessionID)
	raw, err := memcodeImportFile(ctx, root, filepath.Join(dir, memcodePreparationFile), 16<<10)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	want, err := memcodePreparationFor(ctx, r)
	if err != nil {
		return false, err
	}
	var got memcodePreparation
	if json.Unmarshal(raw, &got) != nil || got != want {
		return false, errors.New("memcode private preparation belongs to a different selection")
	}
	marker, err := memcodeImportFile(ctx, root, filepath.Join(dir, ".ao-import"), 128)
	if err != nil {
		return false, err
	}
	if string(marker) != memcodeImportOrigin(r.TranscriptPath) {
		return false, errors.New("memcode private preparation origin changed")
	}
	data, err := memcodeImportFile(ctx, root, filepath.Join(dir, "messages.json"), 16<<20)
	if err != nil {
		return false, err
	}
	if NativeHistoryFingerprint(data) != r.SourceFingerprint {
		return false, errors.New("memcode private preparation history changed")
	}
	if _, _, err := transcript(r.LaunchCWD, r.NativeSessionID); err != nil {
		return false, err
	}
	return true, nil
}

func writeMemcodePreparation(ctx context.Context, root *os.Root, r ports.ImportedSessionRelocation) error {
	if r.SourceFingerprint == "" {
		return nil
	}
	want, err := memcodePreparationFor(ctx, r)
	if err != nil {
		return err
	}
	data, err := json.Marshal(want)
	if err != nil {
		return err
	}
	if len(data) > 16<<10 {
		return errors.New("memcode private preparation exceeds read budget")
	}
	path := filepath.Join(".memcode", "sessions", r.NativeSessionID, memcodePreparationFile)
	if err := installMemcodeImport(ctx, root, path, data); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	verified, err := recoverMemcodePreparation(ctx, root, r)
	if err != nil {
		return err
	}
	if !verified {
		return errors.New("memcode private preparation was not published")
	}
	return nil
}
