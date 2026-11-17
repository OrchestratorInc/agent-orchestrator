// Renderer Sentry intake is deliberately transport-free. Trusted preload adds
// the latest main-owned generation before forwarding to Electron main.
import { classifyError, type ClassifyInput, type Triage } from "../../shared/observability";
import { SAFE_APP_ASSET, SAFE_FUNCTION, safeSentryExceptionType, safeSentryFunctionName } from "../../shared/sentry-config";
import { aoBridge } from "./bridge";
import type { RendererTelemetryCaptureInput } from "../../shared/telemetry-policy";

function forwardCapture(input: RendererTelemetryCaptureInput): void {
	try { void Promise.resolve(aoBridge.telemetry?.capture(input)).catch(() => undefined); }
	catch { /* a failing reporter must not become an unhandled rejection */ }
}

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

type RendererFrame = { function: string; filename?: string; lineno?: number; colno?: number; debug_id?: string };

// The release build's Sentry bundler plugin registers each chunk's debug ID
// under a stack captured inside that chunk. Map the chunk's asset path to it so
// Sentry can apply the uploaded source map.
function debugIdsByAsset(): Map<string, string> {
	const ids = (globalThis as { _sentryDebugIds?: Record<string, string> })._sentryDebugIds;
	const byAsset = new Map<string, string>();
	for (const [stack, id] of Object.entries(ids ?? {})) {
		const asset = /app:\/\/renderer\/(assets\/[^\s:)]+)/.exec(stack)?.[1];
		if (asset && SAFE_APP_ASSET.test(asset)) byAsset.set(asset, id);
	}
	return byAsset;
}

// Named ("at fn (location)") and anonymous ("at location") frames are both
// kept when the location is an app asset. Function text crosses only when it is
// an allowlisted name; anything else becomes "renderer_frame", and Electron
// main titles the event by bundle file and line instead.
function safeRendererFrames(error: unknown): RendererFrame[] {
	if (!(error instanceof Error) || !error.stack) return [];
	const frames: RendererFrame[] = [];
	const debugIds = debugIdsByAsset();
	for (const line of error.stack.split("\n")) {
		const match = /^\s*at\s+(?:(.+?)\s+\((\S+)\)|(\S+))\s*$/.exec(line);
		if (!match) continue;
		const name = match[1]?.replace(/^(?:async|new)\s+/, "");
		const location = match[2] ?? match[3];
		if (!location.startsWith("app://renderer/") && !/^https?:\/\/(?:localhost|127\.0\.0\.1)(?::\d+)?\//.test(location)) continue;
		const source = location.replace(/:\d+:\d+$/, "");
		const appPath = source.startsWith("app://renderer/") ? source.slice("app://renderer/".length) : undefined;
		const filename = appPath && SAFE_APP_ASSET.test(appPath) ? appPath : undefined;
		const debugId = filename ? debugIds.get(filename) : undefined;
		const position = /:(\d+):(\d+)$/.exec(location);
		const lineno = position ? Number(position[1]) : undefined;
		const colno = position ? Number(position[2]) : undefined;
		const fn = name && name.length <= 128 && SAFE_FUNCTION.test(name) ? safeSentryFunctionName(name, "renderer_frame") : "renderer_frame";
		frames.push({ function: fn, ...(filename ? { filename } : {}), ...(lineno && Number.isSafeInteger(lineno) && lineno <= 1_000_000 ? { lineno } : {}), ...(colno && Number.isSafeInteger(colno) && colno <= 10_000_000 ? { colno } : {}), ...(debugId ? { debug_id: debugId } : {}) });
		if (frames.length === 8) break;
	}
	return frames.reverse();
}

export function captureExceptionToSentry(error: unknown, meta: CaptureMeta = {}): void {
	const triage = classifyError(meta);
	forwardCapture({
		kind: triage.report ? "exception" : "breadcrumb",
		message: "Renderer error",
		level: triage.level,
		tags: tagsFor(meta, triage),
		...(triage.report ? { exception: { type: safeSentryExceptionType((error as Error)?.name), frames: safeRendererFrames(error) } } : {}),
	});
}
export function captureApiErrorToSentry(operation: string, category: string, status?: number, code?: string, requestId?: string): void {
	const meta: CaptureMeta = { operation, category, httpStatus: status, code, requestId, domain: domainOf(operation) };
	const triage = classifyError(meta);
	forwardCapture({ kind: triage.report ? "message" : "breadcrumb", message: `${operation} ${category}`, level: triage.level, tags: tagsFor(meta, triage) });
}
