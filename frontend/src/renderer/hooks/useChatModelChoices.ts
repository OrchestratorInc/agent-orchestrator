import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { claudeChoiceModel } from "../lib/agent-model-choices";
import type { ChatConfigOption, ChatModel } from "../types/conversation";
import { agentModelsQueryOptions } from "./useAgentModelsQuery";
import { useOfferedModels } from "./useOfferedModels";

/** The models and provider options a chat composer offers, for local and cloud chats alike. */
export function useChatModelChoices(
	harness: string | undefined,
	controllerModels: ChatModel[],
	options: ChatConfigOption[] | undefined,
	currentModel: string | undefined,
	hostId?: string,
) {
	// Claude's live list is family aliases; the new-task picker's catalog carries the versions.
	const isClaude = harness === "claude-code";
	const claudeCatalog = useQuery({ ...agentModelsQueryOptions("claude-code", "", hostId), enabled: isClaude }).data;
	const catalogModels = useMemo(() => {
		if (!isClaude || !claudeCatalog?.models.length) return controllerModels;
		return claudeCatalog.models
			.filter((model) => model.id.toLowerCase() !== "default")
			.map((model) => ({
				id: model.id,
				displayName: model.label || model.id,
				default: Boolean(model.isDefault),
			}));
	}, [isClaude, claudeCatalog, controllerModels]);
	// A provider's own model option (ACP config) replaces the native list above,
	// so the same setting narrows its choices; other options pass through as is.
	const providerModelOption = options?.find(
		(option) => option.type === "select" && (option.category === "model" || option.id === "model"),
	);
	const providerModelChoices = useMemo(
		() => providerModelOption?.choices.map((choice) => ({ id: choice.value })),
		[providerModelOption],
	);
	// Resolve current Claude aliases before filtering so their labels cannot move
	// to an older enabled model when the current concrete entry is switched off.
	const resolveClaudeChoice = useMemo(
		() => isClaude && claudeCatalog ? claudeChoiceModel(claudeCatalog.models) : undefined,
		[isClaude, claudeCatalog],
	);
	const currentModels = [currentModel, providerModelOption?.currentValue];
	const { models } = useOfferedModels(harness, catalogModels, {
		hostId,
		keep: [...currentModels, ...currentModels.map((id) => id ? resolveClaudeChoice?.(id)?.id : undefined)],
	});
	// Claude's choices are family aliases ("sonnet"); Settings names catalog ids.
	const claudeChoiceID = useMemo(
		() => resolveClaudeChoice && ((choice: { id: string }) => resolveClaudeChoice(choice.id)?.id ?? choice.id),
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
