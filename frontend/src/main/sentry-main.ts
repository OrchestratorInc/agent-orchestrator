// Electron main is the only desktop Sentry sender. The policy controller owns
// this transport's lifetime, and the SDK never receives raw renderer text.
import * as Sentry from "@sentry/electron/main";
import { rm } from "node:fs/promises";
import path from "node:path";
import type { RendererTelemetryCapture } from "../shared/telemetry-policy";
import { DEFAULT_ELECTRON_SENTRY_DSN, SAFE_FUNCTION, safeSentryExceptionType, sentryEnvironment } from "../shared/sentry-config";
import { loadOrCreateTelemetryInstallId } from "../shared/telemetry";
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
	return values.some((value) => typeof value === "string" && (
		/^AbortError$/i.test(value) ||
		/\b(?:ERR_NETWORK_CHANGED|ERR_INTERNET_DISCONNECTED|ERR_NETWORK_IO_SUSPENDED|network changed|daemon not ready|DAEMON_NOT_READY|daemon_unavailable)\b/i.test(value)
	));
}

function scrubMainSentryEvent(event: Sentry.ErrorEvent, installId: string, version: string, environment: string): Sentry.ErrorEvent | null {
	if (isExpectedNoise(event)) return null;
	const renderer = event.tags?.["ao.origin"] === "renderer";
	const processReason = typeof event.message === "string"
		? PROCESS_REASONS.find((reason) => event.message?.endsWith(`process exited with '${reason}'`))
		: undefined;
	const title = scrubTelemetryText(renderer ? "Renderer error" : processReason ? "Electron process failure" : "Electron main error");
	const rendererKind = event.tags?.["ao.kind"];
	const kind = renderer
		? (rendererKind === "exception" || rendererKind === "message" ? rendererKind : "unknown")
		: processReason ? "process_exit"
		: event.level === "fatal" && event.exception?.values?.some((value) => value.mechanism?.type === "generic") ? "uncaught_exception"
		: event.exception?.values?.some((value) => value.mechanism?.type === "auto.node.onunhandledrejection") ? "unhandled_rejection"
		: "main_error";
	const category = event.tags?.["ao.category"];
	const code = event.tags?.["ao.code"];
	const safeCategory = typeof category === "string" && SAFE_CATEGORIES.has(category) ? category : "unknown";
	const safeCode = typeof code === "string" && SAFE_CODES.has(code) ? code : "unknown";
	const exceptionType = safeSentryExceptionType(event.exception?.values?.[0]?.type);
	const frames = event.exception?.values?.flatMap((value) => value.stacktrace?.frames ?? [])
		.filter((frame) => frame.in_app !== false && typeof frame.function === "string" && frame.function.length <= 128 && SAFE_FUNCTION.test(frame.function))
		.slice(-8)
		.map((frame) => ({ function: frame.function, lineno: frame.lineno, in_app: true }));
	const topFunction = frames?.at(-1)?.function ?? "unknown";
	return {
		type: undefined,
		event_id: event.event_id,
		timestamp: event.timestamp,
		level: event.level === "fatal" || event.level === "error" || event.level === "warning" ? event.level : "error",
		message: title,
		fingerprint: ["electron", kind, renderer ? safeCode : processReason ?? safeCode, exceptionType, topFunction, renderer ? "renderer" : "electron-main"],
		tags: { surface: renderer ? "renderer" : "electron-main", kind, ...(renderer ? { category: safeCategory, code: safeCode } : {}), ...(processReason ? { reason: processReason } : {}) },
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
		tracesSampleRate: 0,
		skipOpenTelemetrySetup: true,
		beforeBreadcrumb: () => null,
		beforeSend: (event) => scrubMainSentryEvent(event, installId, version, environment),
	});
	const uncaughtListeners = process.listeners("uncaughtException").filter((listener) => !priorUncaught.has(listener));
	const rejectionListeners = process.listeners("unhandledRejection").filter((listener) => !priorRejections.has(listener));
	childProcessHookInstalled = true;
	Sentry.setUser({ id: installId });
	return {
		capture(request) {
			if (!accepting) return;
			const safe = sanitizeRendererCapture(request);
			if (!safe || safe.kind === "breadcrumb" || safe.tags?.category === "daemon_unavailable" || /\b(?:AbortError|ERR_NETWORK_CHANGED|ERR_INTERNET_DISCONNECTED|DAEMON_NOT_READY)\b/i.test(safe.message)) return;
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
			if (Object.keys(entry).some((key) => key !== "function" && key !== "lineno")) return null;
			if (typeof entry.function !== "string" || entry.function.length > 128 || !SAFE_FUNCTION.test(entry.function)) return null;
			if (entry.lineno !== undefined && (typeof entry.lineno !== "number" || !Number.isSafeInteger(entry.lineno) || entry.lineno < 1 || entry.lineno > 1_000_000)) return null;
			frames.push({ function: entry.function, ...(entry.lineno === undefined ? {} : { lineno: entry.lineno as number }) });
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
