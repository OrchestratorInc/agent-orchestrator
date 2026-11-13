// Electron main is the only desktop Sentry sender. The policy controller owns
// this transport's lifetime, and the SDK never receives raw renderer text.
import * as Sentry from "@sentry/electron/main";
import { createHash } from "node:crypto";
import { readFileSync, renameSync, writeFileSync } from "node:fs";
import { rm } from "node:fs/promises";
import path from "node:path";
import type { RendererTelemetryCapture } from "../shared/telemetry-policy";
import { DEFAULT_ELECTRON_SENTRY_DSN, isPublishedSentryBuild, SAFE_APP_ASSET, SAFE_FUNCTION, safeSentryExceptionType, safeSentryFunctionName, sentryEnvironment } from "../shared/sentry-config";
import { loadOrCreateTelemetryInstallId } from "../shared/telemetry";
import type { DesktopTelemetryTransport } from "./desktop-telemetry-controller";
import { isSafeMainErrorDetail, MAIN_ERROR_CATEGORIES, MAIN_ERROR_REASONS, sanitizeMainErrorCapture, type MainErrorCapture } from "./main-error-capture";

export function electronMainSentryDsn(version: string, isDev: boolean): string {
	return process.env.AO_ELECTRON_SENTRY_DSN ?? (isPublishedSentryBuild(version, isDev) ? DEFAULT_ELECTRON_SENTRY_DSN : "");
}

// One install in ten sends non-fatal errors: sha256(install_id) read as a
// big-endian uint32, modulo 10, equals 0. The daemon uses the same hash
// (sentryobs.InstallInErrorSample), so an install is in or out everywhere.
// Main-process crashes and uncaught exceptions send from every install.
const ERROR_SAMPLE_RATE = "0.1";
const ALWAYS_SENT_KINDS = new Set(["uncaught_exception", "process_exit"]);

export function installInErrorSample(installId: string): boolean {
	return installId !== "" && createHash("sha256").update(installId).digest().readUInt32BE(0) % 10 === 0;
}

// Per install and UTC day, keyed on the final scrubbed fingerprint. The
// counters live on disk so a reload, a new window or a restart cannot hand a
// crash loop a fresh budget.
const ERRORS_PER_FINGERPRINT_PER_DAY = 3;
const ERRORS_PER_INSTALL_PER_DAY = 25;
export const ERROR_BUDGET_FILE = "telemetry_sentry_budget.json";
type ErrorBudgetState = { day: string; total: number; fingerprints: Record<string, number> };

function readErrorBudget(file: string): ErrorBudgetState {
	try {
		const parsed = JSON.parse(readFileSync(file, "utf8")) as Partial<ErrorBudgetState> | null;
		if (parsed && typeof parsed.day === "string" && Number.isSafeInteger(parsed.total) && parsed.fingerprints && typeof parsed.fingerprints === "object" && !Array.isArray(parsed.fingerprints)) {
			const fingerprints = Object.fromEntries(Object.entries(parsed.fingerprints).filter((entry): entry is [string, number] => Number.isSafeInteger(entry[1])));
			return { day: parsed.day, total: parsed.total as number, fingerprints };
		}
	} catch { /* missing or corrupt: start a fresh day */ }
	return { day: "", total: 0, fingerprints: {} };
}

export function createErrorBudget(file: string): (fingerprint: readonly string[], now?: number) => boolean {
	let state = readErrorBudget(file);
	return (fingerprint, now = Date.now()) => {
		const day = new Date(now).toISOString().slice(0, 10);
		if (state.day !== day) state = { day, total: 0, fingerprints: {} };
		const key = fingerprint.join("|");
		const count = state.fingerprints[key] ?? 0;
		if (state.total >= ERRORS_PER_INSTALL_PER_DAY || count >= ERRORS_PER_FINGERPRINT_PER_DAY) return false;
		state = { day, total: state.total + 1, fingerprints: { ...state.fingerprints, [key]: count + 1 } };
		try {
			const temporary = `${file}.${process.pid}.tmp`;
			writeFileSync(temporary, JSON.stringify(state), { mode: 0o600 });
			renameSync(temporary, file);
		} catch { /* the in-memory budget still holds for this run */ }
		return true;
	};
}

