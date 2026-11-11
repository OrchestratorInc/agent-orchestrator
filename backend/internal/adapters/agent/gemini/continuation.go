package gemini

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/importcopy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
	_ ports.AgentImportedSessionRelocator    = (*Plugin)(nil)
)

const maxImportedGeminiBytes = 128 << 20

// NativeSessionConfigDir returns Gemini's state root for the launch environment.
func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	home, ok := env["GEMINI_CLI_HOME"]
	if !ok {
		home = os.Getenv("GEMINI_CLI_HOME")
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".gemini"), nil
}

// ProbeNativeSession checks for readable local history with the exact native id.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	return nativeconfig.ProbeTranscript(ctx, ref, p.LocateTranscript)
}

func geminiSessionHeader(ctx context.Context, root *os.Root, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := root.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	var header struct {
		SessionID string `json:"sessionId"`
		Kind      string `json:"kind"`
	}
	err = json.NewDecoder(io.LimitReader(f, maxImportedGeminiBytes)).Decode(&header)
	if header.Kind == "subagent" {
		return "", errors.New("gemini subagent history cannot be imported")
	}
	return header.SessionID, err
}

// LocateTranscript finds a matching session header within Gemini's state root.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if strings.TrimSpace(ref.ConfigDir) == "" || strings.TrimSpace(ref.NativeSessionID) == "" {
		return "", false, nil
	}
	root, err := os.OpenRoot(ref.ConfigDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { _ = root.Close() }()
	var found string
	err = filepath.WalkDir(filepath.Join(ref.ConfigDir, "tmp"), func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil || !entry.Type().IsRegular() || filepath.Base(filepath.Dir(path)) != "chats" || !strings.HasPrefix(entry.Name(), "session-") {
			return nil //nolint:nilerr // Unreadable provider files are deliberately hidden.
		}
		if !strings.HasSuffix(path, ".json") && !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		rel, err := filepath.Rel(ref.ConfigDir, path)
		if err != nil || !filepath.IsLocal(rel) {
			return nil //nolint:nilerr // Files outside the provider root are ineligible.
		}
		if id, err := geminiSessionHeader(ctx, root, rel); err == nil && id == ref.NativeSessionID {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	return found, found != "", err
}

// RelocateImportedSession copies only the selected session to the project
// directory the CLI will use. Legacy Gemini uses sha256(cwd); current Gemini
// discovers that same directory through .project_root without AO rewriting its
// registry. Existing registered slugs take priority. CLI mode uses getcwd, so
// symlink launch paths are resolved here, unlike an ACP cwd argument.
func (*Plugin) RelocateImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cwd, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(cwd))
	hash := hex.EncodeToString(sum[:])
	root, err := os.OpenRoot(r.ConfigDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	rel, err := filepath.Rel(r.ConfigDir, r.TranscriptPath)
	if err != nil || !filepath.IsLocal(rel) {
		return errors.New("gemini source is outside provider home")
	}
	id, err := geminiSessionHeader(ctx, root, rel)
	if err != nil || id != r.NativeSessionID {
		return errors.New("gemini source no longer holds the imported conversation")
	}
	project := geminiProjectDirectory(root, cwd, hash, runtime.GOOS == "windows")
	dir := filepath.Join("tmp", project)
	if err := root.MkdirAll(filepath.Join(dir, "chats"), 0o700); err != nil {
		return err
	}
	marker := filepath.Join(dir, ".project_root")
	if body, err := root.ReadFile(marker); err == nil {
		if geminiRegistryPath(strings.TrimSpace(string(body)), runtime.GOOS == "windows") != geminiRegistryPath(cwd, runtime.GOOS == "windows") {
			return errors.New("gemini destination belongs to another folder")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := importcopy.WriteOnce(ctx, root, marker, []byte(cwd)); err != nil {
			return err
		}
	} else {
		return err
	}
	destination := filepath.Join(dir, "chats", filepath.Base(r.TranscriptPath))
	if _, err := root.Stat(destination); err == nil {
		if existing, err := geminiSessionHeader(ctx, root, destination); err != nil || existing != r.NativeSessionID {
			return errors.New("gemini destination does not hold the imported conversation")
		}
		return nil // a retry must never overwrite AO's later turns
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := root.Open(rel)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(f, maxImportedGeminiBytes+1))
	_ = f.Close()
	if err != nil {
		return err
	}
	if len(body) > maxImportedGeminiBytes {
		return errors.New("gemini history exceeds copy budget")
	}
	rewrite := func(raw []byte) ([]byte, error) {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		if _, ok := record["projectHash"]; ok {
			record["projectHash"], _ = json.Marshal(hash)
		}
		if set, ok := record["$set"]; ok {
			var updates map[string]json.RawMessage
			if json.Unmarshal(set, &updates) == nil {
				if _, ok := updates["projectHash"]; ok {
					updates["projectHash"], _ = json.Marshal(hash)
					record["$set"], _ = json.Marshal(updates)
				}
			}
		}
		return json.Marshal(record)
	}
	var copied bytes.Buffer
	if strings.HasSuffix(r.TranscriptPath, ".jsonl") {
		reader := bufio.NewReader(bytes.NewReader(body))
		for {
			line, readErr := reader.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				encoded, err := rewrite(line)
				if err != nil {
					if errors.Is(readErr, io.EOF) {
						break // a torn final record is not part of persisted history
					}
					return err
				}
				copied.Write(encoded)
				copied.WriteByte('\n')
			}
			if readErr != nil {
				break
			}
		}
	} else {
		encoded, err := rewrite(body)
		if err != nil {
			return err
		}
		copied.Write(encoded)
	}
	if err := importcopy.WriteOnce(ctx, root, destination, copied.Bytes()); err != nil {
		return fmt.Errorf("write gemini copy: %w", err)
	}
	return nil
}

func geminiRegistryPath(path string, windows bool) string {
	path = filepath.Clean(path)
	if windows {
		return strings.ToLower(path)
	}
	return path
}

func geminiProjectDirectory(root *os.Root, cwd, hash string, windows bool) string {
	if data, err := root.ReadFile("projects.json"); err == nil {
		var registry struct {
			Projects map[string]string `json:"projects"`
		}
		if json.Unmarshal(data, &registry) == nil {
			if slug := registry.Projects[geminiRegistryPath(cwd, windows)]; slug != "" && filepath.Base(slug) == slug && slug != "." && slug != ".." {
				return slug
			}
		}
	}
	// A marker can predate a registry entry (the provider heals these itself).
	if entries, err := fs.ReadDir(root.FS(), "tmp"); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			marker, err := root.ReadFile(filepath.Join("tmp", entry.Name(), ".project_root"))
			if err == nil && geminiRegistryPath(strings.TrimSpace(string(marker)), windows) == geminiRegistryPath(cwd, windows) {
				return entry.Name()
			}
		}
	}
	return hash
}
