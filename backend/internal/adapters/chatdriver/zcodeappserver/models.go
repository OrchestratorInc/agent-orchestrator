package zcodeappserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// modelIDSeparator joins providerId and modelId into AO's single model id.
// Provider ids are opaque and may contain ':' (account:zai-individual-coding-plan),
// so the join uses '/' and the split takes the LAST one.
const modelIDSeparator = "/"

// modelID is the AO id for a provider model selection.
func modelID(selection zcodeproto.ModelSelection) string {
	return selection.ProviderID + modelIDSeparator + selection.ModelID
}

// parseModelSelection splits an AO model id back into the provider pair.
func parseModelSelection(id string) (zcodeproto.ModelSelection, bool) {
	cut := strings.LastIndex(id, modelIDSeparator)
	if cut <= 0 || cut == len(id)-1 {
		return zcodeproto.ModelSelection{}, false
	}
	return zcodeproto.ModelSelection{ProviderID: id[:cut], ModelID: id[cut+1:]}, true
}

// ListModels reads the catalog off the latest session snapshot
// (settings.model.available, PROVEN p7-models.ndjson). The snapshot is the
// only honest source: zcode has no model/list endpoint, and the create/resume
// result is where the provider config's models show up. Efforts are the
// model's reasoning.levels; a selection must carry one of them.
func (c *conversation) ListModels(context.Context) ([]ports.ChatModel, error) {
	c.mu.Lock()
	settings := c.snapshot.Settings.Model
	launchEffort := c.launchEffort
	c.mu.Unlock()

	current := ""
	if settings.Current != nil {
		current = modelID(*settings.Current)
	}
	models := make([]ports.ChatModel, 0, len(settings.Available))
	for _, entry := range settings.Available {
		if entry.Ref.ProviderID == "" || entry.Ref.ModelID == "" {
			continue
		}
		id := modelID(entry.Ref)
		display := entry.Label
		if display == "" {
			display = entry.Ref.ModelID
		}
		model := ports.ChatModel{ID: id, DisplayName: display, Default: id == current}
		if entry.Reasoning != nil {
			for _, level := range entry.Reasoning.Levels {
				if level.Value != "" {
					model.Efforts = append(model.Efforts, level.Value)
				}
			}
			model.DefaultEffort = entry.Reasoning.DefaultLevel
			// The conversation's own level wins for the model it runs on, as the
			// codex driver reports its thread effort: that is what a turn with no
			// explicit effort will actually use.
			if model.Default && launchEffort != "" && hasEffort(entry, launchEffort) {
				model.DefaultEffort = launchEffort
			}
		}
		models = append(models, model)
	}
	return models, nil
}

// hasEffort reports whether a level is one the model advertises.
func hasEffort(entry zcodeproto.AvailableModel, effort string) bool {
	if entry.Reasoning == nil {
		return false
	}
	for _, level := range entry.Reasoning.Levels {
		if level.Value == effort {
			return true
		}
	}
	return false
}

// availableModel finds a selection in the cached catalog.
func (c *conversation) availableModel(selection zcodeproto.ModelSelection) (zcodeproto.AvailableModel, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.snapshot.Settings.Model.Available {
		if entry.Ref.ProviderID == selection.ProviderID && entry.Ref.ModelID == selection.ModelID {
			return entry, true
		}
	}
	return zcodeproto.AvailableModel{}, false
}

// currentModel is the session's current selection from the cached snapshot.
func (c *conversation) currentModel() (zcodeproto.ModelSelection, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot.Settings.Model.Current == nil {
		return zcodeproto.ModelSelection{}, false
	}
	return *c.snapshot.Settings.Model.Current, true
}

