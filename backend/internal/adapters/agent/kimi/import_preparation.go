package kimi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/importcopy"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/OrchestratorInc/agent-orchestrator/backend/internal/process"
)

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)
var _ ports.AgentImportVersionChecker = (*Plugin)(nil)

type kimiImportProof struct {
	Source    string `json:"source"`
	ID        string `json:"id"`
	CWD       string `json:"cwd"`
	Selection string `json:"selection"`
	Home      string `json:"home"`
	Copied    string `json:"copied"`
	Model     string `json:"model"`
	// The imported journal is append-only; mutable state.json is validated
	// separately. Later native turns and blobs do not share snapshot budgets.
	WireBytes int64            `json:"wire_bytes"`
	WireHash  string           `json:"wire_hash"`
	Blobs     map[string]int64 `json:"blobs,omitempty"`
}

// CheckImportVersion refuses unverified native contracts for new preparations.
// Existing private snapshots remain resumable after the native binary updates.
func (p *Plugin) CheckImportVersion(ctx context.Context) error {
	binary, err := p.kimiBinary(ctx)
	if err != nil {
		return err
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := aoprocess.CommandContext(probe, binary, "--version")
	command.WaitDelay = 100 * time.Millisecond
	out, err := command.Output()
	if err != nil || strings.TrimSpace(string(out)) != "2.4.1" {
		return errors.New("kimi: native import requires Kimi Code 2.4.1")
	}
	return nil
}

// PrepareImportedSession proves and stages the exact selected native history.
// The installed version gates only a NEW copy: an already-published private
// home is recovered from its proof even after a later Kimi update.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	return prepareKimiImportChecked(ctx, r, p.CheckImportVersion)
}

func prepareKimiImport(ctx context.Context, r ports.ImportedSessionRelocation, version string) (ports.NativeSessionRef, error) {
	return prepareKimiImportChecked(ctx, r, func(context.Context) error {
		if version != "2.4.1" {
			return errors.New("kimi: native import requires Kimi Code 2.4.1")
		}
		return nil
	})
}

