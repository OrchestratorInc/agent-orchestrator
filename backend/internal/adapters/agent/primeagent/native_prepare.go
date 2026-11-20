package primeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

type primeImportProof struct {
	Version      int
	SourceID     string
	SourcePath   string
	SourceDigest string
	Workspace    string
	TargetID     string
	TargetPath   string
	TargetDigest string
}

// PrepareImportedSession forks through the provider's taskless RPC startup.
// EOF disposes its engine before AO checkpoints the exact returned native ID.
// Native output and the retry proof stay in AO's managed Prime session root.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	var empty ports.NativeSessionRef
	source, found, err := p.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: r.ConfigDir})
	if err != nil {
		return empty, err
	}
	if !found || filepath.Clean(source) != filepath.Clean(r.TranscriptPath) {
		return empty, errors.New("exact prime source history is unavailable")
	}
	expected, err := readPrimeNativeHistory(ctx, r.ConfigDir, source, 0)
	if err != nil {
		return empty, err
	}
	if !primeSamePath(expected.cwd, r.SourceCWD) {
		return empty, errors.New("prime native source workspace changed")
	}
	workspace, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return empty, err
	}
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		return empty, errors.New("prime native AO workspace is unavailable")
	}
	root, err := sessionDir(r.DataDir)
	if err != nil {
		return empty, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return empty, err
	}
	key := sha256.Sum256([]byte(source + "\x00" + r.NativeSessionID + "\x00" + workspace))
	marker := ".ao-import-" + hex.EncodeToString(key[:]) + ".json"
	proof := primeImportProof{Version: 1, SourceID: r.NativeSessionID, SourcePath: source, SourceDigest: hex.EncodeToString(expected.digest[:]), Workspace: workspace}
	if ref, found, err := readPrimeImportProof(ctx, root, marker, proof, expected); err != nil || found {
		return ref, err
	}
	before, err := primeSessionFiles(ctx, root)
	if err != nil {
		return empty, err
	}
	binary, err := p.primeAgentBinary(ctx)
	if err != nil {
		return empty, err
	}
	env := maps.Clone(r.Env)
	if env == nil {
		env = map[string]string{}
	}
	env["PRIME_AGENT_SESSION_DIR"] = root
	profile, err := primeDataDir(r.DataDir)
	if err != nil {
		return empty, err
	}
	env[primeAgentCodingAgentDirEnv] = profile
	env["PRIME_AGENT_DAEMON_SOCKET"] = filepath.Join(profile, "daemon.sock")
	runner := p.runCommand
	if runner == nil {
		runner = runPrimeNativeFork
	}
	forkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err = runner(forkCtx, binary, workspace, env, "--mode", "rpc", "--offline", "--daemon-socket", env["PRIME_AGENT_DAEMON_SOCKET"], "--session-dir", root, "--cwd", workspace, "--fork", source)
	if err != nil {
		if ctxErr := forkCtx.Err(); ctxErr != nil {
			return empty, ctxErr
		}
		return empty, fmt.Errorf("prime native fork preparation failed: %w", err)
	}
	after, err := readPrimeNativeHistory(ctx, r.ConfigDir, source, 0)
	if err != nil || after.digest != expected.digest || after.count != expected.count {
		return empty, errors.New("prime native source changed during preparation")
	}
	files, err := primeSessionFiles(ctx, root)
	if err != nil {
		return empty, err
	}
	for path := range files {
		if before[path] {
			continue
		}
		history, err := readPrimeNativeHistory(ctx, root, path, expected.count)
		if err != nil || !primeForkMatches(history, proof, expected) {
			continue
		}
		if proof.TargetID != "" {
			return empty, errors.New("prime native fork identity is ambiguous")
		}
		complete, err := readPrimeNativeHistory(ctx, root, path, 0)
		if err != nil {
			return empty, err
		}
		proof.TargetID = history.id
		proof.TargetPath, err = filepath.Rel(root, path)
		if err != nil {
			return empty, err
		}
		proof.TargetDigest = hex.EncodeToString(complete.digest[:])
	}
	if proof.TargetID == "" {
		return empty, errors.New("prime native preparation did not create the requested fork")
	}
	data, err := json.Marshal(proof)
	if err != nil {
		return empty, err
	}
	if err := publishPrimeImportProof(root, marker, data); err != nil {
		return empty, err
	}
	return ports.NativeSessionRef{NativeSessionID: proof.TargetID, ConfigDir: root, TranscriptPath: filepath.Join(root, proof.TargetPath)}, nil
}

