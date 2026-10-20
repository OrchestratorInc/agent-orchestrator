package triage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AgentTransport is either the selected driver's native Chat controller or its
// registered interactive terminal.
type AgentTransport struct {
	Conversation ports.ChatConversation
	Terminal     *TerminalAgent
}

// DefaultOptions wires native agent transports without constructing the daemon
// or opening its database.
func DefaultOptions(dataDir string) Options {
	catalog := NewCatalog(dataDir)
	agents := terminalHarnesses()
	return Options{DataDir: dataDir, Deps: Dependencies{
		Catalog: catalog.Choices, Configure: catalog.Configure,
		Collect: func(ctx context.Context, origin Origin) ([]Evidence, error) {
			return Collect(ctx, dataDir, origin)
		},
		RecoverConversation: func(ctx context.Context, _ Run) error {
			nativeErr := reconcileDiagnosticHosts(ctx, dataDir, persistenthost.Reconcile)
			_, terminalErr := newHarnessRuntime(ctx, dataDir)
			return errors.Join(nativeErr, terminalErr)
		},
		StartAgent: func(ctx context.Context, run Run) (AgentTransport, error) {
			selection, accountEnv, err := catalog.ResolveSelection(ctx, run.Request.Selection)
			if err != nil {
				return AgentTransport{}, err
			}
			run.Request.Selection = selection
			if err := reconcileDiagnosticHosts(ctx, dataDir, persistenthost.Reconcile); err != nil {
				return AgentTransport{}, err
			}
			paths := InvestigationPaths(ctx, dataDir, run.Request.Origin)
			if driver, driverErr := catalog.chat.Driver(domain.AgentHarness(selection.Harness)); driverErr == nil && selection.SessionMode == domain.SessionModeChat {
				conv, startErr := startDiagnosticConversation(ctx, dataDir, driver, run, accountEnv, paths)
				if startErr == nil || !errors.Is(startErr, ports.ErrChatDriverUnavailable) {
					return AgentTransport{Conversation: conv}, startErr
				}
			}
			if selection.Harness == string(domain.HarnessUnreal) {
				return AgentTransport{}, errors.New("unreal agent requires its native Chat transport")
			}
			runtime, err := newHarnessRuntime(ctx, dataDir)
			if err != nil {
				return AgentTransport{}, err
			}
			agent := agents[selection.Harness]
			if agent == nil {
				return AgentTransport{}, errors.New("the selected agent has no registered launch adapter")
			}
			workspace, _, err := diagnosticWorkspace(dataDir, run, paths)
			if err != nil {
				return AgentTransport{}, err
			}
			prompt := agentStartupPrompt()
			terminal, err := launchTerminalAgent(ctx, run, workspace, dataDir, prompt, diagnosticEnv(os.Environ(), accountEnv), agent, runtime)
			return AgentTransport{Terminal: terminal}, err
		},
	}}
}

func reconcileDiagnosticHosts(ctx context.Context, dataDir string, reconcile func(context.Context, string, map[string]struct{}) error) error {
	cleanupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	root := filepath.Join(dataDir, "reports", "triage", "provider-hosts")
	if err := privateDir(root); err != nil {
		return err
	}
	if err := reconcile(cleanupCtx, root, map[string]struct{}{}); err != nil {
		return fmt.Errorf("stop interrupted diagnostic provider: %w", err)
	}
	return nil
}

func startDiagnosticConversation(ctx context.Context, dataDir string, driver ports.ChatDriver, run Run, accountEnv, paths map[string]string) (ports.ChatConversation, error) {
	selection := run.Request.Selection
	workspace, providerRoot, err := diagnosticWorkspace(dataDir, run, paths)
	if err != nil {
		return nil, err
	}
	conv, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: domain.SessionID(run.ID), DataDir: providerRoot,
		WorkspacePath: workspace, Env: diagnosticEnv(os.Environ(), accountEnv),
		Model: selection.Model, Effort: selection.Effort,
		Permissions: selection.Permissions,
	})
	if err != nil {
		if conv != nil {
			err = errors.Join(err, stopProviderConversation(conv, ""))
		}
		return nil, err
	}
	if conv == nil {
		return nil, errors.New("the selected agent returned no Chat controller")
	}
	var needed []ports.ChatCapability
	for _, capability := range []ports.ChatCapability{ports.ChatCapabilityStreaming, ports.ChatCapabilityInterrupt} {
		if !conv.Capabilities().Has(capability) {
			needed = append(needed, capability)
		}
	}
	if len(needed) != 0 {
		admission := &ports.ChatCapabilityError{Harness: driver.Harness(), Missing: needed}
		return nil, errors.Join(admission, stopProviderConversation(conv, ""))
	}
	return conv, nil
}

func stopProviderConversation(conv ports.ChatConversation, turnID string) error {
	if terminator, ok := conv.(ports.ChatProviderTerminator); ok {
		return terminator.Terminate()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	interruptErr := conv.Interrupt(ctx, turnID)
	return errors.Join(interruptErr, conv.Close())
}

func diagnosticWorkspace(dataDir string, run Run, paths map[string]string) (string, string, error) {
	dir, err := RunDir(dataDir, run.ID)
	if err != nil {
		return "", "", err
	}
	workspace := filepath.Join(dir, "workspace")
	providerRoot := filepath.Join(dataDir, "reports", "triage", "provider-hosts")
	for _, path := range []string{dir, workspace, providerRoot} {
		if err := privateDir(path); err != nil {
			return "", "", fmt.Errorf("prepare diagnostic workspace: %w", err)
		}
	}
	snapshot := map[string]any{"origin": run.Request.Origin, "evidence": run.Evidence,
		"humanObservation": run.Request.Observation, "operationalPaths": paths}
	if err := atomicPrivateJSON(filepath.Join(workspace, "diagnostics.json"), snapshot); err != nil {
		return "", "", fmt.Errorf("save diagnostic snapshot: %w", err)
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(workspace, "triage.md"), []byte(InvestigationInstructions(run.Request.PublishAuthorized)), 0o600); err != nil {
		return "", "", fmt.Errorf("save private investigation instructions: %w", err)
	}
	return workspace, providerRoot, nil
}

// diagnosticEnv keeps the selected native login while clearing AO delegation
// credentials from the provider process.
func diagnosticEnv(inherited []string, account map[string]string) map[string]string {
	env := make(map[string]string, len(inherited)+len(account))
	for _, entry := range inherited {
		key, value, ok := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if !ok {
			continue
		}
		if strings.HasPrefix(upper, "AO_MCP_") || strings.HasPrefix(upper, "AO_SESSION") || upper == "AO_GITHUB_TOKEN" ||
			upper == "AO_SIDE_CONVERSATION_ID" || strings.HasPrefix(upper, "AO_") && strings.HasSuffix(upper, "_CAPABILITY") {
			env[key] = ""
			continue
		}
		env[key] = value
	}
	for key, value := range account {
		env[key] = value
	}
	return env
}
