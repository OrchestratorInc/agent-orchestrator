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

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/importcopy"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)

type kimiImportProof struct {
	Source    string `json:"source"`
	ID        string `json:"id"`
	CWD       string `json:"cwd"`
	Selection string `json:"selection"`
	Home      string `json:"home"`
	Copied    string `json:"copied"`
	Model     string `json:"model"`
}

// CheckImportVersion refuses unverified native contracts before any import launch.
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
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	if err := p.CheckImportVersion(ctx); err != nil {
		return ports.NativeSessionRef{}, err
	}
	return prepareKimiImport(ctx, r, "2.4.1")
}

func prepareKimiImport(ctx context.Context, r ports.ImportedSessionRelocation, version string) (ports.NativeSessionRef, error) {
	var zero ports.NativeSessionRef
	if version != "2.4.1" {
		return zero, errors.New("kimi: native import requires Kimi Code 2.4.1")
	}
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
	key := fmt.Sprintf("%x.json", sha256.Sum256([]byte(source+"\x00"+r.NativeSessionID+"\x00"+cwd)))
	reuse := func() (ports.NativeSessionRef, error) {
		b, err := readKimiFile(ctx, root, key, 1<<20)
		if err != nil {
			return zero, err
		}
		var proof kimiImportProof
		if json.Unmarshal(b, &proof) != nil || proof.Source != source || proof.ID != r.NativeSessionID || proof.CWD != cwd || proof.Home == "" || filepath.Base(proof.Home) != proof.Home || !strings.HasPrefix(proof.Home, ".native-") || proof.Selection == "" || (r.SourceFingerprint != "" && proof.Selection != r.SourceFingerprint) {
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
		return reuse()
	} else if !errors.Is(err, os.ErrNotExist) {
		return zero, err
	}
	original, err := ReadImportedSession(ctx, source, r.NativeSessionID)
	if err != nil {
		return zero, err
	}
	if r.SourceFingerprint != "" && r.SourceFingerprint != original.Fingerprint {
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
	var state kimiImportState
	if err := json.Unmarshal(original.State, &state); err != nil {
		return zero, err
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
	b, err := json.Marshal(state)
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
	proofBytes, _ := json.Marshal(proof)
	if err := os.WriteFile(filepath.Join(home, ".ao-import.json"), proofBytes, 0o600); err != nil {
		return zero, err
	}
	if err := importcopy.WriteOnce(ctx, root, key, proofBytes); err != nil {
		if errors.Is(err, os.ErrExist) {
			return reuse()
		}
		return zero, err
	}
	published = true
	return ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: home, TranscriptPath: copied.Path}, nil
}
