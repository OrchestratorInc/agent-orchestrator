import { isConcreteModelID } from "./agent-model-choices";

type CatalogModel = { id: string; efforts?: string[]; requiresVersion?: string };
type Catalog = {
	models?: CatalogModel[];
	stale?: boolean;
	refreshState?: string;
	customModelEntry?: string;
};

/**
 * What a saved model and effort no longer pass against an agent's catalog, the
 * same checks a spawn refuses with UNSUPPORTED_MODEL and UNSUPPORTED_EFFORT.
 * Only an authoritative catalog decides: one that is stale, still loading or
 * empty cannot say a model is gone (MODEL_CAPABILITIES_UNAVAILABLE), so it
 * resets nothing. An unlisted model is kept where the catalog allows typed
 * model IDs.
 */
export function staleSavedTuning(catalog: Catalog | undefined, model: string, effort: string): { model: boolean; effort: boolean } | undefined {
	if (!catalog?.models?.length || catalog.stale || (catalog.refreshState && catalog.refreshState !== "idle")) return undefined;
	if (!isConcreteModelID(model)) return undefined;
	const listed = catalog.models.find((item) => item.id === model);
	if ((!listed && catalog.customModelEntry !== "direct") || listed?.requiresVersion) {
		return { model: true, effort: effort !== "" };
	}
	if (listed && effort && listed.efforts?.length && !listed.efforts.includes(effort)) {
		return { model: false, effort: true };
	}
	return undefined;
}
