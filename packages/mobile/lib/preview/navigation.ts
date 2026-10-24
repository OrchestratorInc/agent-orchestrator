import type { SourceRef } from "../environment/scopedBoard";
import { resolvePreviewRoute, type PreviewRouteParams } from "./route";

export type PreviewRouteOwner = { source: SourceRef; scopeKey?: string };

export function previewRoute(id: string, hostId?: string, title?: string, owner?: PreviewRouteOwner) {
	const source = owner?.source ?? (hostId ? { kind: "local" as const, id: hostId } : undefined);
	return { pathname: "/preview/[id]" as const, params: { id,
		...(source ? { source: source.kind, sourceId: source.id } : {}),
		...(source?.kind === "local" ? { hostId: source.id } : {}),
		...(source?.kind === "cloud" ? { scopeKey: owner?.scopeKey } : {}),
		...(title ? { title } : {}),
	} };
}

export function legacyPreviewRoute(params: PreviewRouteParams) {
	const route = resolvePreviewRoute(params);
	return route.kind === "found" ? previewRoute(route.id, undefined, typeof params.title === "string" ? params.title : undefined, { source: route.source, scopeKey: route.scopeKey }) : null;
}
