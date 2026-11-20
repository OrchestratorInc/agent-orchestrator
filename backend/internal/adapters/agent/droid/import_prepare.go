package droid

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/importcopy"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)

type droidImportReceipt struct {
	SourceID    string `json:"sourceId"`
	Fingerprint string `json:"fingerprint"`
	TargetID    string `json:"targetId"`
	ConfigDir   string `json:"configDir"`
	CWD         string `json:"cwd"`
	Path        string `json:"path"`
	// Assumes Droid appends history without rewriting earlier JSONL lines;
	// the mutable session header is excluded from this hash.
	PrefixHash  string `json:"prefixHash"`
	PrefixBytes int    `json:"prefixBytes"`
}

// PrepareImportedSession publishes only a conversation JSONL under a new UUID
// in the existing Factory profile. It never opens/copies settings or auth. The
// receipt lets S2 recover publication after a lost identity checkpoint without
// replacing later turns or consulting the original source again.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	id, err := canonicalDroidNativeSessionID(r.NativeSessionID)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if !filepath.IsAbs(r.ConfigDir) || !filepath.IsAbs(r.LaunchCWD) || !filepath.IsAbs(r.DataDir) || r.SourceFingerprint == "" {
		return ports.NativeSessionRef{}, errors.New("droid: import requires absolute roots and selected-history fingerprint")
	}
	if err := p.verifyImportVersion(ctx, r.Env); err != nil {
		return ports.NativeSessionRef{}, err
	}
	if err := p.verifyImportProfile(ctx, r.ConfigDir, r.Env); err != nil {
		return ports.NativeSessionRef{}, err
	}
	launch, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	config, err := filepath.EvalSymlinks(r.ConfigDir)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	target := uuid.NewSHA1(uuid.NameSpaceOID, []byte(config+"\x00"+id+"\x00"+launch+"\x00"+r.SourceFingerprint)).String()
	receiptPath := droidReceiptPath(r.DataDir, target)
	var planned *droidImportReceipt
	var published *droidImportReceipt
	if _, err := os.Lstat(receiptPath); err == nil {
		receipt, err := readDroidReceipt(ctx, r.DataDir, config, target, launch)
		if err != nil && (!errors.Is(err, os.ErrNotExist) || receipt.TargetID != target) {
			return ports.NativeSessionRef{}, err
		}
		if receipt.SourceID != id || receipt.Fingerprint != r.SourceFingerprint {
			return ports.NativeSessionRef{}, errors.New("droid: prepared copy does not match selected source")
		}
		completed, completionErr := droidImportCompleted(r.DataDir, target, r.SourceFingerprint)
		if completionErr != nil {
			return ports.NativeSessionRef{}, completionErr
		}
		if err == nil && completed {
			return ports.NativeSessionRef{NativeSessionID: target, ConfigDir: config, TranscriptPath: receipt.Path}, nil
		}
		if err != nil && completed {
			return ports.NativeSessionRef{}, err
		}
		// A published copy is not verified until the post-publication source
		// check succeeds. A prior rejection must never become a fast-path success.
		if err == nil {
			published = &receipt
		} else {
			planned = &receipt
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ports.NativeSessionRef{}, err
	} else if completed, err := droidImportCompleted(r.DataDir, target, r.SourceFingerprint); err != nil {
		return ports.NativeSessionRef{}, err
	} else if completed {
		return ports.NativeSessionRef{}, errors.New("droid: verified import has lost its ownership receipt")
	}
	src := r.TranscriptPath
	if src == "" {
		var found bool
		src, found, err = p.LocateTranscript(ctx, ports.NativeSessionRef{ConfigDir: config, NativeSessionID: id})
		if err != nil {
			return ports.NativeSessionRef{}, err
		}
		if !found {
			return ports.NativeSessionRef{}, errors.New("droid: original conversation missing")
		}
	}
	if filepath.Base(src) != id+".jsonl" {
		return ports.NativeSessionRef{}, errors.New("droid: selected filename does not match source identity")
	}
	body, err := ReadNativeHistory(ctx, config, src)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if NativeHistoryFingerprint(body) != r.SourceFingerprint {
		return ports.NativeSessionRef{}, errors.New("droid: source changed since selection; scan again")
	}
	if err := validateDroidHistory(body, id, r.SourceCWD); err != nil {
		return ports.NativeSessionRef{}, err
	}
	newline := bytes.IndexByte(body, '\n')
	if newline < 0 {
		return ports.NativeSessionRef{}, errors.New("droid: incomplete native history")
	}
	header, err := importcopy.SetStringField(body[:newline], "id", target)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	header, err = importcopy.SetStringField(header, "cwd", launch)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	copyBody := append(append(header, '\n'), body[newline+1:]...)
	if err := validateDroidHistory(copyBody, target, launch); err != nil {
		return ports.NativeSessionRef{}, err
	}
	// Use the same cwd bucket encoding as the native CLI. A new UUID avoids
	// Droid's cross-bucket resolver ever choosing the source conversation.
	bucket := strings.NewReplacer("\\", "-", "/", "-", ":", "-").Replace(launch)
	if !strings.HasPrefix(bucket, "-") {
		bucket = "-" + bucket
	}
	rel := filepath.Join("sessions", bucket, target+".jsonl")
	root, err := os.OpenRoot(config)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(filepath.Dir(rel), 0o700); err != nil {
		return ports.NativeSessionRef{}, err
	}
	// Droid can update the header title on a later turn. Bind its identity and
	// cwd structurally; pin the copied conversation bytes after that header.
	payload := copyBody[bytes.IndexByte(copyBody, '\n')+1:]
	receipt := droidImportReceipt{SourceID: id, Fingerprint: r.SourceFingerprint, TargetID: target, ConfigDir: config, CWD: launch, Path: filepath.Join(config, rel), PrefixHash: NativeHistoryFingerprint(payload), PrefixBytes: len(payload)}
	if planned != nil && *planned != receipt {
		return ports.NativeSessionRef{}, errors.New("droid: publication reservation does not match selected history")
	}
	if published != nil && *published != receipt {
		return ports.NativeSessionRef{}, errors.New("droid: incomplete publication does not match selected history")
	}
	if _, found, err := p.LocateTranscript(ctx, ports.NativeSessionRef{ConfigDir: config, NativeSessionID: target}); err != nil {
		return ports.NativeSessionRef{}, err
	} else if found && published == nil {
		return ports.NativeSessionRef{}, errors.New("droid: unproven native target already exists")
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o700); err != nil {
		return ports.NativeSessionRef{}, err
	}
	receipts, err := os.OpenRoot(filepath.Dir(receiptPath))
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	defer func() { _ = receipts.Close() }()
	// Reserve ownership BEFORE publishing any native transcript. Cancellation
	// or a receipt-write failure can no longer orphan an unprovable native copy.
	if planned == nil && published == nil {
		if err := importcopy.WriteOnce(ctx, receipts, filepath.Base(receiptPath), encoded); err != nil {
			return ports.NativeSessionRef{}, err
		}
	}
	if published == nil {
		if err := importcopy.WriteOnce(ctx, root, rel, copyBody); err != nil {
			return ports.NativeSessionRef{}, fmt.Errorf("droid: publish native copy: %w", err)
		}
	}
	// Refuse concurrent source changes before permitting any native launch.
	after, err := ReadNativeHistory(ctx, config, src)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if !bytes.Equal(after, body) {
		return ports.NativeSessionRef{}, errors.New("droid: source changed during preparation")
	}
	if err := importcopy.WriteOnce(ctx, receipts, filepath.Base(receiptPath)+".verified", []byte(r.SourceFingerprint)); err != nil && !errors.Is(err, os.ErrExist) {
		return ports.NativeSessionRef{}, err
	}
	if err := verifyDroidReceipt(ctx, r.DataDir, config, target, launch); err != nil {
		return ports.NativeSessionRef{}, err
	}
	return ports.NativeSessionRef{NativeSessionID: target, ConfigDir: config, TranscriptPath: receipt.Path}, nil
}

