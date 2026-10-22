//go:build darwin || linux

package unrealagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/unreallabsai/unreal-agent/harness/session"
	"github.com/unreallabsai/unreal-agent/harness/sessionstore"
	"github.com/unreallabsai/unreal-agent/harness/sessionstore/localfile"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatIsolatedForker = (*Driver)(nil)
var _ ports.ChatForker = (*conversation)(nil)
var _ ports.ChatForkDeleter = (*conversation)(nil)

// ForkIntoHost starts an independent provider at the selected request boundary.
func (d *Driver) ForkIntoHost(ctx context.Context, sourceID, lastTurnID string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if err := validateStart(cfg.WorkspacePath, cfg.Permissions, cfg.ReadOnly, cfg.AdditionalDirectories); err != nil {
		return nil, err
	}
	if err := d.validateLaunchAuth(ctx, cfg.Env); err != nil {
		return nil, err
	}
	operationCtx := ctx
	var release func()
	if cfg.AcquireForkSource != nil {
		var err error
		ctx, release, err = cfg.AcquireForkSource(ctx)
		if err != nil {
			return nil, err
		}
	}
	id, err := forkNativeSession(ctx, cfg.DataDir, sourceID, lastTurnID)
	if release != nil {
		release()
	}
	ctx = operationCtx
	if err != nil {
		return nil, err
	}
	if cfg.RecordFork != nil {
		if err := cfg.RecordFork(context.WithoutCancel(ctx), id); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			return nil, errors.Join(err, deleteNativeFork(cleanupCtx, cfg.DataDir, sourceID, id))
		}
	}
	conv, err := d.Resume(ctx, ports.ChatResumeConfig{
		SessionID: cfg.SessionID, ProviderConversationID: id, DataDir: cfg.DataDir,
		WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, PrepareEnv: cfg.PrepareEnv,
		Model: cfg.Model, Effort: cfg.Effort, Permissions: cfg.Permissions,
		ReadOnly: cfg.ReadOnly, SystemPrompt: cfg.SystemPrompt, ProviderScopeID: cfg.ProviderScopeID,
	})
	reconnected, ok := conv.(ports.ChatLiveReconnector)
	if err == nil && ok && reconnected.ReconnectedLive() {
		_ = conv.Close()
		err = fmt.Errorf("%w: destination Unreal Agent host already owns a conversation", ports.ErrChatRecoveryInconclusive)
	}
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return nil, errors.Join(err, deleteNativeFork(cleanupCtx, cfg.DataDir, sourceID, id))
	}
	return conv, nil
}

func (c *conversation) Fork(ctx context.Context, anchor *string) (string, error) {
	id := ""
	if anchor != nil {
		id = *anchor
		if id == "" {
			return "", fmt.Errorf("unreal agent fork anchor is empty")
		}
	}
	return forkNativeSession(ctx, c.cfg.DataDir, c.cfg.ProviderConversationID, id)
}

func (c *conversation) DeleteFork(ctx context.Context, id string) error {
	return deleteNativeFork(ctx, c.cfg.DataDir, c.cfg.ProviderConversationID, id)
}

func nativeSessionDirectory(dataDir string) string {
	return filepath.Join(dataDir, "agent-runtime", string(domain.HarnessUnreal), "sessions")
}

func forkNativeSession(ctx context.Context, dataDir, sourceID, anchor string) (string, error) {
	if !filepath.IsAbs(dataDir) || sourceID == "" {
		return "", fmt.Errorf("unreal agent fork requires an absolute data directory and source id")
	}
	store, err := localfile.New(nativeSessionDirectory(dataDir))
	if err != nil {
		return "", err
	}
	nativeTurn, err := nativeForkTurn(ctx, dataDir, sourceID, anchor)
	if err != nil {
		return "", fmt.Errorf("read Unreal Agent fork source: %w", err)
	}
	id := uuid.NewString()
	if _, err := store.Fork(ctx, session.ID(id), session.ID(sourceID), nativeTurn); err != nil {
		return "", fmt.Errorf("fork Unreal Agent session: %w", err)
	}
	return id, nil
}

// Read only input/turn identifiers; the SDK validates and decodes the full log
// once when writing the fork, including compaction and tool history.
func nativeForkTurn(ctx context.Context, dataDir, sourceID, anchor string) (session.TurnID, error) {
	if sourceID == "." || sourceID == ".." || strings.ContainsAny(sourceID, "/\\") {
		return "", errors.New("invalid source session id")
	}
	file, err := os.Open(filepath.Join(nativeSessionDirectory(dataDir), sourceID+".session.jsonl"))
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	// AO identifies a request by its external input ID. One request can span
	// several native turns (tools or compaction); copy through its final turn.
	var nativeTurn session.TurnID
	found := anchor == ""
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break
		} // Ignore uncommitted trailing records, as the SDK does.
		if err != nil {
			return "", err
		}
		var record struct {
			Type string
			Data struct {
				Item struct {
					Kind sessionstore.ItemKind
					Data struct {
						ID   string
						Kind string
					}
				}
			}
		}
		if err := json.Unmarshal(line, &record); err != nil {
			return "", err
		}
		item := record.Data.Item
		if record.Type != "item" {
			continue
		}
		if item.Kind == sessionstore.ItemInput && item.Data.Kind == "external" && anchor != "" {
			if found && item.Data.ID != anchor {
				break
			}
			found = item.Data.ID == anchor
		} else if item.Kind == sessionstore.ItemTurn && found {
			nativeTurn = session.TurnID(item.Data.ID)
		}
	}
	if !found || nativeTurn == "" {
		return "", fmt.Errorf("%w: Unreal Agent fork anchor is unavailable", ports.ErrChatCapabilityUnavailable)
	}
	return nativeTurn, nil
}

func deleteNativeFork(ctx context.Context, dataDir, parentID, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(dataDir) || id == parentID {
		return fmt.Errorf("refusing to delete the active Unreal Agent conversation")
	}
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("invalid Unreal Agent fork id: %w", err)
	}
	// The pinned SDK has no delete API. Only AO-registered child IDs reach here.
	err := os.Remove(filepath.Join(nativeSessionDirectory(dataDir), id+".session.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
