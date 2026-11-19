import type { ErrorEvent, StackFrame } from "@sentry/react-native";
import { classifyError, type ClassifyInput } from "./observability";
import { DEFAULT_SENTRY_DSN } from "./sentry-config";

const SAFE_CATEGORIES = new Set(["native_crash", "render_crash", "http_5xx", "http_4xx", "offline", "timeout"]);
const INSTALL_ID = /^ins_[0-9a-f-]{36}$/;

let initialized = false;
let permitted = false;
let initPromise: Promise<void> | null = null;
let sentry: typeof import("@sentry/react-native") | null = null;
let pairedInstallId: string | null = null;
let daemonVersion: string | undefined;
let observabilityContext: MobileObservabilityContext = {};
let consentGeneration = 0;
const allowedEventIds = new Set<string>();

function dsn(): string {
	const override = process.env.EXPO_PUBLIC_SENTRY_DSN;
	return override === undefined ? DEFAULT_SENTRY_DSN : override.trim();
}

function environment(channel?: string): string {
	return channel === "production" ? "stable" : channel === "preview" ? "preview" : "development";
}

function safeFrame(frame: StackFrame): StackFrame {
	return {
		function: "mobile_frame",
		lineno: Number.isSafeInteger(frame.lineno) && (frame.lineno ?? 0) > 0 ? frame.lineno : undefined,
		colno: Number.isSafeInteger(frame.colno) && (frame.colno ?? 0) > 0 ? frame.colno : undefined,
	};
}

export function sanitizeMobileSentryEvent(event: ErrorEvent): ErrorEvent | null {
	if (!permitted) return null;
	if (event.tags?.["ao.consent_generation"] !== String(consentGeneration)) return null;
	const category = typeof event.tags?.["ao.category"] === "string" && SAFE_CATEGORIES.has(event.tags["ao.category"])
		? event.tags["ao.category"] : "other";
	const severity = typeof event.tags?.["ao.severity"] === "string" && /^P[0-3]$/.test(event.tags["ao.severity"])
		? event.tags["ao.severity"] : undefined;
	const title = event.message === "Mobile API error" ? "Mobile API error" : "Mobile error";
	const values = event.exception?.values?.map((value) => ({
		type: "MobileError",
		value: "Mobile error",
		stacktrace: value.stacktrace?.frames ? { frames: value.stacktrace.frames.map(safeFrame) } : undefined,
	}));
	const clean: ErrorEvent = {
		type: undefined,
		event_id: event.event_id,
		timestamp: event.timestamp,
		level: event.level,
		platform: "javascript",
		release: observabilityContext.release,
		environment: environment(observabilityContext.channel),
		message: title,
		exception: values?.length ? { values } : undefined,
		tags: {
			"ao.platform": "mobile",
			"ao.category": category,
			...(severity ? { "ao.severity": severity } : {}),
			...(daemonVersion && /^\d+\.\d+\.\d+(?:[.-][A-Za-z0-9.]+)?$/.test(daemonVersion) ? { "ao.daemon_version": daemonVersion } : {}),
		},
		fingerprint: ["mobile", title, category],
		user: pairedInstallId ? { id: pairedInstallId } : undefined,
	};
	if (clean.event_id) allowedEventIds.add(clean.event_id);
	return clean;
}

export type MobileObservabilityContext = { release?: string; channel?: string };

export function setMobileSentryContext(context: MobileObservabilityContext): void {
	observabilityContext = context;
	if (permitted) void initMobileSentry();
}

export function setMobileDaemonVersion(version: string): void {
	daemonVersion = version;
}

export function setMobileSentryPairedInstallId(id: string | null): void {
	pairedInstallId = id && INSTALL_ID.test(id) ? id : null;
	sentry?.setUser(pairedInstallId ? { id: pairedInstallId } : null);
}

export function setMobileSentryConsent(allowed: boolean): void {
	if (permitted !== allowed) {
		consentGeneration++;
		allowedEventIds.clear();
	}
	permitted = allowed;
	if (!allowed) {
		pairedInstallId = null;
		sentry?.setUser(null);
	}
	if (allowed) void initMobileSentry();
}

