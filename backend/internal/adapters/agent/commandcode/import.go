package commandcode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var commandNativeID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// NativeSessionConfigDir resolves the profile containing the native catalog.
func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if home := env["HOME"]; home != "" {
		return filepath.Join(home, ".commandcode"), nil
	}
	if home := env["USERPROFILE"]; home != "" {
		return filepath.Join(home, ".commandcode"), nil
	}
	return realCommandCodeDir(), nil
}

// LocateTranscript finds a readable v3 conversation in the native catalog.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if !commandNativeID.MatchString(ref.NativeSessionID) {
		return "", false, nil
	}
	root, err := os.OpenRoot(ref.ConfigDir)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = root.Close() }()
	projects, err := root.OpenRoot("projects")
	if err != nil {
		return "", false, err
	}
	defer func() { _ = projects.Close() }()
	entries, err := os.ReadDir(filepath.Join(ref.ConfigDir, "projects"))
	if err != nil {
		return "", false, err
	}
	var found string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		rel := filepath.Join(entry.Name(), ref.NativeSessionID+".jsonl")
		if err := commandReadable(ctx, projects, rel, ref.NativeSessionID); err != nil {
			continue
		}
		found = filepath.Join(ref.ConfigDir, "projects", rel)
		break
	}
	return found, found != "", nil
}

// ProbeNativeSession checks the native catalog without starting Command Code.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	_, ok, err := p.LocateTranscript(ctx, ref)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !ok) {
		return ports.NativeSessionAvailabilityUnavailable, nil
	}
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	return ports.NativeSessionAvailabilityAvailable, nil
}

// Command Code 1.88.2 uses @sindresorhus/slugify 2.x for project buckets.
// Match its four decamelizing passes for this exact ASCII subset. Refuse other
// paths rather than guess the dependency's Unicode transliteration table.
func commandProjectBucket(cwd string) (string, error) {
	if !filepath.IsAbs(cwd) || strings.ContainsAny(cwd, "\r\n") {
		return "", errors.New("command-code workspace must be absolute")
	}
	for _, ch := range cwd {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune(" /\\._-:", ch) {
			continue
		}
		return "", errors.New("command-code workspace needs unsupported slug transliteration")
	}
	for _, pattern := range []string{`([A-Z]{2,})([0-9]+)`, `([a-z0-9]+)([A-Z]{2,})`, `([a-z0-9])([A-Z])`, `([A-Z]+)([A-Z][a-rt-z0-9]+)`} {
		cwd = regexp.MustCompile(pattern).ReplaceAllString(cwd, "$1 $2")
	}
	slug := regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(cwd), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "root"
	}
	if len(slug) > 255 {
		return "", errors.New("command-code workspace bucket exceeds filesystem limit")
	}
	return slug, nil
}

func commandNativeProjectBucket(cwd string) (string, error) {
	// The native catalog uses Node's process.cwd(), which is the physical
	// working directory rather than an alias supplied to the runtime launcher.
	physicalCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	return commandProjectBucket(physicalCWD)
}

// RelocateImportedSession copies only this native conversation and its sidecars
// into the launch project's bucket. Native tools use the launch cwd; the saved
// header's cwd is historical metadata, not an execution override.
func (*Plugin) RelocateImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !commandNativeID.MatchString(r.NativeSessionID) {
		return errors.New("invalid command-code native session id")
	}
	bucket, err := commandNativeProjectBucket(r.LaunchCWD)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(r.ConfigDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	rel, err := filepath.Rel(r.ConfigDir, r.TranscriptPath)
	if err != nil || !filepath.IsLocal(rel) || filepath.Base(rel) != r.NativeSessionID+".jsonl" {
		return errors.New("command-code transcript is outside its profile")
	}
	// Reuse a validated prepared target before reading the original source.
	// New copies still pass native validation in staging before publication.
	return copyCommandBundle(ctx, root, filepath.Dir(rel), r.ConfigDir, bucket, r.NativeSessionID)
}

// The shared workspace context preserves import provenance after adoption, even
// if private files are removed. Only first open copies the native bundle; later
// launches must find AO's existing turns and may not reseed from an old source.
func prepareImportedCommandWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if cfg.ImportConfigDir != "" {
		if !commandNativeID.MatchString(cfg.NativeSessionID) {
			return errors.New("invalid command-code imported identity")
		}
		switch cfg.RestoreMode {
		case ports.WorkspaceRestoreImported:
			home, err := PrepareSessionHome(cfg.DataDir, domain.SessionID(cfg.SessionID), cfg.SystemPrompt)
			if err != nil {
				return err
			}
			if home == "" {
				return errors.New("command-code imported profile is unavailable")
			}
			bucket, err := commandNativeProjectBucket(cfg.WorkspacePath)
			if err != nil {
				return err
			}
			source, err := os.OpenRoot(cfg.ImportConfigDir)
			if err != nil {
				return err
			}
			err = copyCommandBundle(ctx, source, filepath.Join("projects", bucket), filepath.Join(home, ".commandcode"), bucket, cfg.NativeSessionID)
			_ = source.Close()
			if err != nil {
				return err
			}
		case ports.WorkspaceRestoreNative:
			// Preserve the managed copy, including all later AO turns.
		default:
			return errors.New("command-code import requires native restore context")
		}
		if err := verifyImportedCommandResume(ctx, cfg.DataDir, domain.SessionID(cfg.SessionID), cfg.WorkspacePath, cfg.NativeSessionID); err != nil {
			return err
		}
	}
	return commandCodeHooks.Install(ctx, cfg.WorkspacePath)
}

