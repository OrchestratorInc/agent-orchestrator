import type { SourceRef } from "../environment/scopedBoard";
import { routeSource } from "../session/sessionRoute";

export type FilesRouteParams = {
	sessionId?: string | string[];
	hostId?: string | string[];
	source?: string | string[];
	sourceId?: string | string[];
	scopeKey?: string | string[];
	repository?: string | string[];
};
export type FilesRouteResolution = { kind: "found"; sessionId: string; source: SourceRef; scopeKey?: string; repository?: string } | { kind: "invalid" };

/** Files never borrow the active machine or account for an unqualified link. */
export function resolveFilesRoute(params: FilesRouteParams): FilesRouteResolution {
	const valid = (value: string | string[] | undefined): value is string => typeof value === "string" && Boolean(value.trim());
	if (!valid(params.sessionId)) return { kind: "invalid" };
	const explicit = routeSource(params);
	if (explicit?.kind === "invalid") return explicit;
	if (params.hostId !== undefined && (!valid(params.hostId) || (explicit && (explicit.kind !== "local" || explicit.id !== params.hostId)))) return { kind: "invalid" };
	const source = explicit ?? (valid(params.hostId) ? { kind: "local" as const, id: params.hostId } : null);
	if (!source) return { kind: "invalid" };
	if (params.repository !== undefined && typeof params.repository !== "string") return { kind: "invalid" };
	if (params.repository && (params.repository === "." || params.repository === ".." || /[/\\\0]/.test(params.repository) || !params.repository.trim())) return { kind: "invalid" };
	if (source.kind === "cloud") {
		if (!valid(params.scopeKey)) return { kind: "invalid" };
		return { kind: "found", sessionId: params.sessionId, source, scopeKey: params.scopeKey, ...(params.repository ? { repository: params.repository } : {}) };
	}
	if (params.repository) return { kind: "invalid" };
	return { kind: "found", sessionId: params.sessionId, source };
}