func publishPrimeImportProof(root, marker string, data []byte) error {
	file, err := os.CreateTemp(root, ".ao-import-staging-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	// Linking a complete, synced file publishes atomically without replacing a
	// concurrent proof. The staging name is never considered an import proof.
	return os.Link(file.Name(), filepath.Join(root, marker))
}

func primeForkMatches(history primeNativeHistory, proof primeImportProof, expected primeNativeHistory) bool {
	id, err := uuid.Parse(history.id)
	// Current Prime creates UUIDv7 identities; older native histories used v4.
	return err == nil && len(history.id) == 36 && (id.Version() == 4 || id.Version() == 7) && history.id != proof.SourceID &&
		primeSamePath(history.parent, proof.SourcePath) && primeSamePath(history.cwd, proof.Workspace) &&
		history.count == expected.count && history.digest == expected.digest
}

func readPrimeImportProof(ctx context.Context, root, marker string, expectedProof primeImportProof, expected primeNativeHistory) (ports.NativeSessionRef, bool, error) {
	var empty ports.NativeSessionRef
	provider, err := os.OpenRoot(root)
	if err != nil {
		return empty, false, err
	}
	defer func() { _ = provider.Close() }()
	info, err := provider.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return empty, false, nil
	}
	if err != nil {
		return empty, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<10 || info.Mode().Perm()&0o077 != 0 {
		return empty, false, errors.New("prime native preparation proof is not private regular data")
	}
	file, err := provider.Open(marker)
	if err != nil {
		return empty, false, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil {
		return empty, false, err
	}
	var proof primeImportProof
	if len(data) > 16<<10 || json.Unmarshal(data, &proof) != nil || proof.Version != expectedProof.Version || proof.SourceID != expectedProof.SourceID || proof.SourcePath != expectedProof.SourcePath || proof.SourceDigest != expectedProof.SourceDigest || proof.Workspace != expectedProof.Workspace || !filepath.IsLocal(proof.TargetPath) {
		return empty, false, errors.New("prime native preparation proof does not match this import")
	}
	path := filepath.Join(root, proof.TargetPath)
	history, err := readPrimeNativeHistory(ctx, root, path, expected.count)
	if err != nil || history.id != proof.TargetID || !primeForkMatches(history, proof, expected) {
		return empty, false, errors.New("prime prepared fork no longer matches its source and workspace")
	}
	complete, err := readPrimeNativeHistory(ctx, root, path, 0)
	if err != nil || hex.EncodeToString(complete.digest[:]) != proof.TargetDigest {
		return empty, false, errors.New("prime prepared fork changed before its identity was checkpointed")
	}
	return ports.NativeSessionRef{NativeSessionID: proof.TargetID, ConfigDir: root, TranscriptPath: path}, true, nil
}

func runPrimeNativeFork(ctx context.Context, binary, workingDir string, env map[string]string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, binary, args...) //nolint:gosec // Adapter-resolved binary; all arguments are adapter-owned.
	cmd.Dir = workingDir
	// Direct RPC startup has no login shell. Apply every explicit override,
	// including clears, with the same semantics for every provider.
	cmd.Env = slices.DeleteFunc(processenv.Merge(env), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		value, explicit := env[key]
		return explicit && value == ""
	})
	// A nil stdin supplies EOF, not a prompt or an RPC command. Prime's RPC
	// shutdown disposes the engine/kernel and exits after the native fork.
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return nil, cmd.Run()
}
