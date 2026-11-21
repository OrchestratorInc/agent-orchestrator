import { CloudCpError, type CloudCpChatModelsResponse } from "./cloud-cp";

/**
 * Cloud's model list for an agent: the models a Cloud worker running the
 * pinned CLI listed for the signed-in account (source "worker", verified), or,
 * before any worker has, the ones that CLI release supports for every account
 * (source "release"). The control plane learns the account's list from every
 * session's worker on its own, for every agent with a lister, so "release" is
 * only the brief state before the account's first one.
 */
export type CloudModelCatalog = CloudCpChatModelsResponse;

/** Why the control plane gave a cloud task no model list. */
export type CloudModelCatalogGap = "legacy" | "unavailable" | "failed";

/** Why the control plane gave a cloud task no model catalog. */
export function cloudModelCatalogGap(error: unknown): CloudModelCatalogGap {
	if (!(error instanceof CloudCpError)) return "failed";
	// An older control plane has no such route and answers 404 or 501 without an
	// error envelope. A handler's own 404 carries a code and is a real failure.
	if (!error.code && (error.status === 404 || error.status === 501)) return "legacy";
	if (error.status === 409 && error.code === "MODEL_CATALOG_UNAVAILABLE") return "unavailable";
	return "failed";
}

/**
 * Whether a cloud task lists this computer's models in place of the control
 * plane's: an older control plane's suggestions, or opencode's own discovery,
 * scoped to the connected provider's credential type.
 */
export function listsLocalModelsForCloud(gap: CloudModelCatalogGap | undefined, agent: string, modelScope: string): boolean {
	return gap === "legacy" || (gap === "unavailable" && agent === "opencode" && modelScope !== "");
}

/**
 * Whether a model list offers the model id, for any agent: the control plane's
 * modelcatalog.Offers, so the desktop and the server agree. It errs towards
 * yes, because a model it wrongly says the list lacks is one a task stops
 * using: the same ID in any case; a bracketed variant of a listed model
 * ("sonnet[1m]" of "sonnet"); a listed dated release named without its date
 * ("claude-opus-4-5" of "claude-opus-4-5-20251101") and a dated release of a
 * listed undated ID; a Claude model the list offers through an alias
 * (claudeOffersThroughAlias); and "opusplan" when the list offers opus. An
 * empty id is the agent's own default, which every list offers.
 */
export function cloudCatalogOffers(models: ReadonlyArray<CatalogEntry>, id: string): boolean {
	const wanted = id.trim();
	if (!wanted) return true;
	const open = wanted.indexOf("[");
	const base = open > 0 && wanted.endsWith("]") ? wanted.slice(0, open) : wanted;
	const datedReleaseOf = (listed: string, undated: string) =>
		listed.length === undated.length + 9 && sameID(listed.slice(0, undated.length), undated) && /^-\d{8}$/.test(listed.slice(undated.length));
	const listedByID = models.some((model) => {
		const listed = model.id.trim();
		if (!listed) return false;
		return sameID(listed, wanted) || sameID(listed, base) || datedReleaseOf(listed, base) || datedReleaseOf(base, listed);
	});
	if (listedByID || claudeOffersThroughAlias(models, base)) return true;
	return sameID(base, "opusplan") && cloudCatalogOffers(models, "opus");
}

type CatalogEntry = { id: string; description?: string; providerId?: string };

const sameID = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();

/**
 * Whether a list offers a full Claude ID (base, without a bracketed suffix)
 * that it does not name, through an alias: the control plane's
 * claudeOffersThroughAlias. On a list whose entries carry the provider's model
 * (any entry has providerId), only a model the provider confirmed counts: the
 * request without its release date is some entry's providerId, or that ID
 * without its date. Claude Code describes an alias with the model it would run
 * whether or not the account can. A list from before entries carried it is
 * matched by the descriptions ("Opus 5.5 · ...").
 */
function claudeOffersThroughAlias(models: ReadonlyArray<CatalogEntry>, base: string): boolean {
	const name = claudeModelName(base);
	if (!name) return false;
	if (models.some((model) => model.providerId?.trim())) {
		const wanted = withoutReleaseDate(base);
		return models.some((model) => {
			const provided = model.providerId?.trim();
			return Boolean(provided) && (sameID(provided!, wanted) || sameID(withoutReleaseDate(provided!), wanted));
		});
	}
	return models.some((model) => {
		const described = describedModel(model.description);
		return described !== "" && (sameID(described, name) || sameID(described, `Claude ${name}`));
	});
}

/** id without a trailing release date ("-" and eight digits). */
function withoutReleaseDate(id: string): string {
	return /^.+-\d{8}$/.test(id) ? id.slice(0, -9) : id;
}

/**
 * The name Claude Code describes a full Claude model ID by: "claude-opus-5-5"
 * and "claude-opus-5-5-20260101" are "Opus 5.5", "claude-3-7-sonnet-20250219"
 * is "Sonnet 3.7". It needs exactly one family word and at least one version
 * number, so anything else has no name.
 */
export function claudeModelName(id: string): string | undefined {
	const lower = id.toLowerCase();
	if (!lower.startsWith("claude-")) return undefined;
	let parts = lower.slice("claude-".length).split("-");
	if (parts.length > 1 && /^\d{8}$/.test(parts[parts.length - 1])) parts = parts.slice(0, -1);
	let family = "";
	const version: string[] = [];
	for (const part of parts) {
		if (/^\d+$/.test(part)) version.push(part);
		else if (!family && /^\p{L}+$/u.test(part)) family = part;
		else return undefined;
	}
	if (!family || version.length === 0) return undefined;
	return `${family[0].toUpperCase()}${family.slice(1)} ${version.join(".")}`;
}

/** The model a Claude Code entry says it runs: its description before "·". */
function describedModel(description: string | undefined): string {
	const at = description?.indexOf("·") ?? -1;
	return at < 0 || !description ? "" : description.slice(0, at).trim();
}

/**
 * What Cloud's list says about running a model:
 * - "offered": the list has it (or no model is named: the agent's default);
 * - "unavailable": the list is the account's own (verified) and lacks it, so
 *   a task on it fails;
 * - "unverified": only the release list is known yet, which lacks it; the
 *   account may still run it, and the control plane is learning the account's
 *   list.
 */
export type CloudModelVerdict = "offered" | "unavailable" | "unverified";

export function cloudModelVerdict(catalog: Pick<CloudModelCatalog, "models" | "source"> | undefined, model: string): CloudModelVerdict {
	// No model is the agent's default. The literal "default" is a model ID like
	// any other, as the control plane's Offers has it: Claude Code lists it,
	// Codex does not, and Cloud refuses it there.
	if (!catalog || cloudCatalogOffers(catalog.models, model)) return "offered";
	return catalog.source === "worker" ? "unavailable" : "unverified";
}
