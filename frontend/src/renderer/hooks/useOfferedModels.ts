import { useMemo } from "react";
import { filterEnabledModels } from "../lib/agent-model-choices";
import { useSettings } from "./useSettings";

const NO_MODELS: never[] = [];

/**
 * The models a picker offers for one harness: its catalog narrowed to the ones
 * switched on in Settings → Harness → Models on that host. Ids in `keep` (a
 * model already chosen or saved) stay listed. `skip` offers the catalog as is,
 * for Cloud, whose VMs keep their own catalog.
 */
export function useOfferedModels<T extends { id: string }>(
	agentId: string | undefined,
	models: T[] | undefined,
	{ hostId, keep = [], skip = false }: { hostId?: string; keep?: ReadonlyArray<string | undefined>; skip?: boolean } = {},
): { models: T[]; narrowed: boolean } {
	const active = !skip && Boolean(agentId);
	const { settings } = useSettings(hostId, active);
	const enabled = active && agentId ? settings?.harnessDefaults?.[agentId]?.enabledModels : undefined;
	const keepKey = keep.filter((id): id is string => !!id).join("\n");
	return useMemo(
		() => filterEnabledModels(models ?? NO_MODELS, enabled, keepKey ? keepKey.split("\n") : []),
		[enabled, keepKey, models],
	);
}
