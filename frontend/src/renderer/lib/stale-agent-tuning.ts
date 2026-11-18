import { isConcreteModelID, newestClaudeFamilyModel } from "./agent-model-choices";

type CatalogModel = { id: string; label?: string; efforts?: string[]; requiresVersion?: string; isDefault?: boolean };
type Catalog = {
	models?: CatalogModel[];
	stale?: boolean;
	refreshState?: string;
	customModelEntry?: string;
	source?: string;
};

/**
 * What a saved model and effort no longer pass against an agent's catalog, the
 * same checks a spawn refuses with UNSUPPORTED_MODEL and UNSUPPORTED_EFFORT.
 * Only an authoritative catalog decides: one that is stale, still loading or
 * empty cannot say a model is gone (MODEL_CAPABILITIES_UNAVAILABLE), so it
 * resets nothing. An unlisted model is kept where the catalog allows typed
 * model IDs.
 */
export function staleSavedTuning(catalog: Catalog | undefined, model: string, effort: string, agent: string): { model: boolean; effort: boolean } | undefined {
	if (!catalog?.models?.length || catalog.stale || (catalog.refreshState && catalog.refreshState !== "idle")) return undefined;
	const concrete = isConcreteModelID(model);
	const listed = concrete ? catalog.models.find((item) => item.id.toLowerCase() === model.toLowerCase()) : catalog.models.find((item) => item.isDefault);
	// An upgrade requirement is actionable in the picker, not stale settings.
	if (listed?.requiresVersion) return undefined;
	if (concrete && !listed && catalog.customModelEntry === "none" && (agent === "memcode" || agent === "muse" || agent === "amp") && catalog.source?.startsWith("official")) {
		return { model: true, effort: effort !== "" };
	}
	const effortModel = listed && agent === "claude-code" && !listed.efforts?.length
		? newestClaudeFamilyModel(catalog.models, listed.id) ?? listed
		: listed;
	// Claude omits empty effort lists from the wire; for a listed concrete
	// model that still means no efforts. Other providers may omit unknown data.
	const knownEfforts = (agent === "codex" && effortModel?.efforts !== undefined) || agent === "claude-code";
	if (effortModel && effort && knownEfforts && !effortModel.efforts?.includes(effort)) {
		return { model: false, effort: true };
	}
	return undefined;
}
