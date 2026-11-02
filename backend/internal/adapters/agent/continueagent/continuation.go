package continueagent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var continueNativeID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
)

// NativeSessionConfigDir resolves Continue's native state root without changing
// account state or invoking the provider. A launch-local override takes priority.
func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, set := env["CONTINUE_GLOBAL_DIR"]
	if !set {
		root = os.Getenv("CONTINUE_GLOBAL_DIR")
	}
	if root != "" {
		return filepath.Abs(root)
	}
	home := env["HOME"]
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".continue"), nil
}

type continueSession struct {
	ID        string           `json:"sessionId"`
	Workspace string           `json:"workspaceDirectory"`
	History   []map[string]any `json:"history"`
}

func readContinueSession(ctx context.Context, root *os.Root, id string) (continueSession, error) {
	var session continueSession
	if err := ctx.Err(); err != nil {
		return session, err
	}
	if !continueNativeID.MatchString(id) {
		return session, errors.New("continue requires a full native session id")
	}
	rel := filepath.Join("sessions", id+".json")
	info, err := root.Lstat(rel)
	if err != nil {
		return session, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return session, errors.New("continue session is not a bounded regular file")
	}
	f, err := root.Open(rel)
	if err != nil {
		return session, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, (64<<20)+1))
	if err != nil {
		return session, err
	}
	if len(data) > 64<<20 {
		return session, errors.New("continue session exceeds read budget")
	}
	if err := json.Unmarshal(data, &session); err != nil {
		return session, err
	}
	if session.ID != id || !filepath.IsAbs(session.Workspace) || len(session.History) == 0 {
		return session, errors.New("continue native identity, workspace or history missing")
	}
	return session, ctx.Err()
}

// continueHistoryDigest mirrors session.modifySessionBeforeSave: system items
// are omitted and a user item's editorState is set to its message content.
// Every remaining field participates, including native message ids and tools.
func continueHistoryDigest(history []map[string]any) ([32]byte, error) {
	var normalized []map[string]any
	for _, item := range history {
		message, ok := item["message"].(map[string]any)
		if !ok {
			return [32]byte{}, errors.New("continue history item has no native message")
		}
		role, _ := message["role"].(string)
		if role == "system" {
			continue
		}
		if role != "user" && role != "assistant" && role != "tool" {
			return [32]byte{}, errors.New("continue native message role is invalid")
		}
		copyItem := make(map[string]any, len(item)+1)
		for key, value := range item {
			copyItem[key] = value
		}
		if role == "user" {
			copyItem["editorState"] = message["content"]
		}
		normalized = append(normalized, copyItem)
	}
	if len(normalized) == 0 {
		return [32]byte{}, errors.New("continue native history is empty")
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(data), nil
}

type continueForkProof struct {
	root, sourceID, workspace string
	before                    map[string]bool
	digest                    [32]byte
}

func continueSessionIDs(ctx context.Context, root *os.Root) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := root.Open("sessions")
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(32769)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > 32768 {
		return nil, errors.New("continue session inventory exceeds read budget")
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") && continueNativeID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids, ctx.Err()
}

func prepareContinueFork(ctx context.Context, configDir, sourceID, workspace string) (*continueForkProof, error) {
	workspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(configDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	source, err := readContinueSession(ctx, root, sourceID)
	if err != nil {
		return nil, err
	}
	digest, err := continueHistoryDigest(source.History)
	if err != nil {
		return nil, err
	}
	ids, err := continueSessionIDs(ctx, root)
	if err != nil {
		return nil, err
	}
	before := make(map[string]bool, len(ids))
	for _, id := range ids {
		before[id] = true
	}
	return &continueForkProof{root: configDir, sourceID: sourceID, workspace: workspace, before: before, digest: digest}, nil
}

// resolve considers only files absent before this fork launch. A candidate
// must preserve the complete native history and name the exact AO worktree.
// It never selects the globally newest file, a rewritten old id, or a prefix.
func (p *continueForkProof) resolve(ctx context.Context) (string, bool, error) {
	root, err := os.OpenRoot(p.root)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = root.Close() }()
	ids, err := continueSessionIDs(ctx, root)
	if err != nil {
		return "", false, err
	}
	var found string
	for _, id := range ids {
		if p.before[id] || id == p.sourceID {
			continue
		}
		session, err := readContinueSession(ctx, root, id)
		if err != nil {
			if ctx.Err() != nil {
				return "", false, ctx.Err()
			}
			continue // A provider save may be in progress; no partial file is proof.
		}
		workspace, err := filepath.EvalSymlinks(session.Workspace)
		if err != nil || workspace != p.workspace {
			continue
		}
		digest, err := continueHistoryDigest(session.History)
		if err != nil || digest != p.digest {
			continue
		}
		if found != "" {
			return "", false, errors.New("continue fork identity is ambiguous")
		}
		found = id
	}
	return found, found != "", nil
}

// LocateTranscript finds exact native Continue history within its state root.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if ref.ConfigDir == "" {
		return "", false, ctx.Err()
	}
	root, err := os.OpenRoot(ref.ConfigDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { _ = root.Close() }()
	session, err := readContinueSession(ctx, root, ref.NativeSessionID)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if _, err := continueHistoryDigest(session.History); err != nil {
		return "", false, err
	}
	return filepath.Join(ref.ConfigDir, "sessions", ref.NativeSessionID+".json"), true, nil
}

// ProbeNativeSession reports whether the exact fork source has usable history.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	_, found, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if found {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}
