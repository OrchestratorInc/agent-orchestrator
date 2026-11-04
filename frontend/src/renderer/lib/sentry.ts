// Renderer Sentry intake is deliberately transport-free. Trusted preload adds
// the latest main-owned generation before forwarding to Electron main.
import { classifyError, type ClassifyInput, type Triage } from "../../shared/observability";
import { SAFE_APP_ASSET, SAFE_FUNCTION, safeSentryExceptionType, safeSentryFunctionName } from "../../shared/sentry-config";
import { aoBridge } from "./bridge";

export type ObservabilityContext = { release?: string; channel?: string; platform?: string; distinctId?: string };
export type CaptureMeta = ClassifyInput & { operation?: string; surface?: string; domain?: string; requestId?: string };
let context: ObservabilityContext = {};
export async function initSentry(next: ObservabilityContext): Promise<void> { context = next; }
function domainOf(operation?: string): string | undefined {
	if (!operation) return undefined;
	const parts = (operation.split(" ").pop() ?? operation).split("/").filter(Boolean);
	const index = parts.indexOf("v1"); return index >= 0 ? parts[index + 1] : parts[0];
}
function tagsFor(meta: CaptureMeta, triage: Triage): Record<string, string> {
	return Object.fromEntries(Object.entries({ platform: context.platform ?? "desktop", surface: meta.surface, domain: meta.domain, operation: meta.operation, category: meta.category, code: meta.code, http_status: meta.httpStatus?.toString(), apierr_kind: meta.kind, severity: triage.severity, owner: triage.owner }).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
}

function safeRendererFrames(error: unknown): Array<{ function: string; filename?: string; lineno?: number; colno?: number }> {
	if (!(error instanceof Error) || !error.stack) return [];
	const frames: Array<{ function: string; filename?: string; lineno?: number; colno?: number }> = [];
	for (const line of error.stack.split("\n")) {
		const match = /^\s*at\s+([^\s()]+)\s+\(([^)]+)\)\s*$/.exec(line);
		if (!match || !SAFE_FUNCTION.test(match[1]) || match[1].length > 128) continue;
		const location = match[2];
		if (!location.startsWith("app://renderer/") && !/^https?:\/\/(?:localhost|127\.0\.0\.1)(?::\d+)?\//.test(location)) continue;
		const source = location.replace(/:\d+:\d+$/, "");
		const appPath = source.startsWith("app://renderer/") ? source.slice("app://renderer/".length) : undefined;
		const filename = appPath && SAFE_APP_ASSET.test(appPath) ? appPath : undefined;
		const position = /:(\d+):(\d+)$/.exec(location);
		const lineno = position ? Number(position[1]) : undefined;
		const colno = position ? Number(position[2]) : undefined;
		frames.push({ function: safeSentryFunctionName(match[1], "renderer_frame"), ...(filename ? { filename } : {}), ...(lineno && Number.isSafeInteger(lineno) && lineno <= 1_000_000 ? { lineno } : {}), ...(colno && Number.isSafeInteger(colno) && colno <= 10_000_000 ? { colno } : {}) });
		if (frames.length === 8) break;
	}
	return frames.reverse();
}

export function captureExceptionToSentry(error: unknown, meta: CaptureMeta = {}): void {
	const triage = classifyError(meta);
	void aoBridge.telemetry?.capture({
		kind: triage.report ? "exception" : "breadcrumb",
		message: String((error as Error)?.message ?? error),
		level: triage.level,
		tags: tagsFor(meta, triage),
		...(triage.report ? { exception: { type: safeSentryExceptionType((error as Error)?.name), frames: safeRendererFrames(error) } } : {}),
	});
}
/** A daemon API failure; its origin tag says so, apart from AO Cloud's. */
export function captureApiErrorToSentry(operation: string, category: string, status?: number, code?: string, requestId?: string): void {
	const meta: CaptureMeta = { operation, category, httpStatus: status, code, requestId, domain: domainOf(operation) };
	const triage = classifyError(meta);
	void aoBridge.telemetry?.capture({ kind: triage.report ? "message" : "breadcrumb", message: `${operation} ${category}`, level: triage.level, tags: { ...tagsFor(meta, triage), origin: "daemon" } });
}

export type CloudFailureCategory = "http_5xx" | "network_error" | "malformed_response";

/**
 * An unexpected AO Cloud failure (cloud-error-report.ts decides which are).
 * Tagged with where it came from, its status, the control plane's code and the
 * request id its logs carry; never its message, path or body.
 */
export function captureCloudErrorToSentry(
	category: CloudFailureCategory,
	failure: { origin: "cloud" | "network"; status: number; code?: string; requestId?: string },
): void {
	const triage = classifyError({ category, httpStatus: failure.status || undefined, code: failure.code });
	// The shared triage keeps network errors as breadcrumbs; reaching here means
	// AO Cloud was unreachable while this computer was online, which is worth one.
	const report = category === "http_5xx" ? triage.report : true;
	const level = category === "network_error" ? "warning" : category === "malformed_response" ? "error" : triage.level;
	void aoBridge.telemetry?.capture({
		kind: report ? "message" : "breadcrumb",
		message: `AO Cloud ${category}`,
		level,
		tags: {
			platform: context.platform ?? "desktop",
			surface: "cloud",
			category,
			...(failure.code ? { code: failure.code } : {}),
			http_status: String(failure.status),
			origin: failure.origin,
			...(failure.requestId ? { request_id: failure.requestId } : {}),
			severity: triage.severity,
			owner: triage.owner,
		},
	});
}