func verifyImportedCommandResume(ctx context.Context, dataDir string, sessionID domain.SessionID, cwd, nativeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !commandNativeID.MatchString(nativeID) || SessionHomeDir(dataDir, sessionID) == "" {
		return errors.New("invalid command-code imported resume identity")
	}
	bucket, err := commandNativeProjectBucket(cwd)
	if err != nil {
		return err
	}
	target := filepath.Join(SessionHomeDir(dataDir, sessionID), ".commandcode")
	root, err := os.OpenRoot(target)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return commandReadable(ctx, root, filepath.Join("projects", bucket, nativeID+".jsonl"), nativeID)
}

// ValidateNativeImport verifies the selected v3 native tree without running the
// provider. Sharing this with discovery prevents admitting a file the native
// loader would discard and replace with an empty conversation.
func ValidateNativeImport(ctx context.Context, root, path, id string) error {
	if !commandNativeID.MatchString(id) {
		return errors.New("invalid command-code native session id")
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) || filepath.Base(rel) != id+".jsonl" {
		return errors.New("command-code transcript is outside its profile")
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = provider.Close() }()
	return commandReadable(ctx, provider, rel, id)
}

func commandReadable(ctx context.Context, root *os.Root, rel, id string) error {
	info, err := root.Lstat(rel)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<30 {
		return errors.New("command-code transcript is not a bounded regular file")
	}
	f, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(io.LimitReader(f, 1<<30))
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	var header struct {
		Type      string  `json:"type"`
		Version   int     `json:"version"`
		ID        string  `json:"id"`
		Timestamp *string `json:"timestamp"`
		CWD       *string `json:"cwd"`
	}
	if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &header) != nil || header.Type != "session" || header.Version != 3 || header.ID != id || header.Timestamp == nil || header.CWD == nil {
		return errors.New("command-code native header is missing or mismatched")
	}
	type link struct {
		parent string
		user   bool
	}
	links := map[string]link{}
	leaf := ""
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var entry struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Parent    json.RawMessage `json:"parentId"`
			Timestamp *string         `json:"timestamp"`
			Message   struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			return errors.New("command-code native history is corrupt")
		}
		var parent *string
		if entry.ID == "" || len(entry.ID) > 256 || entry.Timestamp == nil || json.Unmarshal(entry.Parent, &parent) != nil {
			return errors.New("command-code native entry is missing required fields")
		}
		switch entry.Type {
		case "message", "model_change", "effort_change", "compaction", "branch_summary", "custom", "custom_message", "label", "session_info":
		default:
			return errors.New("unsupported command-code native entry")
		}
		node := link{}
		if parent != nil {
			node.parent = *parent
		}
		if entry.Type == "message" && entry.Message.Role == "user" {
			var text string
			if json.Unmarshal(entry.Message.Content, &text) == nil {
				node.user = strings.TrimSpace(text) != ""
			} else {
				var blocks []struct{ Type, Text string }
				if json.Unmarshal(entry.Message.Content, &blocks) == nil {
					for _, block := range blocks {
						if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
							node.user = true
						}
					}
				}
			}
		}
		links[entry.ID], leaf = node, entry.ID
		if len(links) > 1<<20 {
			return errors.New("command-code native tree exceeds read budget")
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for leaf != "" && !seen[leaf] {
		node, ok := links[leaf]
		if !ok {
			break
		}
		if node.user {
			return nil
		}
		seen[leaf], leaf = true, node.parent
	}
	return errors.New("command-code native history has no conversation")
}

func copyCommandBundle(ctx context.Context, root *os.Root, sourceDir, targetRoot, bucket, id string) error {
	target, err := os.OpenRoot(targetRoot)
	if err != nil {
		return err
	}
	defer func() { _ = target.Close() }()
	if err := target.Mkdir("projects", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	projects, err := target.Lstat("projects")
	if err != nil {
		return err
	}
	if !projects.IsDir() {
		return errors.New("command-code destination projects is not a directory")
	}
	dest := filepath.Join("projects", bucket)
	if _, err := target.Lstat(filepath.Join(dest, id+".jsonl")); err == nil {
		return commandReadable(ctx, target, filepath.Join(dest, id+".jsonl"), id)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Refuse replacing an existing project directory, even when it does not yet
	// contain this id: another native conversation may already own it.
	if _, err := target.Lstat(dest); err == nil {
		return errors.New("command-code destination project already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Join(targetRoot, "projects"), ".ao-import-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	for _, suffix := range []string{".jsonl", ".meta.json", ".share.json", ".checkpoints.jsonl", ".prompts.jsonl"} {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := id + suffix
		info, err := root.Lstat(filepath.Join(sourceDir, name))
		if errors.Is(err, os.ErrNotExist) && suffix != ".jsonl" {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<30 {
			return errors.New("command-code sidecar is not a bounded regular file")
		}
		f, err := root.Open(filepath.Join(sourceDir, name))
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(stage, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = f.Close()
			return err
		}
		copied, copyErr := io.Copy(out, io.LimitReader(f, info.Size()))
		closeErr := out.Close()
		_ = f.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return fmt.Errorf("copy command-code native history: %w", err)
		}
		if copied != info.Size() {
			return errors.New("command-code native history changed during copy")
		}
	}
	prepared, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	readErr := commandReadable(ctx, prepared, id+".jsonl", id)
	_ = prepared.Close()
	if readErr != nil {
		return readErr
	}
	return os.Rename(stage, filepath.Join(targetRoot, dest))
}
