package auggie

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var nativeImportID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// NativeImportNode contains only visible text or the recorded workspace root.
type NativeImportNode struct {
	Type     int    `json:"type"`
	Content  string `json:"content"`
	TextNode struct {
		Content string `json:"content"`
	} `json:"text_node"`
	IDEState struct {
		Folders []struct {
			RepositoryRoot string `json:"repository_root"`
			FolderRoot     string `json:"folder_root"`
		} `json:"workspace_folders"`
	} `json:"ide_state_node"`
}

// NativeImportSession is the local session shape in Auggie 0.36.0.
type NativeImportSession struct {
	SessionID string `json:"sessionId"`
	Created   string `json:"created"`
	Modified  string `json:"modified"`
	Title     string `json:"title"`
	Custom    string `json:"customTitle"`
	Cloud     bool   `json:"isCloudAgent"`
	Poseidon  string `json:"poseidonAgentId"`
	Parent    string `json:"parentConversationId"`
	State     struct {
		Model string `json:"modelId"`
	} `json:"agentState"`
	History []struct {
		Summary  bool   `json:"isHistorySummary"`
		Finished string `json:"finishedAt"`
		Exchange struct {
			Request       string             `json:"request_message"`
			Response      string             `json:"response_text"`
			RequestNodes  []NativeImportNode `json:"request_nodes"`
			ResponseNodes []NativeImportNode `json:"response_nodes"`
		} `json:"exchange"`
	} `json:"chatHistory"`
}

// ReadNativeImportSession confines reads to the exact listed UUID and refuses
// cloud agents/subagents, whose execution directory is not the local worktree.
func ReadNativeImportSession(ctx context.Context, root, id string) (NativeImportSession, error) {
	var session NativeImportSession
	if err := ctx.Err(); err != nil {
		return session, err
	}
	if !nativeImportID.MatchString(id) {
		return session, errors.New("invalid auggie native session id")
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return session, err
	}
	defer func() { _ = provider.Close() }()
	rel := filepath.Join("sessions", id+".json")
	info, err := provider.Lstat(rel)
	if err != nil {
		return session, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return session, errors.New("auggie session is not a bounded regular file")
	}
	f, err := provider.Open(rel)
	if err != nil {
		return session, err
	}
	defer func() { _ = f.Close() }()
	if err := json.NewDecoder(io.LimitReader(f, info.Size())).Decode(&session); err != nil {
		return session, err
	}
	if session.SessionID != id || session.Created == "" || session.Modified == "" || session.History == nil || session.Cloud || session.Poseidon != "" || session.Parent != "" {
		return session, errors.New("auggie session is not a local resumable conversation")
	}
	return session, ctx.Err()
}

// NativeSessionConfigDir resolves Auggie's default cache; its alternative is a
// CLI --augment-cache-dir flag, not a provider environment override.
func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	home := env["HOME"]
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".augment"), nil
}

// ProbeNativeSession checks backing state without launching Auggie.
func (*Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	_, err := ReadNativeImportSession(ctx, ref.ConfigDir, ref.NativeSessionID)
	if errors.Is(err, os.ErrNotExist) {
		return ports.NativeSessionAvailabilityUnavailable, nil
	}
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	return ports.NativeSessionAvailabilityAvailable, nil
}

// LocateTranscript returns the exact native JSON file after validating it.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if _, err := ReadNativeImportSession(ctx, ref.ConfigDir, ref.NativeSessionID); err != nil {
		return "", false, err
	}
	return filepath.Join(ref.ConfigDir, "sessions", ref.NativeSessionID+".json"), true, nil
}

func appendRestoreWorkspace(cmd *[]string, cfg ports.RestoreConfig) {
	if cfg.Session.WorkspacePath != "" {
		*cmd = append(*cmd, "--workspace-root", cfg.Session.WorkspacePath)
	}
	if root := cfg.Session.Metadata[ports.MetadataKeyImportConfigDir]; root != "" {
		*cmd = append(*cmd, "--augment-cache-dir", root)
	}
}
