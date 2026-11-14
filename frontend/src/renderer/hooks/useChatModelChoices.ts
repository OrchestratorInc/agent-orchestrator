import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { claudeChoiceModel } from "../lib/agent-model-choices";
import type { ChatConfigChoice, ChatConfigOption, ChatModel } from "../types/conversation";
import { agentModelsQueryOptions } from "./useAgentModelsQuery";
import { useOfferedModels } from "./useOfferedModels";

export function useChatModelChoices(
	harness: string | undefined,
	controllerModels: ChatModel[],
	options: ChatConfigOption[] | undefined,
	currentModel: string | undefined,
	hostId?: string,
	liveModelsLoaded = true,
	runsOnDevice = true,
) {
	const isClaude = harness === "claude-code";
	const providerModelOption = options?.find(
		(option) => option.type === "select" && (option.category === "model" || option.id === "model"),
	);
	const ownList = !runsOnDevice && controllerModels.length > 0;
	const showSavedCatalog = (isClaude && !ownList) || (!liveModelsLoaded && !providerModelOption);
	const savedCatalog = useQuery({ ...agentModelsQueryOptions(harness ?? "", "", hostId), enabled: Boolean(harness) && showSavedCatalog }).data;
	const catalogModels = useMemo(() => {
		const savedModels = savedCatalog?.models ?? [];
		if (!showSavedCatalog || savedModels.length === 0) return controllerModels;
		return savedModels
			.filter((model) => typeof model?.id === "string" && model.id.toLowerCase() !== "default")
			.map((model) => ({
				id: model.id,
				displayName: model.label || model.id,
				default: Boolean(model.isDefault),
				...(runsOnDevice && model.unavailableReason ? { unavailableReason: model.unavailableReason } : {}),
			}));
	}, [showSavedCatalog, savedCatalog, controllerModels, runsOnDevice]);
	const unavailableReasons = useMemo(() => {
		if (!isClaude || !runsOnDevice) return undefined;
		const reasons = new Map<string, string>();
		for (const model of savedCatalog?.models ?? []) {
			if (model.unavailableReason) reasons.set(model.id, model.unavailableReason);
		}
		return reasons.size > 0 ? reasons : undefined;
	}, [isClaude, runsOnDevice, savedCatalog]);
	const providerModelChoices = useMemo(
		() => providerModelOption?.choices.map((choice) => ({ id: choice.value, name: choice.name })),
		[providerModelOption],
	);
	const resolveClaudeChoice = useMemo(
		() => isClaude && runsOnDevice && savedCatalog?.models ? claudeChoiceModel(savedCatalog.models) : undefined,
		[isClaude, runsOnDevice, savedCatalog],
	);
	const currentModels = [currentModel, providerModelOption?.currentValue];
	const { models } = useOfferedModels(harness, catalogModels, {
		hostId,
		keep: [...currentModels, ...currentModels.map((id) => id ? resolveClaudeChoice?.(id)?.id : undefined)],
	});
	const claudeChoiceID = useMemo(
		() => resolveClaudeChoice && ((choice: { id: string; name?: string }) => resolveClaudeChoice(choice.id, choice.name)?.id ?? choice.id),
		[resolveClaudeChoice],
	);
	const { models: offeredProviderModels } = useOfferedModels(harness, providerModelChoices, { hostId, keep: [providerModelOption?.currentValue], modelID: claudeChoiceID });
	const configOptions = useMemo(() => {
		if (!providerModelOption) return options;
		const offered = new Set(offeredProviderModels.map((choice) => choice.id));
		const markUnavailable = (choice: ChatConfigChoice): ChatConfigChoice => {
			const reason = unavailableReasons?.get(choice.value) ?? unavailableReasons?.get(choice.value.replace(/\[.*\]$/, ""));
			return reason ? { ...choice, unavailableReason: reason } : choice;
		};
		return options?.map((option) => option === providerModelOption
			? { ...option, choices: option.choices.filter((choice) => offered.has(choice.value)).map(markUnavailable) }
			: option);
	}, [options, offeredProviderModels, providerModelOption, unavailableReasons]);
	return { models, configOptions };
}
