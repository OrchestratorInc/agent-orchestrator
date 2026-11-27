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
// Reconcile before publishing the controller or dispatching its opening prompt.
func (s *Service) reconcileAgyOpeningModel(ctx context.Context, controller *Controller) error {
	previous := controller.Settings()
	configurer, ok := controller.conv.(ports.ChatConfigOptionController)
	if previous.Model == "" || !ok {
		return nil
	}
	options, err := configurer.ListConfigOptions(ctx)
	if err != nil {
		return fmt.Errorf("read Agy opening model: %w", err)
	}
	for _, option := range options {
		if option.ID != "model" && option.Category != "model" {
			continue
		}
		live := option.Current.Select
		if live == "" || live == previous.Model || slices.ContainsFunc(option.Choices, func(choice ports.ChatConfigOptionChoice) bool {
			return choice.Value == previous.Model
		}) {
			return nil
		}
		// A supported composite ID still names the selected base and effort.
		// Keeping it ensures that a restart reapplies the chosen effort.
		if resolver, ok := controller.conv.(ports.ChatModelVariantResolver); ok {
			if base, split := resolver.ModelVariantBase(previous.Model); split && base == live {
				return nil
			}
		}
		next, _ := settingsFromConfigOptions(previous, options)
		next.Model = live
		if err := s.store.UpsertActivity(ctx, controller.conversation.ID, "", domain.ConversationActivity{
			ID: s.newID(), Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
			Summary:        fmt.Sprintf("%s isn't available in Agy Chat. Using %s instead.", previous.Model, live),
			ProviderItemID: "agy-model-fallback:" + previous.Model + ":" + live,
		}, s.now()); err != nil {
			return fmt.Errorf("record Agy model fallback notice: %w", err)
		}
		if err := controller.SetSettings(ctx, next); err != nil {
			return err
		}
		controller.conversation.Settings = next
		return nil
	}
	return nil
}
