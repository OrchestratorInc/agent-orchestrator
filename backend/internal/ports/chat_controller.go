package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type ChatHistoryMode uint8

const (
	ChatHistoryImport ChatHistoryMode = iota
	ChatHistoryRequired
	ChatHistoryDeferred
)

type ChatControllerStart struct {
	ReconnectOnly                      bool
	AwakeReconnectTarget               *domain.AwakeAgentTarget
	SessionID                          domain.SessionID
	Owner                              domain.ConversationOwner
	ProjectID                          domain.ProjectID
	Kind                               domain.SessionKind
	Harness                            domain.AgentHarness
	DataDir                            string
	WorkspacePath                      string
	Env                                map[string]string
	Model                              string
	Effort                             string
	Permissions                        PermissionMode
	ReadOnly                           bool
	ForkHandoffNote                    string
	ForkFirstStart                     bool
	SystemPrompt                       string
	AdditionalDirectories              []string
	MCPServers                         []ChatMCPServerConfig
	ExpectedControllerOwner            domain.SessionControllerOwner
	PrepareControllerEnv               func(context.Context, domain.SessionControllerOwner) (map[string]string, error)
	ProviderConversationID             string
	FreshIfProviderConversationMissing bool
	ProviderScopeID                    string
	ProviderHandoff                    *domain.ChatProviderHandoff
	ProviderSwitch                     *domain.ChatProviderSwitch
	ControllerGeneration               string
	HistoryMode                        ChatHistoryMode
	HistoryPolicy                      domain.SessionInterfaceTransitionHistoryPolicy
	ControllerReady                    func(ChatControllerStarted) (ChatControllerCommit, error)
}

type ChatControllerCommit struct {
	Conversation    domain.ConversationRecord
	ControllerOwner domain.SessionControllerOwner
}

type ChatControllerStarted struct {
	LiveReconnect          bool
	ProviderConversationID string
	ControllerGeneration   string
	Conversation           domain.ConversationRecord
	ProviderBoundary       *domain.ConversationBranch
	CommitProviderHistory  func(context.Context) error
}