func prepareKimiImportChecked(ctx context.Context, r ports.ImportedSessionRelocation, checkVersion func(context.Context) error) (ports.NativeSessionRef, error) {
	var zero ports.NativeSessionRef
	if !r.Chat || !kimiNativeSessionID.MatchString(r.NativeSessionID) || !filepath.IsAbs(r.ConfigDir) || !filepath.IsAbs(r.DataDir) || !filepath.IsAbs(r.LaunchCWD) {
		return zero, errors.New("kimi: imported conversations require Chat and absolute native/workspace/data paths")
	}
	cwd, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return zero, err
	}
	source, err := filepath.Abs(r.ConfigDir)
	if err != nil {
		return zero, err
	}
	staging := filepath.Join(r.DataDir, "native-imports", "kimi")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return zero, err
	}
	root, err := os.OpenRoot(staging)
	if err != nil {
		return zero, err
	}
	defer func() { _ = root.Close() }()
	selection := r.SourceFingerprint
	if selection == "" {
		original, err := ReadImportedSession(ctx, source, r.NativeSessionID)
		if err != nil {
			return zero, err
		}
		selection = original.Fingerprint
	}
	// Each selected snapshot owns its own reservation and private home. Never
	// retire a previous proof: a prepared or active session may still use it.
	binding := source + "\x00" + r.NativeSessionID + "\x00" + cwd
	key := fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding+"\x00"+selection)))
	reuse := func(key string) (ports.NativeSessionRef, error) {
		b, err := readKimiFile(ctx, root, key, 1<<20)
		if err != nil {
			return zero, err
		}
		var proof kimiImportProof
		if json.Unmarshal(b, &proof) != nil || proof.Source != source || proof.ID != r.NativeSessionID || proof.CWD != cwd || proof.Home == "" || filepath.Base(proof.Home) != proof.Home || !strings.HasPrefix(proof.Home, ".native-") || proof.Selection == "" || proof.Selection != selection {
			return zero, errors.New("kimi: private import proof does not match selection")
		}
		status, err := root.Lstat(proof.Home)
		if err != nil || !status.IsDir() || status.Mode()&os.ModeSymlink != 0 {
			return zero, errors.New("kimi: invalid private native home")
		}
		home := filepath.Join(staging, proof.Home)
		copied, err := ReadImportedSession(ctx, home, proof.ID)
		if err != nil {
			return zero, err
		}
		if copied.Fingerprint != proof.Copied || copied.CWD != cwd {
			return zero, errors.New("kimi: prepared native history changed before checkpoint")
		}
		return ports.NativeSessionRef{NativeSessionID: proof.ID, ConfigDir: home, TranscriptPath: copied.Path}, nil
	}
	if _, err := root.Lstat(key); err == nil {
		return reuse(key)
	} else if !errors.Is(err, os.ErrNotExist) {
		return zero, err
	}
	// Recover a pre-fingerprint reservation only for its exact selection. A
	// different selection must not be blocked by that earlier home or history.
	legacyKey := fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding)))
	if b, err := readKimiFile(ctx, root, legacyKey, 1<<20); err == nil {
		var proof kimiImportProof
		if json.Unmarshal(b, &proof) != nil {
			return zero, errors.New("kimi: invalid private import proof")
		}
		if proof.Selection == selection {
			return reuse(legacyKey)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return zero, err
	}
	if err := checkVersion(ctx); err != nil {
		return zero, err
	}
	original, err := ReadImportedSession(ctx, source, r.NativeSessionID)
	if err != nil {
		return zero, err
	}
	if err := CheckImportedOAuth(ctx, source, original.Model); err != nil {
		return zero, err
	}
	if selection != original.Fingerprint {
		return zero, errors.New("kimi: source changed since selection; scan again")
	}
	home, err := os.MkdirTemp(staging, ".native-")
	if err != nil {
		return zero, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(home)
		}
	}()
	sessionDir := filepath.Join(home, "sessions", kimiWorkdirKey(cwd), r.NativeSessionID)
	mainDir := filepath.Join(sessionDir, "agents", "main")
	if err := os.MkdirAll(mainDir, 0o700); err != nil {
		return zero, err
	}
	b, err := remapKimiState(original.State, cwd, mainDir)
	if err != nil {
		return zero, err
	}
	wire, err := remapKimiWire(original.Wire, cwd)
	if err != nil {
		return zero, err
	}
	for name, data := range map[string][]byte{filepath.Join(sessionDir, "state.json"): b, filepath.Join(mainDir, "wire.jsonl"): wire} {
		if err := os.WriteFile(name, data, 0o600); err != nil {
			return zero, err
		}
	}
	if len(original.Blobs) != 0 {
		blobDir := filepath.Join(mainDir, "blobs")
		if err := os.MkdirAll(blobDir, 0o700); err != nil {
			return zero, err
		}
		for key, data := range original.Blobs {
			if err := os.WriteFile(filepath.Join(blobDir, key), data, 0o600); err != nil {
				return zero, err
			}
		}
	}
	entry, _ := json.Marshal(map[string]string{"sessionId": r.NativeSessionID, "sessionDir": sessionDir, "workDir": cwd})
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), append(entry, '\n'), 0o600); err != nil {
		return zero, err
	}
	copied, err := ReadImportedSession(ctx, home, r.NativeSessionID)
	if err != nil {
		return zero, err
	}
	latest, err := ReadImportedSession(ctx, source, r.NativeSessionID)
	if err != nil {
		return zero, err
	}
	if latest.Fingerprint != original.Fingerprint {
		return zero, errors.New("kimi: source changed during native preparation")
	}
	proof := kimiImportProof{Source: source, ID: r.NativeSessionID, CWD: cwd, Selection: original.Fingerprint, Home: filepath.Base(home), Copied: copied.Fingerprint, Model: original.Model}
	proof.WireBytes = int64(len(copied.Wire))
	proof.WireHash = fmt.Sprintf("%x", sha256.Sum256(copied.Wire))
	proof.Blobs = make(map[string]int64, len(copied.Blobs))
	for key, data := range copied.Blobs {
		proof.Blobs[key] = int64(len(data))
	}
	proofBytes, _ := json.Marshal(proof)
	if err := os.WriteFile(filepath.Join(home, ".ao-import.json"), proofBytes, 0o600); err != nil {
		return zero, err
	}
	if err := importcopy.WriteOnce(ctx, root, key, proofBytes); err != nil {
		if errors.Is(err, os.ErrExist) {
			return reuse(key)
		}
		return zero, err
	}
	published = true
	return ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: home, TranscriptPath: copied.Path}, nil
}

func remapKimiState(data []byte, cwd, mainDir string) ([]byte, error) {
	var state kimiImportState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	// Construct only the documented session state. No config, tokens, provider
	// headers, locks, caches, hooks, credentials or arbitrary sidecars are copied.
	state.CWD = cwd
	state.Agents = map[string]struct {
		Home string `json:"homedir"`
		Type string `json:"type"`
	}{"main": {Home: mainDir, Type: "main"}}
	custom := map[string]json.RawMessage{}
	if title, ok := state.Custom["title"]; ok {
		var value string
		if json.Unmarshal(title, &value) == nil {
			custom["title"] = title
		}
	}
	state.Custom = custom
	return json.Marshal(state)
}