func droidReceiptPath(dataDir, id string) string {
	return filepath.Join(dataDir, "native-imports", "droid", id+".json")
}

func verifyDroidReceipt(ctx context.Context, dataDir, config, id, cwd string) error {
	r, err := readDroidReceipt(ctx, dataDir, config, id, cwd)
	if err != nil {
		return err
	}
	completed, err := droidImportCompleted(dataDir, id, r.Fingerprint)
	if err != nil {
		return err
	}
	if !completed {
		return errors.New("droid: native preparation has not completed source verification")
	}
	return nil
}

func droidImportCompleted(dataDir, id, fingerprint string) (bool, error) {
	path := droidReceiptPath(dataDir, id) + ".verified"
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != int64(len(fingerprint)) {
		return false, errors.New("droid: invalid import completion record")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if string(body) != fingerprint {
		return false, errors.New("droid: mismatched import completion record")
	}
	return true, nil
}

func readDroidReceipt(ctx context.Context, dataDir, config, id, cwd string) (droidImportReceipt, error) {
	var r droidImportReceipt
	if !filepath.IsAbs(dataDir) {
		return r, errors.New("droid: missing prepared import state")
	}
	if _, err := canonicalDroidNativeSessionID(id); err != nil {
		return r, err
	}
	path := droidReceiptPath(dataDir, id)
	info, err := os.Lstat(path)
	if err != nil {
		return r, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return r, errors.New("droid: invalid import receipt")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if json.Unmarshal(body, &r) != nil || r.TargetID != id || r.ConfigDir != config || r.CWD != cwd || r.PrefixBytes <= 0 || r.PrefixBytes > maxDroidHistoryBytes {
		return r, errors.New("droid: prepared import binding mismatch")
	}
	f, err := openDroidHistory(ctx, config, r.Path)
	if err != nil {
		return r, err
	}
	defer func() { _ = f.Close() }()
	resolved, found, err := New().LocateTranscript(ctx, ports.NativeSessionRef{ConfigDir: config, NativeSessionID: id})
	if err != nil {
		return r, err
	}
	if !found || resolved != r.Path {
		return r, errors.New("droid: native resolver differs from prepared conversation")
	}
	header := droidHistoryScanner(f)
	if !header.Scan() {
		if err := header.Err(); err != nil {
			return r, err
		}
		return r, errors.New("droid: incomplete native history")
	}
	line := header.Bytes()
	if len(line) == 0 || line[len(line)-1] != '\n' {
		return r, errors.New("droid: incomplete native history")
	}
	// Only the imported payload is pinned. Hash it in bounded chunks, then
	// validate every record, including later turns, without a total-size cap.
	hash := sha256.New()
	if _, err := io.CopyN(hash, io.NewSectionReader(f, int64(len(line)), int64(r.PrefixBytes)), int64(r.PrefixBytes)); err != nil {
		return r, fmt.Errorf("droid: incomplete prepared native history: %w", err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != r.PrefixHash {
		return r, errors.New("droid: prepared native history changed or is incomplete")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return r, err
	}
	if err := validateDroidHistoryReader(ctx, f, id, cwd); err != nil {
		return r, err
	}
	return r, nil
}

func (p *Plugin) verifyImportProfile(ctx context.Context, config string, env map[string]string) error {
	current, err := p.NativeSessionConfigDir(ctx, env)
	if err != nil {
		return err
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return err
	}
	recorded, err := filepath.EvalSymlinks(config)
	if err != nil {
		return err
	}
	if current != recorded {
		return errors.New("droid: launch profile differs from imported Factory profile")
	}
	return nil
}

func (p *Plugin) verifyImportVersion(ctx context.Context, env map[string]string) error {
	binary, err := p.droidBinary(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, binary, "--version")
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out := &droidVersionOutput{}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return errors.New("droid: could not verify installed version")
	}
	if strings.TrimSpace(out.String()) != "0.251.0" {
		return errors.New("droid: session import supports only verified version 0.251.0")
	}
	return nil
}

type droidVersionOutput struct{ bytes.Buffer }

func (b *droidVersionOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1024 {
		return 0, errors.New("droid: version output too large")
	}
	return b.Buffer.Write(p)
}

func (p *Plugin) validateImportedRuntime(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if cfg.ImportConfigDir == "" {
		return nil
	}
	if err := p.verifyImportProfile(ctx, cfg.ImportConfigDir, cfg.Env); err != nil {
		return err
	}
	config, err := filepath.EvalSymlinks(cfg.ImportConfigDir)
	if err != nil {
		return err
	}
	cwd, err := filepath.EvalSymlinks(cfg.WorkspacePath)
	if err != nil {
		return err
	}
	return verifyDroidReceipt(ctx, cfg.DataDir, config, cfg.NativeSessionID, cwd)
}

// PrepareRuntimeLaunch guards imported first-open and every later reopen before
// the native process can fall back to an empty conversation.
func (p *Plugin) PrepareRuntimeLaunch(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return p.validateImportedRuntime(ctx, cfg)
}

// NativeConversationExists applies the same binding to terminal preflight.
func (p *Plugin) NativeConversationExists(ctx context.Context, ref ports.SessionRef, id string, env map[string]string) (bool, error) {
	config := ref.Metadata[ports.MetadataKeyImportConfigDir]
	if config == "" {
		return true, nil
	}
	err := p.validateImportedRuntime(ctx, ports.WorkspaceHookConfig{DataDir: ref.DataDir, Env: env, WorkspacePath: ref.WorkspacePath, NativeSessionID: id, ImportConfigDir: config})
	return err == nil, err
}