// modelSelectionFor builds the session/send modelSelection for a turn
// (PROVEN, p7-models.ndjson). nil means the session's own model runs with its
// own level and nothing is sent.
//
// A selection must name a reasoning level: without options.reasoningLevel the
// turn fails with invalid_model_request. The level is, in order, the per-turn
// effort, the conversation's launch effort, the level the session currently
// runs (settings.model.current.options) when the model is the current one,
// and finally the model's defaultLevel. A model or level the catalog does not
// offer is refused rather than sent, so the user sees AO's refusal instead of
// a failed turn.
func (c *conversation) modelSelectionFor(model, effort string) (*zcodeproto.ModelSelection, error) {
	c.mu.Lock()
	launchEffort := c.launchEffort
	c.mu.Unlock()
	if effort == "" {
		effort = launchEffort
	}
	if model == "" && effort == "" {
		return nil, nil
	}

	var selection zcodeproto.ModelSelection
	if model != "" {
		parsed, ok := parseModelSelection(model)
		if !ok {
			return nil, fmt.Errorf("%w: model id %q is not providerId/modelId", ports.ErrChatConfigOptionInvalid, model)
		}
		selection = parsed
	} else {
		current, ok := c.currentModel()
		if !ok {
			// An effort with no model to attach it to: nothing proven carries a
			// bare level, so the session's own level runs.
			return nil, nil
		}
		selection = zcodeproto.ModelSelection{ProviderID: current.ProviderID, ModelID: current.ModelID}
	}

	entry, known := c.availableModel(selection)
	if !known {
		c.mu.Lock()
		catalogued := len(c.snapshot.Settings.Model.Available) > 0
		c.mu.Unlock()
		if catalogued {
			return nil, fmt.Errorf("%w: model %q is not offered by this session", ports.ErrChatConfigOptionInvalid, modelID(selection))
		}
	}
	if effort != "" && known && !hasEffort(entry, effort) {
		return nil, fmt.Errorf("%w: reasoning level %q is not offered by %s", ports.ErrChatConfigOptionInvalid, effort, modelID(selection))
	}
	if effort == "" {
		if current, ok := c.currentModel(); ok && current.ProviderID == selection.ProviderID &&
			current.ModelID == selection.ModelID && current.Options != nil {
			effort = current.Options.ReasoningLevel
		}
	}
	if effort == "" && entry.Reasoning != nil {
		effort = entry.Reasoning.DefaultLevel
	}
	if effort != "" {
		selection.Options = &zcodeproto.ModelOptions{ReasoningLevel: effort}
	}
	return &selection, nil
}

// dropUnsupportedLaunchEffort clears a launch reasoning level the session's
// current model does not offer, so a resumed session runs at the model's own
// level instead of failing every turn. Start refuses such a level up front
// (createSession); a stored session is kept usable rather than stranded by a
// catalog that changed under it, with the substitution logged.
func (c *conversation) dropUnsupportedLaunchEffort() {
	c.mu.Lock()
	effort := c.launchEffort
	c.mu.Unlock()
	if effort == "" {
		return
	}
	if _, err := c.modelSelectionFor("", effort); err != nil {
		c.log.Warn("launch reasoning level is not offered by the session's model; its default level applies",
			"effort", effort, "error", err)
		c.mu.Lock()
		c.launchEffort = ""
		c.mu.Unlock()
	}
}

// SetModel changes the session's durable model through session/setModel
// (PROVEN, p7-models.ndjson). The returned snapshot replaces the cached one so
// ListModels reflects the change. Not wired to a port interface: AO chooses
// the model per turn through ChatTurnSettings.Model.
func (c *conversation) SetModel(ctx context.Context, id string) error {
	selection, err := c.modelSelectionFor(id, "")
	if err != nil {
		return err
	}
	if selection == nil {
		return fmt.Errorf("%w: model id %q is empty", ports.ErrChatConfigOptionInvalid, id)
	}
	var snapshot zcodeproto.Snapshot
	if err := c.sessionRequest(ctx, zcodeproto.MethodSessionSetModel, zcodeproto.SessionSetModelParams{
		SessionID: c.ProviderConversationID(), Model: *selection,
	}, &snapshot); err != nil {
		return fmt.Errorf("%s: %w", zcodeproto.MethodSessionSetModel, err)
	}
	c.setSnapshot(snapshot)
	return nil
}
