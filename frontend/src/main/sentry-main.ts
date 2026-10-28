// Electron main is the only desktop Sentry sender. The policy controller owns
// this transport's lifetime, and the SDK never receives raw renderer text.
import * as Sentry from "@sentry/electron/main";
import type { RendererTelemetryCapture } from "../shared/telemetry-policy";
import { DEFAULT_ELECTRON_SENTRY_DSN } from "../shared/sentry-config";
import { loadOrCreateTelemetryInstallId } from "../shared/telemetry";
import type { DesktopTelemetryTransport } from "./desktop-telemetry-controller";

export function electronMainSentryDsn(): string {
	return process.env.AO_SENTRY_DSN ?? DEFAULT_ELECTRON_SENTRY_DSN;
}

const CAPTURE_KEYS = new Set(["consentGeneration", "kind", "level", "message", "tags"]);
const TAG_KEYS = new Set([
	"apierr_kind",
	"category",
	"code",
	"domain",
	"http_status",
	"operation",
	"owner",
	"platform",
	"severity",
	"surface",
]);
const CAPTURE_KINDS = new Set<RendererTelemetryCapture["kind"]>(["exception", "message", "breadcrumb"]);
const CAPTURE_LEVELS = new Set<NonNullable<RendererTelemetryCapture["level"]>>(["fatal", "error", "warning", "info"]);
const LOCAL_URL = /(?:\bfile:\/\/\/\S+|\bapp:\/\/renderer\/\S+|\bhttps?:\/\/(?:localhost|127\.0\.0\.1|\[::1\])(?::\d+)?\S*)/gi;
const HOME_PATH = /\/(?:Users|home)\/[^\s"']+/g;
const WIN_PATH = /[A-Za-z]:\\[^\s"']+|\\\\[^\s"']+/g;
const SAFE_CATEGORIES = new Set(["agent_runtime", "http_5xx", "integration", "native_crash", "render_crash", "update"]);
const SAFE_CODES = new Set(["AGENT_RESUME_FAILED", "INTERNAL_ERROR", "WORKSPACE_GIT_READ_FAILED"]);
const PROCESS_REASONS = ["abnormal-exit", "crashed", "oom", "launch-failed", "integrity-failure"] as const;
const SAFE_FUNCTION = /^[A-Za-z_$][A-Za-z0-9_$.:<>]*$/;
const DRAIN_TIMEOUT_MS = 2_000;
let crashHooksInstalled = false;

function scrubMainSentryEvent(event: Sentry.ErrorEvent, installId: string, version: string, environment: string): Sentry.ErrorEvent {
	const renderer = event.tags?.["ao.origin"] === "renderer";
	const processReason = typeof event.message === "string"
		? PROCESS_REASONS.find((reason) => event.message?.endsWith(`process exited with '${reason}'`))
		: undefined;
	const title = renderer ? "Renderer error" : processReason ? "Electron process failure" : "Electron main error";
	const category = event.tags?.["ao.category"];
	const code = event.tags?.["ao.code"];
	const safeCategory = typeof category === "string" && SAFE_CATEGORIES.has(category) ? category : "unknown";
	const safeCode = typeof code === "string" && SAFE_CODES.has(code) ? code : "unknown";
	const frames = event.exception?.values?.flatMap((value) => value.stacktrace?.frames ?? [])
		.filter((frame) => typeof frame.function === "string" && SAFE_FUNCTION.test(frame.function))
		.map((frame) => ({ function: frame.function, lineno: frame.lineno, in_app: true }));
	return {
		type: undefined,
		event_id: event.event_id,
		timestamp: event.timestamp,
		level: event.level === "fatal" || event.level === "error" || event.level === "warning" ? event.level : "error",
		message: title,
		fingerprint: renderer ? ["electron", "renderer", safeCategory, safeCode] : ["electron", processReason ?? "main", frames?.at(-1)?.function ?? "unknown"],
		tags: { surface: renderer ? "renderer" : "electron-main", ...(renderer ? { category: safeCategory, code: safeCode } : {}), ...(processReason ? { reason: processReason } : {}) },
		user: { id: installId },
		release: version,
		environment,
		exception: { values: [{ type: title, value: "", ...(frames?.length ? { stacktrace: { frames } } : {}) }] },
	};
}

export async function initMainSentry(version: string, dataDir: string, isPackaged: boolean): Promise<DesktopTelemetryTransport> {
	const dsn = electronMainSentryDsn();
	if (!dsn) return { capture: () => undefined, closeAndDrain: async () => undefined, clearCache: async () => undefined };
	const installId = await loadOrCreateTelemetryInstallId(dataDir);
	const environment = isPackaged ? "stable" : "development";
	Sentry.init({
		dsn,
		release: version,
		environment,
		transport: Sentry.makeElectronTransport,
		ipcMode: 0 as Sentry.IPCMode,
		defaultIntegrations: [],
		integrations: crashHooksInstalled ? [] : [
			Sentry.onUncaughtExceptionIntegration(),
			Sentry.onUnhandledRejectionIntegration(),
			Sentry.childProcessIntegration({ breadcrumbs: false, events: [...PROCESS_REASONS] }),
		],
		maxBreadcrumbs: 0,
		sendDefaultPii: false,
		tracesSampleRate: 0,
		skipOpenTelemetrySetup: true,
		beforeBreadcrumb: () => null,
		beforeSend: (event) => scrubMainSentryEvent(event, installId, version, environment),
	});
	crashHooksInstalled = true;
	Sentry.setUser({ id: installId });
	let accepting = true;
	return {
		capture(request) {
			if (!accepting) return;
			const safe = sanitizeRendererCapture(request);
			if (!safe || safe.kind === "breadcrumb" || safe.tags?.category === "daemon_unavailable") return;
			const category = safe.tags?.category;
			const code = safe.tags?.code;
			Sentry.captureEvent({
				message: "Renderer error",
				level: safe.level ?? "error",
				tags: {
					"ao.origin": "renderer",
					"ao.category": category && SAFE_CATEGORIES.has(category) ? category : "unknown",
					"ao.code": code && SAFE_CODES.has(code) ? code : "unknown",
				},
			});
		},
		async closeAndDrain() {
			accepting = false;
			if (!(await Sentry.close(DRAIN_TIMEOUT_MS))) throw new Error("Electron Sentry drain timed out");
		},
		async clearCache() {
			// The online transport has no disk queue. Clear both SDK scopes after
			// the controller has drained and closed its sender.
			Sentry.getCurrentScope().clear();
			Sentry.getIsolationScope().clear();
		},
	};
}

export function sanitizeRendererCapture(request: unknown): RendererTelemetryCapture | null {
	if (!request || typeof request !== "object" || Array.isArray(request)) return null;
	const input = request as Record<string, unknown>;
	if (Object.keys(input).some((key) => !CAPTURE_KEYS.has(key))) return null;
	if (typeof input.message !== "string" || input.message.length > 4096) return null;
	if (typeof input.consentGeneration !== "string" || input.consentGeneration.length > 256) return null;
	if (typeof input.kind !== "string" || !CAPTURE_KINDS.has(input.kind as RendererTelemetryCapture["kind"])) return null;
	if (input.level !== undefined && (typeof input.level !== "string" || !CAPTURE_LEVELS.has(input.level as NonNullable<RendererTelemetryCapture["level"]>))) return null;

	let tags: Record<string, string> | undefined;
	if (input.tags !== undefined) {
		if (!input.tags || typeof input.tags !== "object" || Array.isArray(input.tags)) return null;
		const entries = Object.entries(input.tags as Record<string, unknown>);
		if (entries.length > TAG_KEYS.size) return null;
		tags = {};
		for (const [key, value] of entries) {
			if (!TAG_KEYS.has(key) || typeof value !== "string" || value.length > 128) return null;
			tags[key] = scrubTelemetryText(value);
		}
	}

	return {
		consentGeneration: input.consentGeneration,
		kind: input.kind as RendererTelemetryCapture["kind"],
		message: scrubTelemetryText(input.message),
		...(input.level === undefined ? {} : { level: input.level as NonNullable<RendererTelemetryCapture["level"]> }),
		...(tags === undefined ? {} : { tags }),
	};
}

function scrubTelemetryText(value: string): string {
	return value
		.replace(LOCAL_URL, "[redacted-url]")
		.replace(HOME_PATH, "[redacted-path]")
		.replace(WIN_PATH, "[redacted-path]");
}