function admitMainSentryEvent(event: Sentry.ErrorEvent | null, sampled: boolean, budget: (fingerprint: readonly string[]) => boolean): Sentry.ErrorEvent | null {
	if (!event) return null;
	const alwaysSent = ALWAYS_SENT_KINDS.has(String(event.tags?.kind));
	if (!alwaysSent && !sampled) return null;
	if (!budget(event.fingerprint ?? [])) return null;
	return { ...event, tags: { ...event.tags, sample_rate: alwaysSent ? "1" : ERROR_SAMPLE_RATE } };
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
const MAIN_CATEGORIES = new Set<string>(MAIN_ERROR_CATEGORIES);
const MAIN_REASONS = new Set<string>(MAIN_ERROR_REASONS);
const SAFE_CODES = new Set([
	"AGENT_RESUME_FAILED", "CHAT_DELIVERY_FAILED", "CONVERSATION_READ_FAILED",
	"INTERNAL_ERROR", "ORCHESTRATOR_SPAWN_FAILED", "REPORT_CREATE_FAILED",
	"REPORT_LIST_FAILED", "REVIEW_OPERATION_FAILED", "WORKSPACE_GIT_READ_FAILED",
]);
const SAFE_OPERATIONS = new Set(["query", "mutation", "route", "react_render", "open_orchestrator", "xterm_init"]);
const PROCESS_REASONS = ["abnormal-exit", "crashed", "oom", "launch-failed", "integrity-failure"] as const;
const EXPECTED_NOISE = /^(?:AbortError|net::ERR_NETWORK_CHANGED|net::ERR_INTERNET_DISCONNECTED|net::ERR_NETWORK_IO_SUSPENDED)$/i;
const DRAIN_TIMEOUT_MS = 2_000;
const DEBUG_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
// Electron main's own bundles. Any install path before .vite/build is dropped.
const MAIN_BUNDLE = /[\\/]\.vite[\\/]build[\\/]([A-Za-z0-9_][A-Za-z0-9_.-]{0,80}\.js)$/;

// The app-relative URL Sentry matches against an uploaded source map's debug
// ID: app://renderer/assets/... for the renderer, app:///.vite/build/... for main.
function safeAbsPath(filename: unknown, renderer: boolean): string | undefined {
	if (typeof filename !== "string") return undefined;
	if (renderer) {
		const asset = filename.startsWith("app://renderer/") ? filename.slice("app://renderer/".length) : filename;
		return SAFE_APP_ASSET.test(asset) ? `app://renderer/${asset}` : undefined;
	}
	const bundle = MAIN_BUNDLE.exec(filename)?.[1];
	return bundle ? `app:///.vite/build/${bundle}` : undefined;
}

function absPathOf(frame: { filename?: string; abs_path?: string }, renderer: boolean): { abs_path?: string } {
	const absPath = safeAbsPath(renderer ? frame.filename : frame.abs_path ?? frame.filename, renderer);
	return absPath ? { abs_path: absPath } : {};
}

function safeDebugImages(event: Sentry.ErrorEvent, renderer: boolean): Array<{ type: "sourcemap"; code_file: string; debug_id: string }> {
	const images = new Map<string, string>();
	for (const image of event.debug_meta?.images ?? []) {
		if (image.type !== "sourcemap" || typeof image.debug_id !== "string" || !DEBUG_ID.test(image.debug_id)) continue;
		const codeFile = safeAbsPath(image.code_file, renderer);
		if (codeFile) images.set(codeFile, image.debug_id.toLowerCase());
	}
	return [...images].slice(0, 8).map(([code_file, debug_id]) => ({ type: "sourcemap", code_file, debug_id }));
}
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
	if (event.tags?.["ao.origin"] === "main_handled") return scrubMainHandledEvent(event, installId, version, environment);
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
	const operation = event.tags?.["ao.operation"];
	const safeOperation = typeof operation === "string" && SAFE_OPERATIONS.has(operation) ? operation : undefined;
	const safeCategory = typeof category === "string" && SAFE_CATEGORIES.has(category) ? category : "unknown";
	const safeCode = typeof code === "string" && SAFE_CODES.has(code) ? code : "unknown";
	const exceptionType = safeSentryExceptionType(event.exception?.values?.[0]?.type);
	const frames = event.exception?.values?.flatMap((value) => value.stacktrace?.frames ?? [])
		.filter((frame) => frame.in_app !== false && typeof frame.function === "string" && frame.function.length <= 128 && SAFE_FUNCTION.test(frame.function))
		.slice(-8)
		.map((frame) => ({ function: safeSentryFunctionName(frame.function, renderer ? "renderer_frame" : "main_frame"), ...(renderer && typeof frame.filename === "string" && SAFE_APP_ASSET.test(frame.filename) ? { filename: frame.filename } : {}), ...absPathOf(frame, renderer), ...(typeof frame.lineno === "number" && Number.isSafeInteger(frame.lineno) && frame.lineno >= 1 && frame.lineno <= 1_000_000 ? { lineno: frame.lineno } : {}), ...(typeof frame.colno === "number" && Number.isSafeInteger(frame.colno) && frame.colno >= 1 && frame.colno <= 10_000_000 ? { colno: frame.colno } : {}), in_app: true }));
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
	const debugImages = frames?.length ? safeDebugImages(event, renderer) : [];
	const title = renderer
		? kind === "message" ? `Renderer API error (${rendererLabel})` : `Renderer ${exceptionType} in ${topFunction} (${rendererLabel})`
		: processReason ? "Electron process failure" : `Electron main ${exceptionType} in ${topFunction}`;
	return {
		type: undefined,
		// Rebuilt events must keep a JS platform: Sentry applies source maps
		// (debug IDs) only to javascript or node events.
		platform: renderer ? "javascript" : "node",
		event_id: event.event_id,
		timestamp: event.timestamp,
		level: event.level === "fatal" || event.level === "error" || event.level === "warning" ? event.level : "error",
		message: title,
		fingerprint: ["electron", kind, renderer ? rendererLabel : processReason ?? safeCode, exceptionType, topFunction, renderer ? "renderer" : "electron-main"],
		tags: { surface: renderer ? "renderer" : "electron-main", kind, ...(renderer ? { category: safeCategory, code: safeCode, ...(safeOperation ? { operation: safeOperation } : {}) } : {}), ...(processReason ? { reason: processReason } : {}) },
		user: { id: installId },
		release: version,
		environment,
		exception: { values: [{ type: exceptionType, value: "", ...(frames?.length ? { stacktrace: { frames } } : {}) }] },
		...(debugImages.length ? { debug_meta: { images: debugImages } } : {}),
	};
}

