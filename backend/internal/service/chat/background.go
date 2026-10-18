package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// RunBackgroundTask executes a short provider turn without publishing a Chat
// controller or conversation into AO's durable session history.
func (s *Service) RunBackgroundTask(ctx context.Context, harness domain.AgentHarness, cfg ports.ChatStartConfig, prompt string) (_ string, err error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("background task prompt is empty")
	}
	if s.newID == nil {
		return "", errors.New("background task id factory is unavailable")
	}
	driver, err := s.drivers.Driver(harness)
	if err != nil {
		return "", fmt.Errorf("background task driver for %s: %w", harness, err)
	}

	cfg.SessionID = domain.SessionID("background-" + s.newID())
	cfg.ProviderScopeID = s.newID()
	cfg.ProviderIDsScoped = true
	cfg.Ephemeral = true
	cfg.Effort = ""
	// shortcut: ACP ignores ReadOnly and DisableNetwork; enforce tool restrictions when those providers support them.
	cfg.ReadOnly = true
	cfg.DisableNetwork = true
	conversation, err := driver.Start(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("start background %s task: %w", harness, err)
	}
	defer func() {
		if cleanupErr := cleanupUnpublishedConversation(conversation, true); err == nil && cleanupErr != nil {
			err = fmt.Errorf("stop background %s task: %w", harness, cleanupErr)
		}
	}()

	return runBackgroundTurn(ctx, harness, conversation, prompt)
}

func runBackgroundTurn(ctx context.Context, harness domain.AgentHarness, conversation ports.ChatConversation, prompt string) (string, error) {
	settings, err := backgroundTaskSettings(ctx, conversation)
	if err != nil {
		return "", fmt.Errorf("configure background %s task: %w", harness, err)
	}
	turn, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
		Text:     prompt,
		Origin:   domain.MessageOriginAutomation,
		Settings: settings,
	})
	if err != nil {
		return "", fmt.Errorf("send background %s task: %w", harness, err)
	}
	if deferred, ok := conversation.(ports.ChatDeferredTurnStarter); ok {
		if err := deferred.StartDeferredTurn(turn.ProviderTurnID); err != nil {
			return "", fmt.Errorf("start deferred background %s task: %w", harness, err)
		}
	}

	var answer string
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case event, ok := <-conversation.Events():
			if !ok {
				return "", errors.New("background task provider stopped before completing")
			}
			if event.ProviderTurnID != "" && event.ProviderTurnID != turn.ProviderTurnID {
				continue
			}
			switch event.Kind {
			case ports.ChatEventMessageCompleted:
				if text := strings.TrimSpace(event.Text); text != "" {
					answer = text
				}
			case ports.ChatEventApprovalRequested, ports.ChatEventInputRequested:
				return "", errors.New("background task requested user interaction")
			case ports.ChatEventError:
				if event.Err != nil {
					return "", event.Err
				}
				return "", errors.New("background task provider reported an error")
			case ports.ChatEventTurnCompleted:
				if event.TurnState != domain.TurnStateCompleted {
					return "", fmt.Errorf("background task ended with %s", event.TurnState)
				}
				if answer == "" {
					return "", errors.New("background task returned no text")
				}
				return answer, nil
			}
		}
	}
}

// The provider's live catalog keeps this text-only call from inheriting an
// expensive reasoning setting or choosing a level its model cannot use.
func backgroundTaskSettings(ctx context.Context, conversation ports.ChatConversation) (ports.ChatTurnSettings, error) {
	switch provider := conversation.(type) {
	case ports.ChatConfigOptionController:
		catalog, err := provider.ListConfigOptions(ctx)
		if err != nil {
			return ports.ChatTurnSettings{}, err
		}
		for _, option := range catalog {
			if option.Category != "thought_level" || option.Type != ports.ChatConfigOptionSelect {
				continue
			}
			values := make([]string, 0, len(option.Choices))
			for _, choice := range option.Choices {
				values = append(values, choice.Value)
			}
			if effort := lowestBackgroundEffort(values); effort != "" && effort != option.Current.Select {
				if _, err := provider.SetConfigOption(ctx, option.ID, ports.ChatConfigOptionValue{Select: effort}); err != nil {
					return ports.ChatTurnSettings{}, err
				}
			}
		}
	case ports.ChatModelLister:
		catalog, err := provider.ListModels(ctx)
		if err != nil {
			return ports.ChatTurnSettings{}, err
		}
		for _, model := range catalog {
			if model.Default {
				return ports.ChatTurnSettings{Effort: lowestBackgroundEffort(model.Efforts)}, nil
			}
		}
	}
	return ports.ChatTurnSettings{}, nil
}

func lowestBackgroundEffort(values []string) string {
	var best string
	bestRank := 8
	for _, value := range values {
		var rank int
		switch strings.ToLower(value) {
		case "none", "off":
			rank = 0
		case "minimal":
			rank = 1
		case "low":
			rank = 2
		case "medium":
			rank = 3
		case "high":
			rank = 4
		case "xhigh":
			rank = 5
		case "max":
			rank = 6
		case "ultra":
			rank = 7
		default:
			continue
		}
		if rank < bestRank {
			best, bestRank = value, rank
		}
	}
	return best
}
