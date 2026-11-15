import { useMemo } from "react";
import { filterDisabledModels } from "../lib/agent-model-choices";
import { useSettings } from "./useSettings";

const NO_MODELS: never[] = [];

export function useOfferedModels<T extends { id: string }>(
	agentId: string | undefined,
	models: T[] | undefined,
	{ hostId, keep = [], skip = false, modelID }: { hostId?: string; keep?: ReadonlyArray<string | undefined>; skip?: boolean; modelID?: (model: T) => string } = {},
): { models: T[]; narrowed: boolean } {
	const active = !skip && Boolean(agentId);
	const { settings } = useSettings(hostId, active);
	const disabled = active && agentId ? settings?.harnessDefaults?.[agentId]?.disabledModels : undefined;
	const keepKey = keep.filter((id): id is string => !!id).join("\n");
	return useMemo(
		() => filterDisabledModels(models ?? NO_MODELS, disabled, keepKey ? keepKey.split("\n") : [], modelID),
		[disabled, keepKey, modelID, models],
	);
}