// A handled main-process failure reported through captureMainError. Its title
// and grouping come only from the fixed category and reason.
function scrubMainHandledEvent(event: Sentry.ErrorEvent, installId: string, version: string, environment: string): Sentry.ErrorEvent | null {
	const category = event.tags?.["ao.category"];
	const reason = event.tags?.["ao.reason"];
	if (typeof category !== "string" || !MAIN_CATEGORIES.has(category) || typeof reason !== "string" || !MAIN_REASONS.has(reason)) return null;
	const detail = event.tags?.["ao.detail"];
	const exceptionType = safeSentryExceptionType(event.exception?.values?.[0]?.type);
	return {
		type: undefined,
		platform: "node",
		event_id: event.event_id,
		timestamp: event.timestamp,
		level: event.level === "fatal" || event.level === "warning" ? event.level : "error",
		message: `Electron main ${category} failure (${reason})`,
		// IPC failures group per channel; everything else per category and reason.
		fingerprint: ["electron", "handled", category, reason, ...(category === "ipc" && isSafeMainErrorDetail(detail) ? [detail] : []), "electron-main"],
		tags: { surface: "electron-main", kind: "handled", category, reason, ...(isSafeMainErrorDetail(detail) ? { detail } : {}) },
		user: { id: installId },
		release: version,
		environment,
		exception: { values: [{ type: exceptionType, value: "" }] },
	};
}

function errorTypeOf(error: unknown): string {
	return error instanceof Error ? safeSentryExceptionType(error.name) : "Error";
}

