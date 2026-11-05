import { useMemo } from "react";
import { filterDisabledModels } from "../lib/agent-model-choices";
import { useSettings } from "./useSettings";

const NO_MODELS: never[] = [];

/**
 * The models a picker offers for one harness: its catalog without the ones
 * switched off in Settings → Harness → Models on that host. Ids in `keep` (a
 * model already chosen or saved) stay listed. `modelID` maps an entry to its
 * catalog id (keep it stable across renders). `skip` offers the catalog as is,
 * for Cloud, whose VMs keep their own catalog.
 */
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
