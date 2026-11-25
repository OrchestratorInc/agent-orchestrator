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
	"strings"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/importcopy"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)

type openHandsImportProof struct {
	Version      int    `json:"version"`
	NativeID     string `json:"native_id"`
	Source       string `json:"source"`
	Workspace    string `json:"workspace"`
	Copy         string `json:"copy"`
	SourceDigest string `json:"source_digest"`
	CopyDigest   string `json:"copy_digest"`
}

// PrepareImportedSession snapshots one conversation into AO-owned storage.
// An idle CLI need not hold an open state descriptor, so an open-file probe
// cannot establish exclusive ownership of the original conversation.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	var empty ports.NativeSessionRef
	id, err := uuid.Parse(r.NativeSessionID)
	if err != nil || r.Chat || !filepath.IsAbs(r.DataDir) || !filepath.IsAbs(r.ConfigDir) || !filepath.IsAbs(r.LaunchCWD) {
		return empty, errors.New("invalid openhands terminal import location or identity")
	}
	name := strings.ReplaceAll(id.String(), "-", "")
	if filepath.Clean(r.TranscriptPath) != filepath.Join(r.ConfigDir, name, "base_state.json") {
		return empty, errors.New("invalid openhands selected transcript")
	}
	data, err := filepath.EvalSymlinks(r.DataDir)
	if err != nil {
		return empty, err
	}
	// Never create staging files inside the provider's source store.
	source, err := filepath.EvalSymlinks(r.ConfigDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	if source == "" {
		source = filepath.Clean(r.ConfigDir) // Durable proof can outlive the source.
	}
	if rel, err := filepath.Rel(source, filepath.Join(data, "native-imports", "openhands")); err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return empty, errors.New("openhands private storage overlaps source")
	}
	parent := data
	for _, part := range []string{"native-imports", "openhands"} {
		parent = filepath.Join(parent, part)
		if err := os.Mkdir(parent, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return empty, err
		}
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return empty, errors.New("invalid openhands private storage directory")
		}
	}
	keyHash := sha256.Sum256([]byte(filepath.Clean(r.ConfigDir) + "\x00" + filepath.Clean(r.LaunchCWD) + "\x00" + id.String()))
	key := hex.EncodeToString(keyHash[:])
	proofPath := filepath.Join(parent, key+".json")
	if _, err := os.Lstat(proofPath); err == nil {
		return recoverOpenHandsImport(ctx, r, parent, key)
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	paths, err := ConversationFiles(ctx, r.ConfigDir, id.String())
	if err != nil {
		return empty, err
	}
	original, err := os.OpenRoot(filepath.Join(r.ConfigDir, name))
	if err != nil {
		return empty, err
	}
	defer func() { _ = original.Close() }()
	raw, err := readOpenHandsState(ctx, original)
	if err != nil {
		return empty, err
	}
	if err := ValidateImportedState(raw, id.String()); err != nil {
		return empty, err
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(raw, &state); err != nil {
		return empty, err
	}
	// SDK validation materializes the persisted workspace before replacing it
	// with the CLI's current workspace. Persist only the local AO workspace.
	workspace, err := json.Marshal(map[string]string{"kind": "LocalWorkspace", "working_dir": r.LaunchCWD})
	if err != nil {
		return empty, err
	}
	state["workspace"] = workspace
	copyRoot, err := os.MkdirTemp(parent, ".native-"+key+"-")
	if err != nil {
		return empty, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(copyRoot)
		}
	}()
	conversation := filepath.Join(copyRoot, name)
	// The native SDK restores this field from base_state.json. Merely
	// redirecting conversation discovery leaves subsequent writes pointing
	// at the source store, even when tools use the new workspace.
	persistence, err := json.Marshal(conversation)
	if err != nil {
		return empty, err
	}
	state["persistence_dir"] = persistence
	privateState, err := json.Marshal(state)
	if err != nil {
		return empty, err
	}
	if err := os.MkdirAll(filepath.Join(conversation, "events"), 0o700); err != nil {
		return empty, err
	}
	if err := writeOpenHandsFile(ctx, filepath.Join(conversation, "base_state.json"), strings.NewReader(string(privateState)), int64(len(privateState))); err != nil {
		return empty, err
	}
	hashes := map[string]string{"base_state.json": openHandsBytesDigest(raw)}
	remaining := openHandsNativeBytes - int64(len(raw))
	for _, path := range paths[1:] {
		rel := filepath.Join("events", filepath.Base(path))
		info, err := original.Lstat(rel)
		if err != nil || !info.Mode().IsRegular() {
			return empty, errors.New("invalid openhands native event file")
		}
		file, err := original.Open(rel)
		if err != nil {
			return empty, err
		}
		hash := sha256.New()
		count := &openHandsCountingReader{in: io.TeeReader(file, hash)}
		err = writeOpenHandsFile(ctx, filepath.Join(conversation, rel), count, remaining)
		_ = file.Close()
		if err != nil {
			return empty, err
		}
		remaining -= count.n
		hashes[rel] = hex.EncodeToString(hash.Sum(nil))
	}
	sourceDigest := openHandsFilesFingerprint(hashes)
	if r.SourceFingerprint != "" && r.SourceFingerprint != sourceDigest {
		return empty, errors.New("openhands selection changed; rescan")
	}
	fresh, err := ConversationFingerprint(ctx, r.ConfigDir, id.String())
	if err != nil || fresh != sourceDigest {
		return empty, errors.New("openhands source changed during private preparation")
	}
	hashes["base_state.json"] = openHandsBytesDigest(privateState)
	proof := openHandsImportProof{Version: 1, NativeID: id.String(), Source: filepath.Clean(r.ConfigDir), Workspace: filepath.Clean(r.LaunchCWD), Copy: filepath.Base(copyRoot), SourceDigest: sourceDigest, CopyDigest: openHandsFilesFingerprint(hashes)}
	body, err := json.Marshal(proof)
	if err != nil {
		return empty, err
	}
	// Publish the proof exclusively. A complete copy without its checkpoint is
	// recoverable by a new adapter instance; unrelated directories never win.
	proofRoot, err := os.OpenRoot(parent)
	if err != nil {
		return empty, err
	}
	defer func() { _ = proofRoot.Close() }()
	if err := importcopy.WriteOnce(ctx, proofRoot, key+".json", body); err != nil {
		return empty, err
	}
	published = true
	return ports.NativeSessionRef{NativeSessionID: id.String(), ConfigDir: copyRoot, TranscriptPath: filepath.Join(conversation, "base_state.json")}, nil
}