export async function initMainSentry(version: string, dataDir: string, cacheRoot: string, isDev = false): Promise<DesktopTelemetryTransport> {
	const appVersion = process.env.AO_TELEMETRY_APP_VERSION ?? version;
	const dsn = electronMainSentryDsn(appVersion, isDev);
	if (!dsn) return { capture: () => undefined, closeAndDrain: async () => undefined, clearCache: async () => undefined };
	const installId = await loadOrCreateTelemetryInstallId(dataDir);
	const sampled = installInErrorSample(installId);
	const budget = createErrorBudget(path.join(dataDir, ERROR_BUDGET_FILE));
	const environment = sentryEnvironment(appVersion);
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
		// Sampling is per install, not per event, and happens in beforeSend
		// together with the persisted per-fingerprint and per-install caps.
		sampleRate: 1,
		tracesSampleRate: 0,
		skipOpenTelemetrySetup: true,
		beforeBreadcrumb: () => null,
		beforeSend: (event) => admitMainSentryEvent(scrubMainSentryEvent(event, installId, version, environment), sampled, budget),
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
			// The SDK replaces frame debug IDs with main's own lookup, so renderer
			// chunk IDs travel as images keyed by the frame's app URL.
			const debugImages = [...new Map((safe.exception?.frames ?? []).filter((frame) => frame.filename && frame.debug_id).map((frame) => [`app://renderer/${frame.filename}`, frame.debug_id as string])).entries()]
				.map(([code_file, debug_id]) => ({ type: "sourcemap" as const, code_file, debug_id }));
			Sentry.captureEvent({
				message: "Renderer error",
				level: safe.level ?? "error",
				...(safe.exception ? { exception: { values: [{ type: safe.exception.type, value: "", stacktrace: { frames: safe.exception.frames.map(({ debug_id: _debugId, ...frame }) => ({ ...frame, in_app: true })) } }] } } : {}),
				...(debugImages.length ? { debug_meta: { images: debugImages } } : {}),
				tags: {
					"ao.origin": "renderer",
					"ao.kind": safe.kind,
					"ao.category": category && SAFE_CATEGORIES.has(category) ? category : "unknown",
					"ao.code": code && SAFE_CODES.has(code) ? code : "unknown",
					...(safe.tags?.operation && SAFE_OPERATIONS.has(safe.tags.operation) ? { "ao.operation": safe.tags.operation } : {}),
				},
			});
		},
		captureMain(request: MainErrorCapture) {
			if (!accepting) return;
			const safe = sanitizeMainErrorCapture(request);
			if (!safe) return;
			Sentry.captureEvent({
				message: "Electron main handled error",
				level: "error",
				exception: { values: [{ type: errorTypeOf(safe.error), value: "" }] },
				tags: {
					"ao.origin": "main_handled",
					"ao.category": safe.category,
					"ao.reason": safe.reason,
					...(safe.detail ? { "ao.detail": safe.detail } : {}),
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
			if (Object.keys(entry).some((key) => key !== "function" && key !== "filename" && key !== "lineno" && key !== "colno" && key !== "debug_id")) return null;
			if (typeof entry.function !== "string" || entry.function.length > 128 || !SAFE_FUNCTION.test(entry.function)) return null;
			if (entry.filename !== undefined && (typeof entry.filename !== "string" || !SAFE_APP_ASSET.test(entry.filename))) return null;
			if (entry.lineno !== undefined && (typeof entry.lineno !== "number" || !Number.isSafeInteger(entry.lineno) || entry.lineno < 1 || entry.lineno > 1_000_000)) return null;
			if (entry.debug_id !== undefined && (typeof entry.debug_id !== "string" || !DEBUG_ID.test(entry.debug_id) || entry.filename === undefined)) return null;
			if (entry.colno !== undefined && (typeof entry.colno !== "number" || !Number.isSafeInteger(entry.colno) || entry.colno < 1 || entry.colno > 10_000_000)) return null;
			frames.push({ function: safeSentryFunctionName(entry.function, "renderer_frame"), ...(entry.filename === undefined ? {} : { filename: entry.filename as string }), ...(entry.lineno === undefined ? {} : { lineno: entry.lineno as number }), ...(entry.colno === undefined ? {} : { colno: entry.colno as number }), ...(entry.debug_id === undefined ? {} : { debug_id: (entry.debug_id as string).toLowerCase() }) });
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