export function initMobileSentry(): Promise<void> {
	if (initialized || !permitted || process.env.EXPO_PUBLIC_AO_TELEMETRY_DISABLED === "1") return Promise.resolve();
	if (initPromise) return initPromise;
	const configuredDsn = dsn();
	if (!configuredDsn) return Promise.resolve();
	const generation = consentGeneration;
	initPromise = initialize(configuredDsn, generation).finally(() => {
		initPromise = null;
		if (permitted && !initialized && generation !== consentGeneration) void initMobileSentry();
	});
	return initPromise;
}

async function initialize(configuredDsn: string, generation: number): Promise<void> {
	let sdk: typeof import("@sentry/react-native");
	let makeFetchTransport: typeof import("@sentry/browser")["makeFetchTransport"];
	try {
		[{ default: sdk }, { makeFetchTransport }] = await Promise.all([
			import("@sentry/react-native").then((module) => ({ default: module })),
			import("@sentry/browser"),
		]);
	} catch {
		return;
	}
	if (!permitted || generation !== consentGeneration) return;
	sdk.init({
		dsn: configuredDsn,
		release: observabilityContext.release,
		environment: environment(observabilityContext.channel),
		sendDefaultPii: false,
		enableNative: false,
		enableAutoSessionTracking: false,
		enableAutoPerformanceTracing: false,
		enableCaptureFailedRequests: false,
		tracesSampleRate: 0,
		maxBreadcrumbs: 0,
		maxQueueSize: 1,
		// TelemetryManager owns uncaught JS capture so it can attach the consent
		// generation before Sentry's duplicate-event guard sees the exception.
		integrations: (defaults) => defaults.filter((integration) => integration.name !== "ReactNativeErrorHandlers"),
		// The native transport persists offline envelopes without a consent-aware
		// purge. The fetch transport discards failed sends and checks consent again.
		transport: (options) => {
			const guardedFetch: typeof fetch = (...args) =>
				permitted ? fetch(...args) : Promise.reject(new Error("Telemetry disabled"));
			const transport = makeFetchTransport(options, guardedFetch);
			return {
				send: (envelope) => {
					const ids = envelope[1]
						.filter(([header]) => header.type === "event")
						.map(([, payload]) => (payload as { event_id?: unknown }).event_id)
						.filter((id): id is string => typeof id === "string");
					if (!permitted || !ids.length || ids.some((id) => !allowedEventIds.has(id))) return Promise.resolve({});
					for (const id of ids) allowedEventIds.delete(id);
					return transport.send(envelope);
				},
				flush: (timeout) => permitted ? transport.flush(timeout) : Promise.resolve(true),
			};
		},
		beforeSend: sanitizeMobileSentryEvent,
	});
	sentry = sdk;
	initialized = true;
	sdk.setUser(pairedInstallId ? { id: pairedInstallId } : null);
}

type CaptureMeta = ClassifyInput & { operation?: string; surface?: string; domain?: string; requestId?: string };

export function captureMobileException(error: unknown, meta: CaptureMeta = {}): void {
	if (!initialized || !permitted) return;
	const triage = classifyError(meta);
	if (!triage.report) return;
	sentry?.captureException(error, {
		level: triage.level,
		tags: { "ao.category": SAFE_CATEGORIES.has(meta.category ?? "") ? meta.category : "other", "ao.severity": triage.severity, "ao.consent_generation": String(consentGeneration) },
	});
}

export function captureMobileApiError(_path: string, category: string, status?: number, code?: string, _requestId?: string): void {
	if (!initialized || !permitted) return;
	const triage = classifyError({ category, httpStatus: status, code });
	if (!triage.report) return;
	sentry?.captureMessage("Mobile API error", {
		level: triage.level,
		tags: { "ao.category": SAFE_CATEGORIES.has(category) ? category : "other", "ao.severity": triage.severity, "ao.consent_generation": String(consentGeneration) },
	});
}

export function httpCategory(status: number): string {
	return status >= 500 ? "http_5xx" : "http_4xx";
}
