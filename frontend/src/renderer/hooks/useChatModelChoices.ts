import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { claudeChoiceModel } from "../lib/agent-model-choices";
import type { ChatConfigOption, ChatModel } from "../types/conversation";
import { agentModelsQueryOptions } from "./useAgentModelsQuery";
import { useOfferedModels } from "./useOfferedModels";

/**
 * The models and provider options a chat composer offers, for local and cloud chats alike.
 * `liveModelsLoaded` reports whether `controllerModels` is the provider's answer yet;
 * until it is, the agent's saved device catalog is shown so the picker never waits.
 */
export function useChatModelChoices(
	harness: string | undefined,
	controllerModels: ChatModel[],
	options: ChatConfigOption[] | undefined,
	currentModel: string | undefined,
	hostId?: string,
	liveModelsLoaded = true,
) {
	// Claude's live list is family aliases; the new-task picker's catalog carries the versions.
	const isClaude = harness === "claude-code";
	// A provider's own model option (ACP config) replaces the native list below,
	// so the same setting narrows its choices; other options pass through as is.
	const providerModelOption = options?.find(
		(option) => option.type === "select" && (option.category === "model" || option.id === "model"),
	);
	// Other agents show their saved catalog until the live list arrives, then the
	// live list, which reflects this account's entitlements, replaces it.
	const showSavedCatalog = isClaude || (!liveModelsLoaded && !providerModelOption);
	const savedCatalog = useQuery({ ...agentModelsQueryOptions(harness ?? "", "", hostId), enabled: Boolean(harness) && showSavedCatalog }).data;
	const catalogModels = useMemo(() => {
		// A catalog response may omit models (an older daemon or a mock); treat that
		// like an empty catalog rather than reading through it.
		const savedModels = savedCatalog?.models ?? [];
		if (!showSavedCatalog || savedModels.length === 0) return controllerModels;
		return savedModels
			.filter((model) => typeof model?.id === "string" && model.id.toLowerCase() !== "default")
			.map((model) => ({
				id: model.id,
				displayName: model.label || model.id,
				default: Boolean(model.isDefault),
			}));
	}, [showSavedCatalog, savedCatalog, controllerModels]);
	const providerModelChoices = useMemo(
		() => providerModelOption?.choices.map((choice) => ({ id: choice.value, name: choice.name })),
		[providerModelOption],
	);
	// Resolve current Claude aliases before filtering so their labels cannot move
	// to an older enabled model when the current concrete entry is switched off.
	const resolveClaudeChoice = useMemo(
		() => isClaude && savedCatalog?.models ? claudeChoiceModel(savedCatalog.models) : undefined,
		[isClaude, savedCatalog],
	);
	const currentModels = [currentModel, providerModelOption?.currentValue];
	const { models } = useOfferedModels(harness, catalogModels, {
		hostId,
		keep: [...currentModels, ...currentModels.map((id) => id ? resolveClaudeChoice?.(id)?.id : undefined)],
	});
	// Claude's choices are family aliases ("sonnet"); Settings names catalog ids.
	const claudeChoiceID = useMemo(
		() => resolveClaudeChoice && ((choice: { id: string; name?: string }) => resolveClaudeChoice(choice.id, choice.name)?.id ?? choice.id),
		[resolveClaudeChoice],
	);
	const { models: offeredProviderModels } = useOfferedModels(harness, providerModelChoices, { hostId, keep: [providerModelOption?.currentValue], modelID: claudeChoiceID });
	const configOptions = useMemo(() => {
		if (!providerModelOption) return options;
		const offered = new Set(offeredProviderModels.map((choice) => choice.id));
		return options?.map((option) => option === providerModelOption
			? { ...option, choices: option.choices.filter((choice) => offered.has(choice.value)) }
			: option);
	}, [options, offeredProviderModels, providerModelOption]);
	return { models, configOptions };
}
