// Package coderagents is the Chat driver for Coder Agents. Inference and the
// chat loop run in the user's Coder deployment; every file and command tool
// is a client-executed tool (`unsafe_dynamic_tools`) that AO runs in the
// local session worktree, behind AO's own approvals. AO never lets the chat
// bind or create a Coder workspace: such a chat would act on a remote
// filesystem, not on the AO worktree.
package coderagents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	coderplugin "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Chat labels AO puts on the Coder chats it creates. The conversation label
// is how Resume finds the chat again; the session label proves ownership.
const (
	labelConversation = "ao.conversation"
	labelSession      = "ao.session"
)

type plugin interface {
	Connect(ctx context.Context, env map[string]string) (*coderapi.Client, coderapi.Credentials, error)
}

// Driver opens Coder Agents conversations.
type Driver struct {
	plugin plugin
	log    *slog.Logger
}

// New creates the Coder Agents Chat driver.
func New(plugin plugin, log *slog.Logger) *Driver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Driver{plugin: plugin, log: log}
}

var _ ports.ChatDriver = (*Driver)(nil)
var _ ports.ChatPermissionModeChecker = (*Driver)(nil)

// Harness returns the harness this driver serves.
func (*Driver) Harness() domain.AgentHarness { return domain.HarnessCoderAgents }

func capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilityTools:     true,
		ports.ChatCapabilityApprovals: true,
		ports.ChatCapabilityInterrupt: true,
		ports.ChatCapabilityResume:    true,
		ports.ChatCapabilityUsage:     true,
		ports.ChatCapabilityModels:    true,
	}
}

// SupportsPermissionMode reports the modes AO can honor. AO itself gates
// every edit and command, so ask-first, accept-edits and bypass all work.
// Auto needs a provider sandbox to run commands unasked, which Coder's
// client-executed tools do not have.
func (*Driver) SupportsPermissionMode(mode ports.PermissionMode) bool {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, ports.PermissionModeBypassPermissions:
		return true
	default:
		return false
	}
}

// Probe checks that a driver can exist. Authentication depends on the
// project environment, so it is checked authoritatively at Start/Resume.
func (d *Driver) Probe(ctx context.Context) (ports.ChatCapabilities, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.plugin == nil {
		return nil, fmt.Errorf("%w: Coder Agents plugin is unavailable", ports.ErrChatDriverUnavailable)
	}
	return capabilities(), nil
}

// session is what Start and Resume establish before a conversation exists.
type session struct {
	client *coderapi.Client
	user   coderapi.User
	org    coderapi.Organization
	models []coderapi.ChatModel
}