func recoverOpenHandsImport(ctx context.Context, r ports.ImportedSessionRelocation, parent, key string) (ports.NativeSessionRef, error) {
	var empty ports.NativeSessionRef
	root, err := os.OpenRoot(parent)
	if err != nil {
		return empty, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(key + ".json")
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return empty, errors.New("invalid openhands private proof")
	}
	file, err := root.Open(key + ".json")
	if err != nil {
		return empty, err
	}
	var proof openHandsImportProof
	err = json.NewDecoder(io.LimitReader(file, 16<<10)).Decode(&proof)
	_ = file.Close()
	id, _ := uuid.Parse(r.NativeSessionID)
	if err != nil || proof.Version != 1 || proof.NativeID != id.String() || proof.Source != filepath.Clean(r.ConfigDir) || proof.Workspace != filepath.Clean(r.LaunchCWD) || filepath.Base(proof.Copy) != proof.Copy || !strings.HasPrefix(proof.Copy, ".native-"+key+"-") {
		return empty, errors.New("openhands private proof does not match selection")
	}
	if r.SourceFingerprint != "" {
		if r.SourceFingerprint != proof.SourceDigest {
			return empty, errors.New("openhands private proof does not match fingerprint")
		}
	} else {
		fresh, err := ConversationFingerprint(ctx, r.ConfigDir, r.NativeSessionID)
		if err != nil || fresh != proof.SourceDigest {
			return empty, errors.New("unfingerprinted openhands source changed")
		}
	}
	info, err = root.Lstat(proof.Copy)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return empty, errors.New("invalid openhands private copy")
	}
	copyRoot := filepath.Join(parent, proof.Copy)
	digest, err := ConversationFingerprint(ctx, copyRoot, proof.NativeID)
	if err != nil || digest != proof.CopyDigest {
		return empty, errors.New("openhands private copy changed before checkpoint")
	}
	name := strings.ReplaceAll(id.String(), "-", "")
	return ports.NativeSessionRef{NativeSessionID: id.String(), ConfigDir: copyRoot, TranscriptPath: filepath.Join(copyRoot, name, "base_state.json")}, nil
}

func openHandsBytesDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type openHandsCountingReader struct {
	in io.Reader
	n  int64
}

func (r *openHandsCountingReader) Read(p []byte) (int, error) {
	n, err := r.in.Read(p)
	r.n += int64(n)
	return n, err
}

func writeOpenHandsFile(ctx context.Context, path string, in io.Reader, limit int64) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(file, io.LimitReader(openHandsContextReader{ctx: ctx, in: in}, limit+1))
	if err == nil && n > limit {
		err = errors.New("openhands native conversation is too large")
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
