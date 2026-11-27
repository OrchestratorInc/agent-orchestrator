package chat

import (
	"context"
	"fmt"
	"slices"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The task catalog comes from agy models and also serves terminal sessions.
// Only the opened ACP session can say which of those models Chat can use.
// Stage the choice before publishing the controller; a pending provider boundary
// persists it and its notice in the activation transaction.
func (s *Service) prepareAgyOpeningModel(ctx context.Context, controller *Controller, requestedModel string, boundary *domain.ConversationBranch) (func(context.Context) error, error) {
	previous := controller.Settings()
	picked := previous.Model
	if picked == "" {
		// Native imports can have no saved settings even though the session's
		// model was passed to ACP during resume.
		picked = requestedModel
	}
	configurer, ok := controller.conv.(ports.ChatConfigOptionController)
	if picked == "" || !ok {
		return nil, nil
	}
	options, err := configurer.ListConfigOptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Agy opening model: %w", err)
	}
	for _, option := range options {
		if option.ID != "model" && option.Category != "model" {
			continue
		}
		live := option.Current.Select
		if live == "" || live == picked || slices.ContainsFunc(option.Choices, func(choice ports.ChatConfigOptionChoice) bool {
			return choice.Value == picked
		}) {
			return nil, nil
		}
		// A supported composite ID still names the selected base and effort.
		// Keeping it ensures that a restart reapplies the chosen effort.
		if resolver, ok := controller.conv.(ports.ChatModelVariantResolver); ok {
			if base, split := resolver.ModelVariantBase(picked); split && base == live {
				return nil, nil
			}
		}
		next, _ := settingsFromConfigOptions(previous, options)
		next.Model = live
		branchID := controller.conversation.ActiveBranchID
		if boundary != nil {
			branchID = boundary.ID
		}
		notice := domain.ConversationActivity{
			ID: s.newID(), Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
			Summary:        fmt.Sprintf("%s isn't available in Agy Chat. Using %s instead.", picked, live),
			ProviderItemID: branchID + ":agy-model-fallback:" + picked + ":" + live,
		}
		// This controller is unpublished and has no projection goroutine yet.
		controller.settings = next
		controller.conversation.Settings = next
		return func(commitCtx context.Context) error {
			if err := s.store.UpsertActivity(commitCtx, controller.conversation.ID, "", notice, s.now()); err != nil {
				return fmt.Errorf("record Agy model fallback notice: %w", err)
			}
			return controller.SetSettings(commitCtx, next)
		}, nil
	}
	return nil, nil
}
