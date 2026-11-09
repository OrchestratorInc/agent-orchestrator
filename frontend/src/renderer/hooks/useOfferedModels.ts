import { useMemo } from "react";
import { filterDisabledModels, offeredCatalogModels } from "../lib/agent-model-choices";
import { useSettings } from "./useSettings";

const NO_MODELS: never[] = [];

/**
 * 先按目标主机的 runnableModels 缩小目录，再应用 disabledModels；keep 中的已选或已保存模型保留。
 * modelID 将别名映射到设置中的目录 ID。skip 用于有独立目录的云端，跳过本机限制。
 */
export function useOfferedModels<T extends { id: string }>(
	agentId: string | undefined,
	models: T[] | undefined,
	{ hostId, keep = [], skip = false, modelID, runnableModels }: {
		hostId?: string;
		keep?: ReadonlyArray<string | undefined>;
		skip?: boolean;
		modelID?: (model: T) => string;
		runnableModels?: string[];
	} = {},
): { models: T[]; narrowed: boolean } {
	const active = !skip && Boolean(agentId);
	const { settings } = useSettings(hostId, active);
	const disabled = active && agentId ? settings?.harnessDefaults?.[agentId]?.disabledModels : undefined;
	const keepKey = keep.filter((id): id is string => !!id).join("\n");
	return useMemo(
		() => {
			const catalog = models ?? NO_MODELS;
			const kept = keepKey ? keepKey.split("\n") : [];
			const available = skip ? catalog : offeredCatalogModels({ models: catalog, runnableModels }, undefined, kept);
			const offered = filterDisabledModels(available, disabled, kept, modelID);
			return { models: offered.models, narrowed: offered.models.length < catalog.length };
		},
		[disabled, keepKey, modelID, models, runnableModels, skip],
	);
}
