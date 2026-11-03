package primeagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const maxPrimeHistoryBytes = 128 << 20

type primeNativeHistory struct {
	id     string
	cwd    string
	parent string
	digest [sha256.Size]byte
	count  int
	opened bool
}

func validPrimeNativeID(id string) bool {
	return id != "" && id != "." && id != ".." && id == strings.TrimSpace(id) && !strings.ContainsAny(id, "/\\")
}

func primeSessionFiles(ctx context.Context, root string) (map[string]bool, error) {
	files := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") {
			if len(files) >= 10000 {
				return errors.New("prime native session inventory exceeds the limit")
			}
			files[path] = true
		}
		return nil
	})
	return files, err
}

// readPrimeNativeHistory fingerprints provider records, not display text.
// Native fork drops git_state and rewires parentId around those entries. A
// prefix limit permits the provider to append initialization/later native rows.
func readPrimeNativeHistory(ctx context.Context, root, path string, prefix int) (primeNativeHistory, error) {
	var history primeNativeHistory
	provider, err := os.OpenRoot(root)
	if err != nil {
		return history, err
	}
	defer func() { _ = provider.Close() }()
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return history, errors.New("prime native history is outside its state root")
	}
	info, err := provider.Stat(rel)
	if err != nil {
		return history, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxPrimeHistoryBytes {
		return history, errors.New("prime native history is not a bounded regular file")
	}
	f, err := provider.Open(rel)
	if err != nil {
		return history, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxPrimeHistoryBytes+1))
	if err != nil {
		return history, err
	}
	if len(data) > maxPrimeHistoryBytes {
		return history, errors.New("prime native history exceeds the size limit")
	}
	// Resolve only parents removed by the provider's native fork. All other
	// ancestry and every retained record field participate in the digest.
	dropped := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	for {
		if err := ctx.Err(); err != nil {
			return history, err
		}
		var row map[string]any
		if err := decoder.Decode(&row); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break // Native fork skips a torn final row without repairing it.
			}
			return history, err
		}
		typ, _ := row["type"].(string)
		if history.id == "" {
			history.id, _ = row["id"].(string)
			history.cwd, _ = row["cwd"].(string)
			history.parent, _ = row["parentSession"].(string)
			if typ != "session" || !validPrimeNativeID(history.id) || history.cwd == "" || row["version"] != json.Number("3") {
				return history, errors.New("prime native history requires a version 3 header")
			}
			continue
		}
		if typ == "session" {
			return history, errors.New("prime native history repeats its header")
		}
		if typ == "git_state" {
			id, _ := row["id"].(string)
			if id != "" {
				dropped[id] = row["parentId"]
			}
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	hash := sha256.New()
	for {
		if err := ctx.Err(); err != nil {
			return history, err
		}
		var row map[string]any
		if err := decoder.Decode(&row); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return history, err
		}
		if row["type"] == "session" || row["type"] == "git_state" {
			continue
		}
		if prefix > 0 && history.count >= prefix {
			state, _ := row["state"].(map[string]any)
			if row["type"] == "session_state" && state["status"] == "active" {
				history.opened = true
			}
			continue
		}
		parent, _ := row["parentId"].(string)
		if _, removed := dropped[parent]; removed {
			seen := map[string]bool{}
			for parent != "" && !seen[parent] {
				next, removed := dropped[parent]
				if !removed {
					break
				}
				seen[parent] = true
				parent, _ = next.(string)
			}
			if parent == "" {
				row["parentId"] = nil
			} else {
				row["parentId"] = parent
			}
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return history, err
		}
		_, _ = hash.Write(encoded)
		_, _ = hash.Write([]byte{'\n'})
		history.count++
	}
	if history.id == "" || history.count == 0 {
		return history, errors.New("prime native history is empty")
	}
	copy(history.digest[:], hash.Sum(nil))
	return history, nil
}

// LocateTranscript finds the exact native header, never a recent or partial id.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if !validPrimeNativeID(ref.NativeSessionID) || ref.ConfigDir == "" {
		return "", false, errors.New("prime native identity and state root are required")
	}
	files, err := primeSessionFiles(ctx, ref.ConfigDir)
	if err != nil {
		return "", false, err
	}
	var found string
	for path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if name != ref.NativeSessionID && !strings.HasSuffix(name, "_"+ref.NativeSessionID) {
			continue
		}
		history, err := readPrimeNativeHistory(ctx, ref.ConfigDir, path, 0)
		if err != nil || history.id != ref.NativeSessionID {
			continue
		}
		if found != "" {
			return "", false, errors.New("prime native identity is ambiguous")
		}
		found = path
	}
	return found, found != "", nil
}

// ProbeNativeSession requires a readable exact native history at the pinned root.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	_, found, err := p.LocateTranscript(ctx, ref)
	if errors.Is(err, os.ErrNotExist) {
		return ports.NativeSessionAvailabilityUnavailable, nil
	}
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if found {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// PrepareImportedSession validates and pins the source ref and AO launch cwd.
// The subsequent native --fork performs the copy; this method never writes it.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	ref := ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: r.ConfigDir}
	path, found, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ref, err
	}
	if !found || filepath.Clean(path) != filepath.Clean(r.TranscriptPath) {
		return ref, fmt.Errorf("%w: exact prime source history is unavailable", ports.ErrNativeHistoryUnavailable)
	}
	history, err := readPrimeNativeHistory(ctx, r.ConfigDir, path, 0)
	if err != nil {
		return ref, err
	}
	if !primeSamePath(history.cwd, r.SourceCWD) {
		return ref, errors.New("prime native source workspace changed")
	}
	launch, err := os.Stat(r.LaunchCWD)
	if err != nil || !launch.IsDir() {
		return ref, errors.New("prime native AO workspace is unavailable")
	}
	ref.TranscriptPath = path
	return ref, nil
}
