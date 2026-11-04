// Electron main is the only desktop Sentry sender. The policy controller owns
// this transport's lifetime, and the SDK never receives raw renderer text.
import * as Sentry from "@sentry/electron/main";
import { rm } from "node:fs/promises";
import path from "node:path";
import type { RendererTelemetryCapture } from "../shared/telemetry-policy";
import { DEFAULT_ELECTRON_SENTRY_DSN, ELECTRON_SENTRY_ERROR_SAMPLE_RATE, SAFE_APP_ASSET, SAFE_FUNCTION, safeSentryExceptionType, safeSentryFunctionName, sentryEnvironment } from "../shared/sentry-config";
import { loadOrCreateTelemetryInstallId } from "../shared/telemetry";
import { CLOUD_MALFORMED_RESPONSE, CLOUD_ROUTE, EXPECTED_NETWORK_NOISE, isOpaqueRequestId } from "../shared/cloud-cp-errors";
import type { DesktopTelemetryTransport } from "./desktop-telemetry-controller";

export function electronMainSentryDsn(isDev: boolean): string {
	return process.env.AO_ELECTRON_SENTRY_DSN ?? (isDev ? "" : DEFAULT_ELECTRON_SENTRY_DSN);
}

const CAPTURE_KEYS = new Set(["consentGeneration", "kind", "level", "message", "tags", "exception"]);
const TAG_KEYS = new Set([
	"apierr_kind",
	"category",
	"code",
	"domain",
	"http_status",
	"operation",
	"origin",
	"owner",
	"platform",
	"request_id",
	"route",
	"severity",
	"surface",
]);
const CAPTURE_KINDS = new Set<RendererTelemetryCapture["kind"]>(["exception", "message", "breadcrumb"]);
const CAPTURE_LEVELS = new Set<NonNullable<RendererTelemetryCapture["level"]>>(["fatal", "error", "warning", "info"]);
const LOCAL_URL = /(?:\bfile:\/\/\/\S+|\bapp:\/\/renderer\/\S+|\bhttps?:\/\/(?:localhost|127\.0\.0\.1|\[::1\])(?::\d+)?\S*)/gi;
const HOME_PATH = /\/(?:Users|home)\/[^\s"']+/g;
const WIN_PATH = /[A-Za-z]:\\[^\s"']+|\\\\[^\s"']+/g;
const SAFE_CATEGORIES = new Set(["agent_runtime", "http_5xx", "integration", "malformed_response", "native_crash", "render_crash", "update"]);
const SAFE_CODES = new Set([
	"AGENT_RESUME_FAILED", "CHAT_DELIVERY_FAILED", "CONVERSATION_READ_FAILED",
	"INTERNAL_ERROR", "ORCHESTRATOR_SPAWN_FAILED", "REPORT_CREATE_FAILED",
	"REPORT_LIST_FAILED", "REVIEW_OPERATION_FAILED", "WORKSPACE_GIT_READ_FAILED",
	// AO Cloud's stable machine codes (contracts/cloud/openapi.yaml and the
	// cloud client), plus the one the desktop names itself.
	CLOUD_MALFORMED_RESPONSE,
	"CHAT_ADAPTER_UNAVAILABLE", "CHAT_DRIVER_UNAVAILABLE", "CHAT_START_UNSUPPORTED", "COMPOSER_LOCKED",
	"IDEMPOTENCY_CONFLICT", "INVALID_EVENT_TYPE", "MERGE_NEEDS_APPROVAL", "MODEL_CATALOG_UNAVAILABLE",
	"SESSION_MODE_UNSUPPORTED", "SESSION_TERMINATED", "STALE_WORKER_TOKEN", "TERMINAL_SESSION_EXITED",
	"TRANSPORT_TIMEOUT", "UPSTREAM_UNAVAILABLE", "WORKER_OPERATION_FAILED", "WORKER_TIMEOUT", "WORKER_UNAVAILABLE",
	"internal_error", "invalid_request", "rate_limited", "request_timeout", "validation_error",
]);
// Where a renderer API error came from: AO Cloud answering, or the local
// daemon. AO Cloud not being reached ("network") is never sent: it is this
// computer's network, or an outage the control plane's uptime alarm reports.
const SAFE_ORIGINS = new Set(["cloud", "daemon"]);
const HTTP_STATUS = /^(?:0|[1-5]\d\d)$/;
const PROCESS_REASONS = ["abnormal-exit", "crashed", "oom", "launch-failed", "integrity-failure"] as const;
const EXPECTED_NOISE = EXPECTED_NETWORK_NOISE;
const DRAIN_TIMEOUT_MS = 2_000;
let childProcessHookInstalled = false;

function sentryQueuePath(cacheRoot: string): string {
	return path.join(cacheRoot, "sentry", "queue");
}

export async function clearMainSentryCache(cacheRoot: string): Promise<void> {
	await rm(sentryQueuePath(cacheRoot), { recursive: true, force: true });
}

async function waitForInFlightSends(inFlight: Set<Promise<unknown>>): Promise<void> {
	if (!inFlight.size) return;
	let timeout: ReturnType<typeof setTimeout> | undefined;
	try {
		const settled = Promise.allSettled([...inFlight]).then(() => true);
		const timedOut = new Promise<false>((resolve) => { timeout = setTimeout(() => resolve(false), DRAIN_TIMEOUT_MS); });
		if (!(await Promise.race([settled, timedOut]))) throw new Error("Electron Sentry retry drain timed out");
	} finally {
		if (timeout) clearTimeout(timeout);
	}
}

function isExpectedNoise(event: Sentry.ErrorEvent): boolean {
	const values = [event.message, ...(event.exception?.values ?? []).flatMap((value) => [value.type, value.value])];
	return values.some((value) => typeof value === "string" && EXPECTED_NOISE.test(value));
}

function scrubMainSentryEvent(event: Sentry.ErrorEvent, installId: string, version: string, environment: string): Sentry.ErrorEvent | null {
	if (isExpectedNoise(event)) return null;
	const renderer = event.tags?.["ao.origin"] === "renderer";
	const processReason = typeof event.message === "string"
		? PROCESS_REASONS.find((reason) => event.message?.endsWith(`process exited with '${reason}'`))
		: undefined;
	const rendererKind = event.tags?.["ao.kind"];
	const kind = renderer
		? (rendererKind === "exception" || rendererKind === "message" ? rendererKind : "unknown")
		: processReason ? "process_exit"
		: event.level === "fatal" && event.exception?.values?.some((value) => value.mechanism?.type === "generic") ? "uncaught_exception"
		: event.exception?.values?.some((value) => value.mechanism?.type === "auto.node.onunhandledrejection") ? "unhandled_rejection"
		: "main_error";
	const category = event.tags?.["ao.category"];
	const code = event.tags?.["ao.code"];
	const errorOrigin = event.tags?.["ao.error_origin"];
	const httpStatus = event.tags?.["ao.http_status"];
	const requestId = event.tags?.["ao.request_id"];
	const safeOrigin = renderer && typeof errorOrigin === "string" && SAFE_ORIGINS.has(errorOrigin) ? errorOrigin : undefined;
	const cloudOrigin = safeOrigin === "cloud";
	const route = event.tags?.["ao.route"];
	const safeCategory = typeof category === "string" && SAFE_CATEGORIES.has(category) ? category : "unknown";
	const safeCode = typeof code === "string" && SAFE_CODES.has(code) ? code : "unknown";
	const exceptionType = safeSentryExceptionType(event.exception?.values?.[0]?.type);
	const frames = event.exception?.values?.flatMap((value) => value.stacktrace?.frames ?? [])
		.filter((frame) => frame.in_app !== false && typeof frame.function === "string" && frame.function.length <= 128 && SAFE_FUNCTION.test(frame.function))
		.slice(-8)
		.map((frame) => ({ function: safeSentryFunctionName(frame.function, renderer ? "renderer_frame" : "main_frame"), ...(renderer && typeof frame.filename === "string" && SAFE_APP_ASSET.test(frame.filename) ? { filename: frame.filename } : {}), ...(typeof frame.lineno === "number" && Number.isSafeInteger(frame.lineno) && frame.lineno >= 1 && frame.lineno <= 1_000_000 ? { lineno: frame.lineno } : {}), ...(typeof frame.colno === "number" && Number.isSafeInteger(frame.colno) && frame.colno >= 1 && frame.colno <= 10_000_000 ? { colno: frame.colno } : {}), in_app: true }));
	const topFrame = frames?.at(-1);
	const topLocation = renderer && topFrame && "filename" in topFrame && typeof topFrame.filename === "string" && "lineno" in topFrame && typeof topFrame.lineno === "number"
		? `${topFrame.filename.replace(/-[A-Za-z0-9_-]{8,16}\.(?:js|mjs)$/, "")}:${topFrame.lineno}${"colno" in topFrame && typeof topFrame.colno === "number" ? `:${topFrame.colno}` : ""}`
		: undefined;
	const mainLocation = !renderer && topFrame && "lineno" in topFrame && typeof topFrame.lineno === "number"
		? `main:${topFrame.lineno}${"colno" in topFrame && typeof topFrame.colno === "number" ? `:${topFrame.colno}` : ""}`
		: undefined;
	const rendererLocation = renderer && topFrame && "lineno" in topFrame && typeof topFrame.lineno === "number"
		? topLocation ?? `renderer:${topFrame.lineno}${"colno" in topFrame && typeof topFrame.colno === "number" ? `:${topFrame.colno}` : ""}`
		: undefined;
	const topFunction = topFrame?.function === "renderer_frame" ? rendererLocation ?? "renderer_frame"
		: topFrame?.function === "main_frame" ? mainLocation ?? "main_frame" : topFrame?.function ?? "unknown";
	const rendererLabel = safeCode !== "unknown" ? safeCode : safeCategory;
	const title = renderer
		? cloudOrigin ? `Renderer AO Cloud error${kind === "exception" ? ` in ${topFunction}` : ""} (${rendererLabel})`
		: kind === "message" ? `Renderer API error (${rendererLabel})` : `Renderer ${exceptionType} in ${topFunction} (${rendererLabel})`
		: processReason ? "Electron process failure" : `Electron main ${exceptionType} in ${topFunction}`;
	return {
		type: undefined,
		event_id: event.event_id,
		timestamp: event.timestamp,
		level: event.level === "fatal" || event.level === "error" || event.level === "warning" ? event.level : "error",
		message: title,
		// An AO Cloud failure never groups with a daemon one that shares its code.
		fingerprint: ["electron", kind, renderer ? rendererLabel : processReason ?? safeCode, exceptionType, topFunction, renderer ? "renderer" : "electron-main", ...(cloudOrigin ? ["cloud"] : [])],
		tags: {
			surface: renderer ? "renderer" : "electron-main",
			kind,
			...(renderer ? { category: safeCategory, code: safeCode } : {}),
			...(safeOrigin ? { origin: safeOrigin } : {}),
			...(renderer && typeof httpStatus === "string" && HTTP_STATUS.test(httpStatus) ? { http_status: httpStatus } : {}),
			// Only an AO Cloud request's id, and only an opaque UUID: it matches the
			// control plane's logs and says nothing about this computer.
			...(cloudOrigin && isOpaqueRequestId(requestId) ? { request_id: requestId } : {}),
			// Which endpoint failed, ids already replaced (cloudRoute).
			...(cloudOrigin && typeof route === "string" && CLOUD_ROUTE.test(route) ? { route } : {}),
			...(processReason ? { reason: processReason } : {}),
		},
		user: { id: installId },
		release: version,
		environment,
		exception: { values: [{ type: exceptionType, value: "", ...(frames?.length ? { stacktrace: { frames } } : {}) }] },
	};
}

export async function initMainSentry(version: string, dataDir: string, cacheRoot: string, isDev = false): Promise<DesktopTelemetryTransport> {
	const dsn = electronMainSentryDsn(isDev);
	if (!dsn) return { capture: () => undefined, closeAndDrain: async () => undefined, clearCache: async () => undefined };
	const installId = await loadOrCreateTelemetryInstallId(dataDir);
	const environment = sentryEnvironment(process.env.AO_TELEMETRY_APP_VERSION ?? version);
	const queuePath = sentryQueuePath(cacheRoot);
	let accepting = true;
	const inFlight = new Set<Promise<unknown>>();
	const priorUncaught = new Set(process.listeners("uncaughtException"));
	const priorRejections = new Set(process.listeners("unhandledRejection"));
	Sentry.init({
		dsn,
		release: version,
		environment,
		transport: Sentry.makeElectronOfflineTransport((options) => {
			const base = Sentry.makeElectronTransport(options);
			return {
				send(envelope) {
					if (!accepting) return Promise.reject(new Error("Electron Sentry sender is closed"));
					const sending = Promise.resolve(base.send(envelope));
					inFlight.add(sending);
					void sending.then(() => inFlight.delete(sending), () => inFlight.delete(sending));
					return sending;
				},
				flush: (timeout) => base.flush(timeout),
			};
		}),
		transportOptions: { queuePath, shouldSend: () => accepting, shouldStore: () => accepting },
		ipcMode: 0 as Sentry.IPCMode,
		defaultIntegrations: [],
		integrations: [
			Sentry.onUncaughtExceptionIntegration(),
			Sentry.onUnhandledRejectionIntegration(),
			...(childProcessHookInstalled ? [] : [Sentry.childProcessIntegration({ breadcrumbs: false, events: [...PROCESS_REASONS] })]),
		],
		maxBreadcrumbs: 0,
		sendDefaultPii: false,
		// The SDK samples after beforeSend, including fatal events. Sample regular
		// errors below so main crashes and renderer fatal events always survive.
		sampleRate: 1,
		tracesSampleRate: 0,
		skipOpenTelemetrySetup: true,
		beforeBreadcrumb: () => null,
		beforeSend: (event) => {
			const safe = scrubMainSentryEvent(event, installId, version, environment);
			if (!safe || (safe.level !== "fatal" && Math.random() >= ELECTRON_SENTRY_ERROR_SAMPLE_RATE)) return null;
			return safe;
		},
	});
	const uncaughtListeners = process.listeners("uncaughtException").filter((listener) => !priorUncaught.has(listener));
	const rejectionListeners = process.listeners("unhandledRejection").filter((listener) => !priorRejections.has(listener));
	childProcessHookInstalled = true;
	Sentry.setUser({ id: installId });
	return {
		capture(request) {
			if (!accepting) return;
			const safe = sanitizeRendererCapture(request);
			if (!safe || safe.kind === "breadcrumb" || safe.tags?.category === "daemon_unavailable" || EXPECTED_NOISE.test(safe.message)) return;
			const category = safe.tags?.category;
			const code = safe.tags?.code;
			Sentry.captureEvent({
				message: "Renderer error",
				level: safe.level ?? "error",
				...(safe.exception ? { exception: { values: [{ type: safe.exception.type, value: "", stacktrace: { frames: safe.exception.frames.map((frame) => ({ ...frame, in_app: true })) } }] } } : {}),
				tags: {
					"ao.origin": "renderer",
					"ao.kind": safe.kind,
					"ao.category": category && SAFE_CATEGORIES.has(category) ? category : "unknown",
					"ao.code": code && SAFE_CODES.has(code) ? code : "unknown",
					...(safe.tags?.origin && SAFE_ORIGINS.has(safe.tags.origin) ? { "ao.error_origin": safe.tags.origin } : {}),
					...(safe.tags?.http_status && HTTP_STATUS.test(safe.tags.http_status) ? { "ao.http_status": safe.tags.http_status } : {}),
					...(isOpaqueRequestId(safe.tags?.request_id) ? { "ao.request_id": safe.tags.request_id } : {}),
					...(safe.tags?.route && CLOUD_ROUTE.test(safe.tags.route) ? { "ao.route": safe.tags.route } : {}),
				},
			});
		},
		async closeAndDrain() {
			accepting = false;
			let drained = false;
			try {
				drained = await Sentry.close(DRAIN_TIMEOUT_MS);
			} finally {
				for (const listener of uncaughtListeners) process.removeListener("uncaughtException", listener);
				for (const listener of rejectionListeners) process.removeListener("unhandledRejection", listener);
				await waitForInFlightSends(inFlight);
			}
			if (!drained) throw new Error("Electron Sentry drain timed out");
		},
		async clearCache() {
			await clearMainSentryCache(cacheRoot);
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
	let exception: RendererTelemetryCapture["exception"];
	if (input.exception !== undefined) {
		if (input.kind !== "exception" || !input.exception || typeof input.exception !== "object" || Array.isArray(input.exception)) return null;
		const value = input.exception as Record<string, unknown>;
		if (Object.keys(value).some((key) => key !== "type" && key !== "frames")) return null;
		if (typeof value.type !== "string" || safeSentryExceptionType(value.type) !== value.type) return null;
		if (!Array.isArray(value.frames) || value.frames.length > 8) return null;
		const frames: NonNullable<RendererTelemetryCapture["exception"]>["frames"] = [];
		for (const frame of value.frames) {
			if (!frame || typeof frame !== "object" || Array.isArray(frame)) return null;
			const entry = frame as Record<string, unknown>;
			if (Object.keys(entry).some((key) => key !== "function" && key !== "filename" && key !== "lineno" && key !== "colno")) return null;
			if (typeof entry.function !== "string" || entry.function.length > 128 || !SAFE_FUNCTION.test(entry.function)) return null;
			if (entry.filename !== undefined && (typeof entry.filename !== "string" || !SAFE_APP_ASSET.test(entry.filename))) return null;
			if (entry.lineno !== undefined && (typeof entry.lineno !== "number" || !Number.isSafeInteger(entry.lineno) || entry.lineno < 1 || entry.lineno > 1_000_000)) return null;
			if (entry.colno !== undefined && (typeof entry.colno !== "number" || !Number.isSafeInteger(entry.colno) || entry.colno < 1 || entry.colno > 10_000_000)) return null;
			frames.push({ function: safeSentryFunctionName(entry.function, "renderer_frame"), ...(entry.filename === undefined ? {} : { filename: entry.filename as string }), ...(entry.lineno === undefined ? {} : { lineno: entry.lineno as number }), ...(entry.colno === undefined ? {} : { colno: entry.colno as number }) });
		}
		exception = { type: value.type, frames };
	}

	let tags: Record<string, string> | undefined;
	if (input.tags !== undefined) {
		if (!input.tags || typeof input.tags !== "object" || Array.isArray(input.tags)) return null;
		const entries = Object.entries(input.tags as Record<string, unknown>);
		if (entries.length > TAG_KEYS.size) return null;
		tags = {};
		for (const [key, value] of entries) {
			if (!TAG_KEYS.has(key) || typeof value !== "string" || value.length > 128) return null;
			// A request id crosses only as an opaque UUID, and an origin only from its vocabulary.
			if (key === "request_id" && !isOpaqueRequestId(value)) return null;
			if (key === "origin" && !SAFE_ORIGINS.has(value)) return null;
			if (key === "route" && !CLOUD_ROUTE.test(value)) return null;
			tags[key] = scrubTelemetryText(value);
		}
	}

	return {
		consentGeneration: input.consentGeneration,
		kind: input.kind as RendererTelemetryCapture["kind"],
		message: scrubTelemetryText(input.message),
		...(input.level === undefined ? {} : { level: input.level as NonNullable<RendererTelemetryCapture["level"]> }),
		...(tags === undefined ? {} : { tags }),
		...(exception === undefined ? {} : { exception }),
	};
}

function scrubTelemetryText(value: string): string {
	return value
		.replace(LOCAL_URL, "[redacted-url]")
		.replace(HOME_PATH, "[redacted-path]")
		.replace(WIN_PATH, "[redacted-path]");
}