func (d *Driver) connect(ctx context.Context, env map[string]string) (session, error) {
	client, _, err := d.plugin.Connect(ctx, env)
	if errors.Is(err, coderapi.ErrNotLoggedIn) {
		return session{}, fmt.Errorf("%w: %w", ports.ErrChatAuthRequired, err)
	}
	if err != nil {
		return session{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	}
	requestCtx, cancel := coderapi.WithRequestTimeout(ctx)
	defer cancel()
	user, err := client.Me(requestCtx)
	if err != nil {
		return session{}, classify(err)
	}
	org, err := coderapi.ResolveOrganization(requestCtx, client, coderplugin.Organization(env))
	if err != nil {
		return session{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	}
	models, err := client.ChatModels(requestCtx, org.ID)
	if coderapi.StatusCode(err) == http.StatusNotFound {
		return session{}, fmt.Errorf("%w: this Coder deployment has no /api/v2/chats API; Coder Agents needs Coder v2.37 or newer", ports.ErrChatDriverIncompatible)
	}
	if err != nil {
		return session{}, classify(err)
	}
	enabled := coderapi.EnabledModels(models)
	if len(enabled) == 0 {
		return session{}, fmt.Errorf("%w: no Coder Agents model is enabled for organization %q", ports.ErrChatDriverUnavailable, org.Name)
	}
	return session{client: client, user: user, org: org, models: enabled}, nil
}

// classify maps an API failure to AO's typed chat errors.
func classify(err error) error {
	switch coderapi.StatusCode(err) {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %w", ports.ErrChatAuthRequired, err)
	case 0:
		// Transport failures, including a refused cross-origin redirect.
		return fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	default:
		return err
	}
}

func validateLaunch(workspace string, permissions ports.PermissionMode, additional []string) error {
	if !filepath.IsAbs(workspace) {
		return fmt.Errorf("workspace path must be absolute, got %q", workspace)
	}
	for _, dir := range additional {
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("additional directory must be absolute, got %q", dir)
		}
	}
	if !(&Driver{}).SupportsPermissionMode(permissions) {
		return fmt.Errorf("%w: Coder Agents supports ask-first, accept-edits and bypass-permissions; AO has no sandbox for auto mode", ports.ErrChatPermissionModeUnsupported)
	}
	return nil
}

func (s session) model(id string) (coderapi.ChatModel, error) {
	id = strings.TrimSpace(id)
	for _, model := range s.models {
		if model.ID == id {
			return model, nil
		}
	}
	return coderapi.ChatModel{}, fmt.Errorf("%w: Coder Agents model config %q is not enabled for this deployment", ports.ErrChatConfigOptionInvalid, id)
}

// Start prepares a new conversation. The Coder chat itself is created with
// the first message, because Coder's create call requires one; the
// conversation key AO persists finds it again through its label.
func (d *Driver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if err := validateLaunch(cfg.WorkspacePath, cfg.Permissions, cfg.AdditionalDirectories); err != nil {
		return nil, err
	}
	s, err := d.connect(ctx, cfg.Env)
	if err != nil {
		return nil, err
	}
	conv, err := newConversation(d.log, s, conversationConfig{
		key: uuid.NewString(), sessionID: string(cfg.SessionID), dataDir: cfg.DataDir,
		workspace: cfg.WorkspacePath, additional: cfg.AdditionalDirectories, env: cfg.Env,
		model: cfg.Model, effort: cfg.Effort, permissions: cfg.Permissions,
		readOnly: cfg.ReadOnly, disableTools: cfg.DisableTools, systemPrompt: cfg.SystemPrompt,
	})
	if err != nil {
		return nil, err
	}
	return conv, nil
}

// Resume reattaches to the Coder chat that carries the conversation key. A
// missing, foreign, archived or workspace-bound chat is a typed resume failure,
// never a silent new conversation.
func (d *Driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	key := strings.TrimSpace(cfg.ProviderConversationID)
	if _, err := uuid.Parse(key); err != nil {
		return nil, fmt.Errorf("%w: Coder Agents conversation id %q is not an AO conversation key", ports.ErrChatResumeFailed, key)
	}
	if err := validateLaunch(cfg.WorkspacePath, cfg.Permissions, cfg.AdditionalDirectories); err != nil {
		return nil, err
	}
	s, err := d.connect(ctx, cfg.Env)
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := coderapi.WithRequestTimeout(ctx)
	defer cancel()
	chats, err := s.client.ListChatsByLabels(requestCtx, map[string]string{labelConversation: key})
	if err != nil {
		return nil, fmt.Errorf("%w: find Coder chat: %w", ports.ErrChatResumeFailed, classify(err))
	}
	var found []coderapi.Chat
	for _, chat := range chats {
		if chat.OwnerID == s.user.ID && chat.ParentChatID == nil && chat.Labels[labelConversation] == key {
			found = append(found, chat)
		}
	}
	conversationCfg := conversationConfig{
		key: key, sessionID: string(cfg.SessionID), dataDir: cfg.DataDir,
		workspace: cfg.WorkspacePath, additional: cfg.AdditionalDirectories, env: cfg.Env,
		model: cfg.Model, effort: cfg.Effort, permissions: cfg.Permissions,
		readOnly: cfg.ReadOnly, systemPrompt: cfg.SystemPrompt,
	}
	switch {
	case len(found) == 0 && !cfg.HasPriorTurns:
		// The first message never reached Coder, so no chat exists yet. The
		// conversation creates it on its first turn under the same key.
		return asConversation(newConversation(d.log, s, conversationCfg))
	case len(found) == 0:
		return nil, fmt.Errorf("%w: the Coder chat for this conversation no longer exists", ports.ErrChatResumeFailed)
	case len(found) > 1:
		return nil, fmt.Errorf("%w: %d Coder chats carry this conversation key", ports.ErrChatResumeFailed, len(found))
	}
	chat := found[0]
	if chat.Labels[labelSession] != string(cfg.SessionID) {
		return nil, fmt.Errorf("%w: the Coder chat belongs to another AO session", ports.ErrChatResumeFailed)
	}
	if chat.Archived {
		return nil, fmt.Errorf("%w: the Coder chat was archived", ports.ErrChatResumeFailed)
	}
	if chat.HasWorkspace() {
		return nil, fmt.Errorf("%w: the Coder chat is bound to a Coder workspace, so its tools no longer act on the AO worktree", ports.ErrChatResumeFailed)
	}
	conversationCfg.chat = &chat
	return asConversation(newConversation(d.log, s, conversationCfg))
}

// asConversation keeps a failed construction from becoming a non-nil
// interface holding a nil pointer.
func asConversation(conv *conversation, err error) (ports.ChatConversation, error) {
	if err != nil {
		return nil, err
	}
	return conv, nil
}

// mergedToolEnv is the command environment for client-executed tools.
func mergedToolEnv(env map[string]string) []string {
	return toolEnv(processenv.Merge(env))
}
