import { CloudCpError, type CloudCpChatModelsResponse } from "./cloud-cp";

export type CloudModelCatalog = CloudCpChatModelsResponse;

export type CloudModelCatalogGap = "legacy" | "unavailable" | "failed";

export function cloudModelCatalogGap(error: unknown): CloudModelCatalogGap {
	if (!(error instanceof CloudCpError)) return "failed";
	if (!error.code && (error.status === 404 || error.status === 501)) return "legacy";
	if (error.status === 409 && error.code === "MODEL_CATALOG_UNAVAILABLE") return "unavailable";
	return "failed";
}

export function listsLocalModelsForCloud(gap: CloudModelCatalogGap | undefined, agent: string, modelScope: string): boolean {
	return gap === "legacy" || (gap === "unavailable" && agent === "opencode" && modelScope !== "");
}

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

function withoutReleaseDate(id: string): string {
	return /^.+-\d{8}$/.test(id) ? id.slice(0, -9) : id;
}

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

function describedModel(description: string | undefined): string {
	const at = description?.indexOf("·") ?? -1;
	return at < 0 || !description ? "" : description.slice(0, at).trim();
}

export type CloudModelVerdict = "offered" | "unavailable" | "unverified";

export function cloudModelVerdict(catalog: Pick<CloudModelCatalog, "models" | "source"> | undefined, model: string): CloudModelVerdict {
	if (!catalog || cloudCatalogOffers(catalog.models, model)) return "offered";
	return catalog.source === "worker" ? "unavailable" : "unverified";
}
