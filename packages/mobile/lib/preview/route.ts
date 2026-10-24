import type { SourceRef } from "../environment/scopedBoard";

export type PreviewRouteParams = {
	id?: string | string[]; hostId?: string | string[]; source?: string | string[];
	sourceId?: string | string[]; scopeKey?: string | string[]; title?: string | string[];
};
export type PreviewRouteResolution = { kind: "found"; id: string; source: SourceRef; scopeKey?: string } | { kind: "invalid" };

/** An unqualified preview must never borrow credentials from the selected source. */
export function resolvePreviewRoute(params: PreviewRouteParams): PreviewRouteResolution {
	const valid = (value: unknown): value is string => typeof value === "string" && value.length <= 2048 && value.trim().length > 0 && !/[\u0000-\u001f\u007f]/.test(value);
	if (!valid(params.id)) return { kind: "invalid" };
	if (params.title !== undefined && typeof params.title !== "string") return { kind: "invalid" };
	let source: SourceRef;
	if (params.source !== undefined || params.sourceId !== undefined) {
		if ((params.source !== "cloud" && params.source !== "local") || !valid(params.sourceId)) return { kind: "invalid" };
		source = { kind: params.source, id: params.sourceId };
	} else if (valid(params.hostId)) source = { kind: "local", id: params.hostId };
	else return { kind: "invalid" };
	if (params.hostId !== undefined && (!valid(params.hostId) || source.kind !== "local" || source.id !== params.hostId)) return { kind: "invalid" };
	if (params.scopeKey !== undefined && !valid(params.scopeKey)) return { kind: "invalid" };
	if (source.kind === "cloud") return valid(params.scopeKey) ? { kind: "found", id: params.id, source, scopeKey: params.scopeKey } : { kind: "invalid" };
	return { kind: "found", id: params.id, source };
}
