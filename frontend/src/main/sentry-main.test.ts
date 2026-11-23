import { createHash } from "node:crypto";
import { AoError } from "../../../packages/shared/telemetry/ao-error";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mkdtempSync, rmSync } from "node:fs";
import { mkdtemp, mkdir, readdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import type { ElectronMainOptions, ErrorEvent } from "@sentry/electron/main";
import { DEFAULT_ELECTRON_SENTRY_DSN } from "../shared/sentry-config";
import { DesktopTelemetryController } from "./desktop-telemetry-controller";
import { clearMainSentryCache, createErrorBudget, ERROR_BUDGET_FILE, electronMainSentryDsn, initMainSentry, installInErrorSample, sanitizeRendererCapture } from "./sentry-main";

const hashParts = (text: string) => ["sha256", createHash("sha256").update(text).digest("hex").slice(0, 10)];

const sdkTransport = vi.hoisted(() => vi.fn());
const sdk = vi.hoisted(() => ({
	init: vi.fn<(options: ElectronMainOptions) => void>(),
	captureEvent: vi.fn<(event: ErrorEvent) => void>(),
	close: vi.fn().mockResolvedValue(true),
	setUser: vi.fn(),
	clearCurrent: vi.fn(),
	clearIsolation: vi.fn(),
	makeElectronOfflineTransport: vi.fn((_base?: unknown) => sdkTransport),
	makeElectronTransport: vi.fn(),
	onUncaughtExceptionIntegration: vi.fn(() => ({ name: "OnUncaughtException" })),
	onUnhandledRejectionIntegration: vi.fn(() => ({ name: "OnUnhandledRejection" })),
	childProcessIntegration: vi.fn(() => ({ name: "ChildProcess" })),
}));
vi.mock("@sentry/electron/main", () => ({
	...sdk,
	getCurrentScope: () => ({ clear: sdk.clearCurrent }),
	getIsolationScope: () => ({ clear: sdk.clearIsolation }),
}));
// sha256("ins_sampled_5") % 10 == 0: this install is in the 1-in-10 error sample.
const installId = vi.hoisted(() => ({ value: "ins_sampled_5" }));
vi.mock("../shared/telemetry", () => ({ loadOrCreateTelemetryInstallId: vi.fn(async () => installId.value) }));

let dataDir = "";
beforeEach(() => {
	vi.spyOn(Math, "random").mockReturnValue(0);
	vi.stubEnv("AO_PUBLISHED_BUILD", "1");
	dataDir = mkdtempSync(path.join(os.tmpdir(), "ao-sentry-main-"));
});
afterEach(() => {
	vi.unstubAllEnvs(); vi.restoreAllMocks(); vi.clearAllMocks(); sdk.close.mockResolvedValue(true);
	installId.value = "ins_sampled_5";
	rmSync(dataDir, { recursive: true, force: true });
});

describe("Electron main Sentry configuration", () => {
	it("keeps Electron and daemon overrides separate and leaves development empty", () => {
		vi.stubEnv("AO_ELECTRON_SENTRY_DSN", undefined);
		vi.stubEnv("AO_SENTRY_DSN", "https://daemon.example/1");
		expect(electronMainSentryDsn("1.0.0", false)).toBe(DEFAULT_ELECTRON_SENTRY_DSN);
		expect(electronMainSentryDsn("1.0.0", true)).toBe("");
		vi.stubEnv("AO_ELECTRON_SENTRY_DSN", "https://electron.example/2");
		expect(electronMainSentryDsn("1.0.0", false)).toBe("https://electron.example/2");
		expect(electronMainSentryDsn("1.0.0", true)).toBe("https://electron.example/2");
		vi.stubEnv("AO_ELECTRON_SENTRY_DSN", "");
		expect(electronMainSentryDsn("1.0.0", false)).toBe("");
	});

	it("creates an offline client with consent-owned crash integrations and an install user", async () => {
		vi.stubEnv("AO_ELECTRON_SENTRY_DSN", undefined);
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		expect(transport).toBeTruthy();
		expect(sdk.init).toHaveBeenCalledOnce();
		const options = sdk.init.mock.calls[0]?.[0];
		expect(options).toMatchObject({
			dsn: DEFAULT_ELECTRON_SENTRY_DSN,
			transport: sdkTransport,
			transportOptions: { queuePath: "/tmp/ao-electron-test/sentry/queue", shouldSend: expect.any(Function), shouldStore: expect.any(Function) },
			ipcMode: 0,
			defaultIntegrations: [],
			maxBreadcrumbs: 0,
			sendDefaultPii: false,
		});
		expect(Array.isArray(options.integrations) ? options.integrations.map((item) => item.name) : undefined).toEqual(["OnUncaughtException", "OnUnhandledRejection", "ChildProcess"]);
		expect(sdk.childProcessIntegration).toHaveBeenCalledWith({ breadcrumbs: false, events: ["abnormal-exit", "crashed", "oom", "launch-failed", "integrity-failure"] });
		expect(sdk.setUser).toHaveBeenCalledWith({ id: "ins_sampled_5" });
		expect(sdk.makeElectronOfflineTransport).toHaveBeenCalledOnce();
	});

	it("keeps every consented main and renderer error without random sampling", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const options = sdk.init.mock.calls[0]?.[0];
		const beforeSend = options.beforeSend as (event: ErrorEvent) => ErrorEvent | null;
		const random = vi.mocked(Math.random);
		random.mockReturnValue(0.99);
		expect(options.sampleRate).toBe(1);
		expect(beforeSend({ type: undefined, message: "ordinary main error" })).not.toBeNull();
		transport.capture({ consentGeneration: "generation-1", kind: "exception", message: "", level: "error", exception: { type: "TypeError", frames: [] } });
		expect(sdk.captureEvent).toHaveBeenCalledOnce();
		expect(beforeSend(sdk.captureEvent.mock.calls[0]?.[0] as ErrorEvent)).not.toBeNull();
		expect(beforeSend({ type: undefined, level: "fatal", message: "'renderer' process exited with 'crashed'" })).not.toBeNull();
		expect(random).not.toHaveBeenCalled();
	});

	it.each(["query", "mutation", "route", "react_render", "open_orchestrator", "xterm_init", "/private/session"])("forwards only registered renderer operations through final scrubbing: %s", async (operation) => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		transport.capture({ consentGeneration: "generation-1", kind: "exception", message: "private", tags: { operation } });
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent | null;
		const event = beforeSend(sdk.captureEvent.mock.calls[0]?.[0] as ErrorEvent);
		expect(event?.tags?.operation).toBe(operation.startsWith("/") ? undefined : operation);
		expect(JSON.stringify(event)).not.toContain("/private/session");
	});

	it("reports handled main failures with a scrubbed title", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent | null;
		const secret = "/Users/dave/private-project";
		transport.captureMain?.({ category: "daemon", reason: "exited", detail: "signal:SIGKILL", error: new TypeError(`spawn ${secret}`) });
		transport.captureMain?.({ category: "daemon", reason: "exited", detail: `path:${secret}` });
		transport.captureMain?.({ category: "made_up" as never, reason: "exited" });
		expect(sdk.captureEvent).toHaveBeenCalledTimes(2);
		const first = beforeSend(sdk.captureEvent.mock.calls[0]?.[0] as ErrorEvent);
		expect(first).toMatchObject({
			message: "spawn <path>",
			fingerprint: ["electron", "handled", "daemon", "exited", "electron-main", ...hashParts("spawn <path>")],
			tags: { surface: "electron-main", kind: "handled", category: "daemon", reason: "exited", detail: "signal:SIGKILL" },
			exception: { values: [{ type: "TypeError", value: "spawn <path> (in electron-main, daemon/exited)" }] },
		});
		const second = beforeSend(sdk.captureEvent.mock.calls[1]?.[0] as ErrorEvent);
		expect(second?.tags).not.toHaveProperty("detail");
		expect(JSON.stringify([first, second])).not.toContain(secret);
	});

	it("scrubs side-chat recovery faults before the SDK sends them", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent | null;
		transport.captureMain?.({ category: "side_chat", reason: "recovery", error: new TypeError("Could not restore session-123 from /Users/private token=secret"), detail: "private-project" });
		const event = beforeSend(sdk.captureEvent.mock.calls[0][0]);
		expect(event).toMatchObject({ message: "Could not restore <id> from <path> <credential>", tags: { category: "side_chat", reason: "recovery" }, exception: { values: [{ type: "TypeError", value: "Could not restore <id> from <path> <credential> (in electron-main, side_chat/recovery)" }] } });
		expect(JSON.stringify(event)).not.toMatch(/private|Users|secret/);
	});

	it("matches daemon Sentry environments for stable, nightly, and development versions", async () => {
		vi.stubEnv("AO_TELEMETRY_APP_VERSION", undefined);
		vi.stubEnv("AO_ELECTRON_SENTRY_DSN", undefined);
		for (const [version, environment] of [["1.0.0", "stable"], ["1.0.1-nightly.202610291200", "nightly"]]) {
			await initMainSentry(version, dataDir, "/tmp/ao-electron-test");
			expect(sdk.init.mock.calls.at(-1)?.[0].environment).toBe(environment);
		}
		vi.stubEnv("AO_TELEMETRY_APP_VERSION", "1.0.1-nightly.202610291200");
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		expect(sdk.init.mock.calls.at(-1)?.[0]).toMatchObject({ release: "1.0.0", environment: "nightly" });
		// Only an explicit DSN override initializes Sentry for any other version.
		vi.stubEnv("AO_TELEMETRY_APP_VERSION", undefined);
		vi.stubEnv("AO_ELECTRON_SENTRY_DSN", "https://electron.example/2");
		for (const [version, environment] of [["1.0.1-edge.1", "development"], ["1.0.1-pr7386.1", "development"], ["", "unknown"]]) {
			await initMainSentry(version, dataDir, "/tmp/ao-electron-test");
			expect(sdk.init.mock.calls.at(-1)?.[0].environment).toBe(environment);
		}
	});

	it("does not initialize Sentry for a self-built or development copy", async () => {
		vi.stubEnv("AO_ELECTRON_SENTRY_DSN", undefined);
		vi.stubEnv("AO_TELEMETRY_APP_VERSION", undefined);
		for (const version of ["0.13.0-ragcorp.7", "0.14.10+ptbr.1", "development"]) await initMainSentry(version, dataDir, "/tmp/ao-electron-test");
		vi.stubEnv("AO_PUBLISHED_BUILD", "");
		await initMainSentry("0.13.0", dataDir, "/tmp/ao-electron-test");
		expect(sdk.init).not.toHaveBeenCalled();
	});

	it("forwards a safe renderer event and drains before clearing SDK scopes", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const request = { consentGeneration: "generation-1", kind: "exception", message: "get project ai-sports-coach-113 failed", level: "error", tags: { category: "http_5xx", code: "WORKSPACE_GIT_READ_FAILED" } } as const;
		transport.capture(request);
		expect(sdk.captureEvent).toHaveBeenCalledOnce();
		const beforeSend = sdk.init.mock.calls[0][0].beforeSend!;
		const event = await beforeSend(sdk.captureEvent.mock.calls[0][0], {});
		expect(event).toMatchObject({ message: "Renderer Error in unknown (WORKSPACE_GIT_READ_FAILED)", tags: { surface: "renderer", category: "http_5xx", code: "WORKSPACE_GIT_READ_FAILED" } });
		expect(JSON.stringify(event)).not.toContain("ai-sports-coach-113");
		transport.capture({ ...request, kind: "breadcrumb" });
		transport.capture({ ...request, tags: { category: "daemon_unavailable" } });
		expect(sdk.captureEvent).toHaveBeenCalledOnce();
		await transport.closeAndDrain();
		expect(sdk.close).toHaveBeenCalledWith(2_000);
		transport.capture(request);
		expect(sdk.captureEvent).toHaveBeenCalledOnce();
		await transport.clearCache();
		expect(sdk.clearCurrent).toHaveBeenCalledOnce();
		expect(sdk.clearIsolation).toHaveBeenCalledOnce();
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		expect(Array.isArray(sdk.init.mock.calls[1]?.[0].integrations) ? sdk.init.mock.calls[1]?.[0].integrations.map((item) => item.name) : undefined).toEqual(["OnUncaughtException", "OnUnhandledRejection"]);
	});

	it("removes SDK process crash listeners on opt-out before a later opt-in", async () => {
		const initialUncaught = process.listenerCount("uncaughtException");
		const initialRejections = process.listenerCount("unhandledRejection");
		const addSdkListeners = () => {
			process.on("uncaughtException", () => undefined);
			process.on("unhandledRejection", () => undefined);
		};
		sdk.init.mockImplementationOnce(addSdkListeners).mockImplementationOnce(addSdkListeners);
		const first = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		expect(process.listenerCount("uncaughtException")).toBe(initialUncaught + 1);
		expect(process.listenerCount("unhandledRejection")).toBe(initialRejections + 1);
		await first.closeAndDrain();
		expect(process.listenerCount("uncaughtException")).toBe(initialUncaught);
		expect(process.listenerCount("unhandledRejection")).toBe(initialRejections);
		const second = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		expect(process.listenerCount("uncaughtException")).toBe(initialUncaught + 1);
		expect(process.listenerCount("unhandledRejection")).toBe(initialRejections + 1);
		await second.closeAndDrain();
		expect(process.listenerCount("uncaughtException")).toBe(initialUncaught);
		expect(process.listenerCount("unhandledRejection")).toBe(initialRejections);
	});

	it("deletes the offline queue under the supplied cache root after drain", async () => {
		const cacheRoot = await mkdtemp(path.join(os.tmpdir(), "ao-sentry-cache-"));
		try {
			const queuePath = path.join(cacheRoot, "sentry", "queue");
			await mkdir(queuePath, { recursive: true });
			await writeFile(path.join(queuePath, "queued-event"), "synthetic queued event");
			const transport = await initMainSentry("1.0.0", dataDir, cacheRoot);
			expect(sdk.init.mock.calls[0]?.[0].transportOptions).toMatchObject({ queuePath });
			await transport.closeAndDrain();
			await transport.clearCache();
			expect(await readdir(path.join(cacheRoot, "sentry"))).toEqual([]);
		} finally {
			await rm(cacheRoot, { recursive: true, force: true });
		}
	});

	it("blocks offline retries after close and waits for a retry already sending", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		let finishSend!: (response: { statusCode: number }) => void;
		const pendingSend = new Promise<{ statusCode: number }>((resolve) => { finishSend = resolve; });
		const baseSend = vi.fn(() => pendingSend);
		sdk.makeElectronTransport.mockReturnValue({ send: baseSend, flush: vi.fn().mockResolvedValue(true) });
		const baseFactory = sdk.makeElectronOfflineTransport.mock.calls[0]?.[0] as (options: never) => { send: (envelope: never) => Promise<unknown> };
		const guarded = baseFactory({} as never);
		const sending = guarded.send([] as never);
		let closed = false;
		const closing = transport.closeAndDrain().then(() => { closed = true; });
		await Promise.resolve();
		expect(closed).toBe(false);
		const options = sdk.init.mock.calls[0]?.[0].transportOptions as { shouldSend: () => boolean; shouldStore: () => boolean };
		expect(options.shouldSend()).toBe(false);
		expect(options.shouldStore()).toBe(false);
		finishSend({ statusCode: 200 });
		await Promise.all([sending, closing]);
		expect(closed).toBe(true);
		await transport.clearCache();
		await expect(guarded.send([] as never)).rejects.toThrow("Electron Sentry sender is closed");
		expect(baseSend).toHaveBeenCalledOnce();
	});

	it("purges an offline event left behind by an interrupted opt-out before a later opt-in", async () => {
		const cacheRoot = await mkdtemp(path.join(os.tmpdir(), "ao-sentry-restart-"));
		try {
			const queuePath = path.join(cacheRoot, "sentry", "queue");
			await mkdir(queuePath, { recursive: true });
			await writeFile(path.join(queuePath, "queued-before-opt-out"), "synthetic queued event");
			let snapshot = { eventsEnabled: false, consentGeneration: "generation-off", updatedAt: "2026-10-29T00:00:00.000Z", acknowledged: true };
			const authority = {
				durabilitySupported: true,
				load: async () => snapshot,
				snapshot: () => snapshot,
				setEventsEnabled: async (eventsEnabled: boolean) => {
					snapshot = { ...snapshot, eventsEnabled, consentGeneration: "generation-on" };
					return snapshot;
				},
				retryPendingReplacement: async () => snapshot,
			};
			const transportFactory = vi.fn(async () => {
				expect(await readdir(path.join(cacheRoot, "sentry"))).toEqual([]);
				return initMainSentry("1.0.0", dataDir, cacheRoot);
			});
			const controller = new DesktopTelemetryController({ authority, transportFactory, clearTransportCache: () => clearMainSentryCache(cacheRoot), environmentAllowsEvents: true, productionEnabled: true });
			await controller.initialize();
			expect(controller.snapshot()).toMatchObject({ eventsEnabled: false, acknowledged: true, state: "applied" });
			expect(await readdir(path.join(cacheRoot, "sentry"))).toEqual([]);
			expect(sdk.init).not.toHaveBeenCalled();
			await controller.setEventsEnabled(true, "generation-off");
			expect(transportFactory).toHaveBeenCalledOnce();
			await controller.close();
		} finally {
			await rm(cacheRoot, { recursive: true, force: true });
		}
	});

	it("starts, purges, and restarts the sender across consent generations", async () => {
		const cacheRoot = await mkdtemp(path.join(os.tmpdir(), "ao-sentry-consent-"));
		try {
			let generation = 0;
			let snapshot = { eventsEnabled: false, consentGeneration: "generation-0", updatedAt: "2026-10-29T00:00:00.000Z", acknowledged: true };
			const authority = {
				durabilitySupported: true,
				load: async () => snapshot,
				snapshot: () => snapshot,
				setEventsEnabled: async (eventsEnabled: boolean) => {
					generation++;
					snapshot = { ...snapshot, eventsEnabled, consentGeneration: `generation-${generation}` };
					return snapshot;
				},
				retryPendingReplacement: async () => snapshot,
			};
			const controller = new DesktopTelemetryController({ authority, transportFactory: () => initMainSentry("1.0.0", dataDir, cacheRoot), environmentAllowsEvents: true, productionEnabled: true });
			await controller.initialize();
			expect(sdk.init).not.toHaveBeenCalled();
			await controller.setEventsEnabled(true, "generation-0");
			expect(sdk.init).toHaveBeenCalledOnce();
			expect(controller.capture({ consentGeneration: "generation-1", kind: "exception", message: "first" })).toBe(true);
			const queuePath = path.join(cacheRoot, "sentry", "queue");
			await mkdir(queuePath, { recursive: true });
			await writeFile(path.join(queuePath, "queued-event"), "synthetic queued event");
			await controller.setEventsEnabled(false, "generation-1");
			expect(sdk.close).toHaveBeenCalledOnce();
			expect(await readdir(path.join(cacheRoot, "sentry"))).toEqual([]);
			expect(controller.capture({ consentGeneration: "generation-2", kind: "exception", message: "blocked" })).toBe(false);
			await controller.setEventsEnabled(true, "generation-2");
			expect(sdk.init).toHaveBeenCalledTimes(2);
			expect(controller.capture({ consentGeneration: "generation-3", kind: "exception", message: "second" })).toBe(true);
			await controller.close();
		} finally {
			await rm(cacheRoot, { recursive: true, force: true });
		}
	});

	it("drops dynamic exception text, paths, account fields, and breadcrumbs before send", async () => {
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		const result = beforeSend({
			type: undefined,
			message: "resume agent ai-sports-coach-113",
			server_name: "private-host",
			request: { url: "file:///Users/alice/private-repo" },
			user: { id: "spoofed", email: "private@example.test" },
			breadcrumbs: [{ message: "private branch" }],
			tags: { "ao.origin": "renderer", "ao.kind": "exception", "ao.category": "http_5xx", "ao.code": "WORKSPACE_GIT_READ_FAILED", branch: "private" },
			exception: { values: [{ type: "Error", value: "get project ai-sports-coach-113", stacktrace: { frames: [{ function: "ChatWorkspace", filename: "/Users/alice/private-repo/file.ts", context_line: "private" }] } }] },
		});
		expect(result).toMatchObject({ message: "Renderer Error in ChatWorkspace (WORKSPACE_GIT_READ_FAILED)", user: { id: "ins_sampled_5" }, fingerprint: ["electron", "exception", "WORKSPACE_GIT_READ_FAILED", "Error", "ChatWorkspace", "renderer", "code", "WORKSPACE_GIT_READ_FAILED"] });
		const encoded = JSON.stringify(result);
		for (const secret of ["ai-sports-coach-113", "private-repo", "private branch", "private@example.test", "private-host", "file:///"]) expect(encoded).not.toContain(secret);
		expect(result.exception?.values?.[0]?.stacktrace?.frames?.[0]).toEqual({ function: "ChatWorkspace", in_app: true });
	});

	it("groups renderer exceptions by type, top frame, and normalized message", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		const capture = (type: string, functionName: string, message: string) => {
			transport.capture({ consentGeneration: "generation-1", kind: "exception", message, exception: { type, frames: [{ function: functionName, filename: "assets/app-A1B2C3D4.js", lineno: 42 }] } });
			return beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent);
		};
		const first = capture("TypeError", "ChatWorkspace", "resume agent ai-sports-coach-113");
		const same = capture("TypeError", "ChatWorkspace", "resume agent sample-123");
		const otherMessage = capture("TypeError", "ChatWorkspace", "get project sample-123");
		const otherType = capture("RangeError", "ChatWorkspace", "resume agent sample-123");
		const otherFrame = capture("TypeError", "SessionView", "resume agent sample-123");
		expect(first.fingerprint).toEqual(["electron", "exception", "unknown", "TypeError", "ChatWorkspace", "renderer", ...hashParts("resume agent <id>")]);
		expect(same.fingerprint).toEqual(first.fingerprint);
		expect(otherMessage.fingerprint).not.toEqual(first.fingerprint);
		expect(otherType.fingerprint).not.toEqual(first.fingerprint);
		expect(otherFrame.fingerprint).not.toEqual(first.fingerprint);
		expect(first.exception?.values?.[0]?.stacktrace?.frames).toEqual([{ function: "ChatWorkspace", filename: "assets/app-A1B2C3D4.js", abs_path: "app://renderer/assets/app-A1B2C3D4.js", lineno: 42, in_app: true }]);
		expect(JSON.stringify(first)).not.toMatch(/ai-sports-coach-113|private-repo/);
	});

	it("groups minified renderer functions by app chunk, line, and column while retaining source map filenames", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		const capture = (filename: string, lineno: number, colno: number, functionName: string) => {
			transport.capture({ consentGeneration: "generation-1", kind: "exception", message: "", exception: { type: "TypeError", frames: [{ function: functionName, filename, lineno, colno }] } });
			return beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent);
		};
		const first = capture("assets/_shell-A1B2C3D4.js", 42, 7, "a");
		const same = capture("assets/_shell-Z9Y8X7W6.js", 42, 7, "b");
		const otherColumn = capture("assets/_shell-A1B2C3D4.js", 42, 138_000, "a");
		expect(first.message).toBe("Renderer TypeError in assets/_shell:42:7 (unknown)");
		expect(first.fingerprint).toEqual(same.fingerprint);
		expect(otherColumn.fingerprint).not.toEqual(first.fingerprint);
		expect(first.exception?.values?.[0]?.stacktrace?.frames).toEqual([{ function: "renderer_frame", filename: "assets/_shell-A1B2C3D4.js", abs_path: "app://renderer/assets/_shell-A1B2C3D4.js", lineno: 42, colno: 7, in_app: true }]);
	});

	it("separates dev renderer sites by line when no packaged asset name exists", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		const capture = (functionName: string, lineno: number) => {
			transport.capture({ consentGeneration: "generation-1", kind: "exception", message: "", exception: { type: "TypeError", frames: [{ function: functionName, lineno }] } });
			return beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent);
		};
		const first = capture("openSessionPanel", 3);
		expect(capture("otherUnknownFunction", 3).fingerprint).toEqual(first.fingerprint);
		expect(capture("renderUsageChart", 6).fingerprint).not.toEqual(first.fingerprint);
		expect(first.message).toBe("Renderer TypeError in renderer:3 (unknown)");
		expect(JSON.stringify(first)).not.toContain("openSessionPanel");
	});

	it("titles renderer API failures with any well-formed code", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		transport.capture({ consentGeneration: "generation-1", kind: "message", message: "private session", tags: { category: "http_5xx", code: "NEW_API_FAILURE", operation: "new_api_operation" } });
		const event = beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent);
		expect(event.message).toBe("Renderer API error in new_api_operation (NEW_API_FAILURE)");
		expect(event.tags).toMatchObject({ code: "NEW_API_FAILURE", category: "http_5xx", operation: "new_api_operation" });
		expect(JSON.stringify(event)).not.toContain("private session");
	});

	it("titles and groups a failed onboarding action by its action and reason alone", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		transport.capture({ consentGeneration: "generation-1", kind: "message", message: "install /Users/alice/private-repo", level: "error", tags: { category: "onboarding", code: "gh_install:install_failed", target: "gh", flow: "onboarding", severity: "P1", owner: "ours" } });
		expect(sdk.captureEvent.mock.calls.at(-1)?.[0].tags).toMatchObject({ "ao.category": "onboarding", "ao.code": "gh_install:install_failed", "ao.target": "gh", "ao.flow": "onboarding" });
		const event = beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent);
		expect(event.message).toBe("Onboarding gh_install failed (install_failed)");
		// The target is a tag to filter by, not part of the grouping.
		expect(event.fingerprint).toEqual(["electron", "onboarding", "gh_install", "install_failed", "renderer"]);
		expect(event.tags).toMatchObject({ surface: "renderer", kind: "message", category: "onboarding", code: "gh_install:install_failed", action: "gh_install", reason: "install_failed", target: "gh", flow: "onboarding", sample_rate: "1" });
		expect(JSON.stringify(event)).not.toMatch(/private-repo|alice/);
	});

	it("tags an onboarding failure's target only from the known list", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		transport.capture({ consentGeneration: "generation-1", kind: "message", message: "safe", tags: { category: "onboarding", code: "agent_sign_in:start_failed", target: "claude-code" } });
		expect(beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent).tags).toMatchObject({ action: "agent_sign_in", target: "claude-code" });
		transport.capture({ consentGeneration: "generation-1", kind: "message", message: "safe", tags: { category: "onboarding", code: "agent_install:install_failed", target: "private-agent" } });
		expect(sdk.captureEvent.mock.calls.at(-1)?.[0].tags).toMatchObject({ "ao.target": "other" });
		expect(beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent).tags).toMatchObject({ action: "agent_install", target: "other" });
		// Other renderer events don't carry it.
		transport.capture({ consentGeneration: "generation-1", kind: "message", message: "safe", tags: { category: "http_5xx", code: "CHAT_DELIVERY_FAILED", target: "codex" } });
		expect(sdk.captureEvent.mock.calls.at(-1)?.[0].tags).not.toHaveProperty("ao.target");
		expect(beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent).tags).not.toHaveProperty("target");
	});

	it("drops onboarding codes outside the closed action and reason lists", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		for (const code of ["gh_install:private-repo", "private:install_failed", "agent_sign_in:not_signed_in", "gh_install:install_failed:extra", "WORKSPACE_GIT_READ_FAILED"]) {
			transport.capture({ consentGeneration: "generation-1", kind: "message", message: "safe", tags: { category: "onboarding", code } });
			expect(sdk.captureEvent.mock.calls.at(-1)?.[0].tags).toMatchObject({ "ao.category": "onboarding", "ao.code": "unknown" });
		}
		// They share one fingerprint, so scrub one: the per-fingerprint budget allows three a day.
		const event = beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent);
		expect(event.message).toBe("Renderer API error (onboarding)");
		expect(event.tags).toMatchObject({ code: "unknown" });
		expect(event.tags).not.toHaveProperty("action");
		// An onboarding code is not a code for any other category.
		transport.capture({ consentGeneration: "generation-1", kind: "message", message: "safe", tags: { category: "http_5xx", code: "gh_install:install_failed" } });
		expect(sdk.captureEvent.mock.calls.at(-1)?.[0].tags).toMatchObject({ "ao.code": "unknown" });
	});

	it("keeps the onboarding flow tag on other renderer errors without changing their grouping", async () => {
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		const request = { consentGeneration: "generation-1", kind: "message", message: "private session", tags: { category: "http_5xx", code: "CHAT_DELIVERY_FAILED" } } as const;
		transport.capture(request);
		const plain = beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent);
		transport.capture({ ...request, tags: { ...request.tags, flow: "onboarding" } });
		const tagged = beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent);
		expect(tagged.tags).toMatchObject({ flow: "onboarding", code: "CHAT_DELIVERY_FAILED" });
		expect(tagged.fingerprint).toEqual(plain.fingerprint);
		expect(plain.tags).not.toHaveProperty("flow");
		transport.capture({ ...request, tags: { ...request.tags, flow: "private-flow" } });
		expect(sdk.captureEvent.mock.calls.at(-1)?.[0].tags).not.toHaveProperty("ao.flow");
	});

	it("groups Electron main exceptions by safe type and top in-app frame", async () => {
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		const mainError = (type: string, top: string, message: string) => beforeSend({
			type: undefined, message,
			exception: { values: [{ type, value: message, stacktrace: { frames: [{ function: "startup", in_app: false }, { function: top, filename: "/Users/alice/private-repo/main.ts", in_app: true }] } }] },
		});
		const first = mainError("TypeError", "startDaemon", "resume agent ai-sports-coach-113");
		expect(mainError("TypeError", "startDaemon", "resume agent project-16").fingerprint).toEqual(first.fingerprint);
		expect(mainError("RangeError", "startDaemon", "other").fingerprint).not.toEqual(first.fingerprint);
		expect(mainError("TypeError", "initMainSentry", "other").fingerprint).not.toEqual(first.fingerprint);
		expect(first.fingerprint).toEqual(["electron", "main_error", "unknown", "TypeError", "startDaemon", "electron-main", ...hashParts("resume agent <id>")]);
		expect(JSON.stringify(first)).not.toMatch(/ai-sports-coach-113|private-repo|\/Users\/alice/);
	});

	it("separates minified main failures by numeric bundle location without sending paths", async () => {
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent;
		const mainError = (colno: number, functionName: string) => beforeSend({
			type: undefined,
			exception: { values: [{ type: "TypeError", stacktrace: { frames: [{ function: functionName, filename: "/Users/alice/private-repo/main.js", lineno: 1, colno, in_app: true }] } }] },
		});
		const first = mainError(321, "a");
		expect(mainError(321, "b").fingerprint).toEqual(first.fingerprint);
		expect(mainError(322, "a").fingerprint).not.toEqual(first.fingerprint);
		expect(first.message).toBe("Electron main TypeError in main:1:321");
		expect(first.exception?.values?.[0]?.stacktrace?.frames).toEqual([{ function: "main_frame", lineno: 1, colno: 321, in_app: true }]);
		expect(JSON.stringify(first)).not.toContain("private-repo");
	});

	it("drops expected noise and keeps process-failure and uncaught labels", async () => {
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent | null;
		for (const message of ["AbortError", "net::ERR_NETWORK_CHANGED", "net::ERR_INTERNET_DISCONNECTED"]) {
			expect(beforeSend({ type: undefined, message })).toBeNull();
		}
		expect(beforeSend({ type: undefined, message: "Updater failed: net::ERR_NETWORK_CHANGED" })).not.toBeNull();
		const mainError = (message: string) => beforeSend({
			type: undefined, message,
				exception: { values: [{ type: "Error", value: message, mechanism: { type: "auto.node.onunhandledrejection" } }] },
		});
		expect(mainError("resume agent ai-sports-coach-113")?.fingerprint).toEqual(["electron", "unhandled_rejection", "unknown", "Error", "unknown", "electron-main", ...hashParts("resume agent <id>")]);
		expect(mainError("get project private-repo")?.fingerprint).not.toEqual(mainError("resume agent ai-sports-coach-113")?.fingerprint);
		expect(beforeSend({ type: undefined, message: "'renderer' process exited with 'crashed'" })?.fingerprint).toEqual(["electron", "process_exit", "crashed", "Error", "unknown", "electron-main"]);
		expect(beforeSend({ type: undefined, level: "fatal", exception: { values: [{ type: "TypeError", mechanism: { type: "generic" } }] } })?.fingerprint).toEqual(["electron", "uncaught_exception", "unknown", "TypeError", "unknown", "electron-main"]);
	});

	it("does not start a client when the environment explicitly sets a blank DSN", async () => {
		vi.stubEnv("AO_ELECTRON_SENTRY_DSN", "");
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		transport.capture({ consentGeneration: "generation-1", kind: "message", message: "error" });
		expect(sdk.init).not.toHaveBeenCalled();
		expect(sdk.captureEvent).not.toHaveBeenCalled();
	});

	it("does not send from development builds without an explicit Electron override", async () => {
		vi.stubEnv("AO_ELECTRON_SENTRY_DSN", undefined);
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test", true);
		expect(sdk.init).not.toHaveBeenCalled();
		vi.stubEnv("AO_ELECTRON_SENTRY_DSN", "https://electron.example/2");
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test", true);
		expect(sdk.init.mock.calls[0]?.[0].dsn).toBe("https://electron.example/2");
	});
});

describe("sanitizeRendererCapture", () => {
	it("bounds message text and scrubs local paths in tags at IPC intake", () => {
		const input = {
			consentGeneration: "generation-1",
			kind: "exception",
			level: "error",
			message: "failed /Users/alice/project/file.ts at http://127.0.0.1:3001/api and C:\\Users\\alice\\repo\\file.ts",
			tags: {
				operation: "GET http://localhost:3001/api/v1/sessions",
				category: "file:///home/alice/private.txt",
			},
		} as const;

		const sanitized = sanitizeRendererCapture(input);

		expect(sanitized).toEqual({
			consentGeneration: "generation-1",
			kind: "exception",
			level: "error",
			message: input.message,
			tags: {
				operation: "GET [redacted-url]",
				category: "[redacted-url]",
			},
		});
		expect(input.message).toContain("/Users/alice");
	});

	it("rejects unknown fields, tags, kinds, and levels", () => {
		const base = { consentGeneration: "generation-1", kind: "message", message: "safe" };
		expect(sanitizeRendererCapture({ ...base, rawError: "secret" })).toBeNull();
		expect(sanitizeRendererCapture({ ...base, tags: { request_id: "identifier" } })).toBeNull();
		expect(sanitizeRendererCapture({ ...base, kind: "trace" })).toBeNull();
		expect(sanitizeRendererCapture({ ...base, level: "debug" })).toBeNull();
		expect(sanitizeRendererCapture({ ...base, kind: "exception", exception: { type: "ProjectError", frames: [] } })).toBeNull();
		expect(sanitizeRendererCapture({ ...base, kind: "exception", exception: { type: "TypeError", frames: [{ function: "loadProject", filename: "/Users/alice/private-repo" }] } })?.exception?.frames).toEqual([]);
		expect(sanitizeRendererCapture({ ...base, kind: "exception", exception: { type: "TypeError", frames: [{ function: "loadProject", filename: "assets/../private.js" }] } })?.exception?.frames).toEqual([]);
		expect(sanitizeRendererCapture({ ...base, kind: "exception", exception: { type: "TypeError", frames: [{ function: "<anonymous>", lineno: 5 }] } })?.exception?.frames).toEqual([{ function: "renderer_frame", lineno: 5 }]);
		for (const colno of [0, -1, 10_000_001, 1.5, "private", null]) {
			expect(sanitizeRendererCapture({ ...base, kind: "exception", exception: { type: "TypeError", frames: [{ function: "privateProject", colno }] } })?.exception?.frames).toEqual([]);
		}
		expect(sanitizeRendererCapture({ ...base, kind: "exception", exception: { type: "TypeError", frames: [{ function: "privateProject", filename: "assets/app-A1B2C3D4.js", lineno: 5, colno: 138_000 }] } })?.exception?.frames).toEqual([{ function: "renderer_frame", filename: "assets/app-A1B2C3D4.js", lineno: 5, colno: 138_000 }]);
	});
});

describe("Electron main error sampling and budget", () => {
	type BeforeSend = (event: ErrorEvent) => ErrorEvent | null;
	const beforeSendOf = (call = -1) => sdk.init.mock.calls.at(call)?.[0].beforeSend as BeforeSend;
	const mainError = (fn: string): ErrorEvent => ({ type: undefined, exception: { values: [{ type: "TypeError", stacktrace: { frames: [{ function: fn === "main" ? "startDaemon" : "initMainSentry", lineno: 1 }] } }] } });
	const crash = (): ErrorEvent => ({ type: undefined, level: "fatal", message: "'renderer' process exited with 'crashed'" });

	it("samples non-fatal errors by install, sends crashes from every install, and tags the rate", async () => {
		// Pinned against sentryobs.InstallInErrorSample in the daemon.
		expect(installInErrorSample("ins_sampled_5")).toBe(true);
		expect(installInErrorSample("ins_unsampled_0")).toBe(false);
		expect(installInErrorSample("")).toBe(false);
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		expect(beforeSendOf()(mainError("main"))?.tags).toMatchObject({ sample_rate: "0.1" });
		installId.value = "ins_unsampled_0";
		const unsampledDir = mkdtempSync(path.join(os.tmpdir(), "ao-sentry-main-"));
		try {
			await initMainSentry("1.0.0", unsampledDir, "/tmp/ao-electron-test");
			const beforeSend = beforeSendOf();
			expect(beforeSend(mainError("main"))).toBeNull();
			const uncaught: ErrorEvent = { type: undefined, level: "fatal", exception: { values: [{ type: "Error", mechanism: { type: "generic", handled: false } }] } };
			expect(beforeSend(uncaught)?.tags).toMatchObject({ kind: "uncaught_exception", sample_rate: "1" });
			expect(beforeSend(crash())?.tags).toMatchObject({ kind: "process_exit", sample_rate: "1" });
		} finally {
			rmSync(unsampledDir, { recursive: true, force: true });
		}
	});

	it("sends failed onboarding actions from every install, within the daily caps", async () => {
		installId.value = "ins_unsampled_0";
		const unsampledDir = mkdtempSync(path.join(os.tmpdir(), "ao-sentry-main-"));
		try {
			const transport = await initMainSentry("1.0.0", unsampledDir, "/tmp/ao-electron-test");
			const beforeSend = beforeSendOf();
			const failure = { consentGeneration: "generation-1", kind: "message", message: "safe", tags: { category: "onboarding", code: "agent_install:install_failed", target: "codex" } } as const;
			for (let sent = 0; sent < 3; sent++) {
				transport.capture(failure);
				expect(beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent)?.tags).toMatchObject({ action: "agent_install", sample_rate: "1" });
			}
			// The fourth of the day is over the per-fingerprint cap.
			transport.capture(failure);
			expect(beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent)).toBeNull();
			// A forged onboarding code is an ordinary event, so this install still samples it out.
			transport.capture({ ...failure, tags: { category: "onboarding", code: "private:install_failed" } });
			expect(beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent)).toBeNull();
		} finally {
			rmSync(unsampledDir, { recursive: true, force: true });
		}
	});

	it("titles an anonymous renderer frame by bundle file and line and keeps its debug ID", async () => {
		const debugId = "8a1f4c2e-1b3d-4e5f-9a6b-7c8d9e0f1a2b";
		const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		transport.capture({ consentGeneration: "generation-1", kind: "exception", message: "", level: "error", exception: { type: "TypeError", frames: [{ function: "<anonymous>", filename: "/home/alice/private.js" }, { function: "Object.a [as then]", filename: "assets/useSessionWorkspaceFiles-BjAotn5p.js", lineno: 123, colno: 45, debug_id: debugId }] } });
		const sent = sdk.captureEvent.mock.calls[0]?.[0] as ErrorEvent;
		expect(sent.debug_meta?.images).toEqual([{ type: "sourcemap", code_file: "app://renderer/assets/useSessionWorkspaceFiles-BjAotn5p.js", debug_id: debugId }]);
		const event = beforeSendOf()(sent);
		expect(event?.message).toBe("Renderer TypeError in assets/useSessionWorkspaceFiles:123:45 (unknown)");
		// Sentry applies source maps only to javascript or node events.
		expect(event?.platform).toBe("javascript");
		expect(event?.exception?.values?.[0]?.value).toBe("Renderer TypeError in assets/useSessionWorkspaceFiles:123:45 (unknown)");
		expect(event?.exception?.values?.[0]?.stacktrace?.frames?.[0]).toMatchObject({ abs_path: "app://renderer/assets/useSessionWorkspaceFiles-BjAotn5p.js", lineno: 123, colno: 45 });
		expect(event?.debug_meta?.images).toEqual([{ type: "sourcemap", code_file: "app://renderer/assets/useSessionWorkspaceFiles-BjAotn5p.js", debug_id: debugId }]);
		expect(sanitizeRendererCapture({ consentGeneration: "g", kind: "exception", message: "m", exception: { type: "Error", frames: [{ function: "renderer_frame", filename: "assets/x-BjAotn5p.js", debug_id: "not-a-uuid" }] } })?.exception?.frames).toEqual([{ function: "renderer_frame", filename: "assets/x-BjAotn5p.js" }]);
	});

	it("maps main bundle frames and debug images to app URLs without install paths", async () => {
		const debugId = "0a1f4c2e-1b3d-4e5f-9a6b-7c8d9e0f1a2b";
		const bundle = "/Users/alice/Applications/Orchestrator.app/Contents/Resources/app.asar/.vite/build/main.js";
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		const event = beforeSendOf()({
			type: undefined,
			exception: { values: [{ type: "TypeError", value: "private", stacktrace: { frames: [{ function: "startDaemon", filename: bundle, lineno: 10, colno: 2 }, { function: "x", filename: "/Users/alice/other.js", lineno: 1 }] } }] },
			debug_meta: { images: [{ type: "sourcemap", code_file: bundle, debug_id: debugId }, { type: "sourcemap", code_file: "/Users/alice/other.js", debug_id: debugId }] },
		});
		expect(event?.exception?.values?.[0]?.stacktrace?.frames?.[0]).toMatchObject({ function: "startDaemon", abs_path: "app:///.vite/build/main.js" });
		expect(event?.debug_meta?.images).toEqual([{ type: "sourcemap", code_file: "app:///.vite/build/main.js", debug_id: debugId }]);
		expect(event?.platform).toBe("node");
		const handled = beforeSendOf()({ type: undefined, platform: "node", exception: { values: [{ type: "Error" }] }, tags: { "ao.origin": "main_handled", "ao.category": "daemon", "ao.reason": "spawn_failed" } });
		expect(handled).toMatchObject({ message: "Electron main daemon failure (spawn_failed)", platform: "node" });
		expect(JSON.stringify(event)).not.toMatch(/alice|Applications/);
	});

	it("caps each final fingerprint at three events per UTC day", () => {
		const budget = createErrorBudget(path.join(dataDir, ERROR_BUDGET_FILE));
		const day = Date.UTC(2026, 10, 10, 12);
		for (let i = 0; i < 3; i++) expect(budget(["electron", "a"], day)).toBe(true);
		expect(budget(["electron", "a"], day)).toBe(false);
		expect(budget(["electron", "b"], day)).toBe(true);
		expect(budget(["electron", "a"], Date.UTC(2026, 10, 11, 0))).toBe(true);
	});

	it("caps an install at 25 events per UTC day across fingerprints", () => {
		const budget = createErrorBudget(path.join(dataDir, ERROR_BUDGET_FILE));
		const day = Date.UTC(2026, 10, 10, 12);
		for (let i = 0; i < 25; i++) expect(budget(["electron", String(i)], day)).toBe(true);
		expect(budget(["electron", "new"], day)).toBe(false);
	});

	it("keeps the budget across a restart and caps on the scrubbed fingerprint", async () => {
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		// Different raw text, same scrubbed fingerprint.
		const sent = ["failed 1", "failed 2", "failed 3", "failed 4"].map((message) => beforeSendOf()({ ...mainError("main"), message }));
		expect(sent.map((event) => event !== null)).toEqual([true, true, true, false]);
		// A fresh init, as after a reload or restart, reads the persisted counters.
		await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
		expect(beforeSendOf()({ ...mainError("main"), message: "failed 5" })).toBeNull();
		expect(beforeSendOf()(mainError("other"))).not.toBeNull();
	});
});

it("uses scrubbed renderer exception text for titles and grouping", async () => {
	const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
	const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent | null;
	const send = (message: string) => {
		transport.capture({ consentGeneration: "g", kind: "exception", message,
			tags: { category: "render_crash" },
			exception: { type: "TypeError", frames: [] },
		});
		return beforeSend(sdk.captureEvent.mock.calls.at(-1)?.[0] as ErrorEvent);
	};
	const first = send("HTTP error 500: /etc/private http://localhost:8080/token alice@example.test '0xdeadbeef'");
	const same = send("HTTP error 503: ~/private http://127.0.0.1/x bob@example.test '12345678-1234-1234-1234-123456789abc'");
	const other = send("Cannot read properties of undefined (reading 'id')");
	expect(first?.exception?.values?.[0]?.value).toBe("HTTP error <n>: <path> <url> <email> '?' (in unknown, render_crash)");
	expect(same?.fingerprint).toEqual(first?.fingerprint);
	expect(other?.fingerprint).not.toEqual(first?.fingerprint);
	expect(other?.message).toBe("Cannot read properties of undefined (reading '?')");
	expect(other?.exception?.values?.[0]?.value).toBe("Cannot read properties of undefined (reading '?') (in unknown, render_crash)");
	expect(other?.fingerprint?.slice(-2)).toEqual(hashParts("cannot read properties of undefined (reading '?')"));
	expect(send("/home/alice/private.txt")?.message).toBe("<path>");
});

it("uses scrubbed messages for handled and SDK-originated main errors", async () => {
	const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
	const beforeSend = sdk.init.mock.calls[0]?.[0].beforeSend as (event: ErrorEvent) => ErrorEvent | null;
	const value = "ENOENT: no such file or directory, open '/var/projects/alice/private'";
	transport.captureMain?.({ category: "ipc", reason: "handler", detail: "channel:project:read", error: new Error(value) });
	const handled = beforeSend(sdk.captureEvent.mock.calls[0][0]);
	expect(handled?.exception?.values?.[0]?.value).toBe("Electron main ipc failure (handler)");
	expect(handled?.fingerprint?.slice(-2)).toEqual(hashParts("enoent: no such file or directory, open '?'"));
	const uncaught = beforeSend({ type: undefined, exception: { values: [{ type: "Error", value }] } });
	expect(uncaught?.exception?.values?.[0]?.value).toBe("Electron main Error in unknown");
});

it("uses the IPC channel and cause code, or the normalized IPC message without one", async () => {
	const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
	const beforeSend = sdk.init.mock.calls[0][0].beforeSend!;
	const send = (channel: string, text: string, code?: string) => {
		transport.capture({
			consentGeneration: "g", kind: "exception",
			message: "Error invoking remote method '" + channel + "': Error: " + text,
			tags: code ? { error_code: code } : undefined,
			exception: { type: "Error", frames: [] },
		});
		return beforeSend(sdk.captureEvent.mock.calls.at(-1)![0], {});
	};
	const first = await send("remotes:list", "Internal server error", "REMOTES_READ_FAILED");
	expect(first).toMatchObject({ message: "Renderer Error in remotes:list (unknown) (REMOTES_READ_FAILED)", fingerprint: ["electron", "ipc", hashParts("remotes:list")[1], "code", "REMOTES_READ_FAILED"] });
	expect((await send("remotes:list", "Erreur interne", "REMOTES_READ_FAILED"))?.fingerprint).toEqual(first?.fingerprint);
	expect((await send("remotes:sync", "Internal server error", "REMOTES_READ_FAILED"))?.fingerprint).not.toEqual(first?.fingerprint);
	const uncoded = await send("remotes:list", "Cannot read /home/alice/private");
	expect(uncoded).toMatchObject({ message: "Renderer Error in remotes:list (unknown)", fingerprint: ["electron", "ipc", hashParts("remotes:list")[1], ...hashParts("cannot read <path>")] });

	expect(uncoded?.tags?.ipc_channel).toBe("remotes:list");

	const cause = new AoError("WORKSPACE_NO_READY_REPOS", "Workspace project must contain at least one fully resolved git repository");
	const sdkError = await beforeSend({ type: undefined, message: "Wrapped" }, { originalException: new Error("Wrapped", { cause }) });
	expect(sdkError).toMatchObject({ message: "Electron main Error in unknown (WORKSPACE_NO_READY_REPOS)", fingerprint: ["electron", "main_error", "unknown", "Error", "unknown", "electron-main", "code", "WORKSPACE_NO_READY_REPOS"] });
});

it.each(["private ".repeat(26).trim(), "x".repeat(301)])("hashes unsafe text and uses the computed title", async (message) => {
	const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
	transport.capture({ consentGeneration: "g", kind: "exception", message, exception: { type: "TypeError", frames: [] } });
	const event = await sdk.init.mock.calls[0][0].beforeSend!(sdk.captureEvent.mock.calls[0][0], {});
	expect(event?.message).toBe("Renderer TypeError in unknown (unknown)");
	expect(event?.fingerprint?.slice(-2)).toEqual(["sha256", createHash("sha256").update(message).digest("hex").slice(0, 10)]);
	expect(JSON.stringify(event)).not.toContain(message);
});

it("omits quoted prompts, email addresses, home paths and remote URLs from the final event", async () => {
	const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
	transport.capture({
		consentGeneration: "g", kind: "exception",
		message: "Could not send 'my private prompt' to alice@example.test from /home/alice/prompt.txt via https://host.test/upload on comic-12",
		exception: { type: "TypeError", frames: [] },
	});
	const event = await sdk.init.mock.calls[0][0].beforeSend!(sdk.captureEvent.mock.calls[0][0], {});
	expect(event?.message).toBe("Could not send '?' to <email> from <path> via <url> on <id>");
	expect(JSON.stringify(event)).not.toMatch(/my private prompt|alice|host\.test|comic-12/);
});

it.each([
	["open /Users/alice/My ConfidentialProject/offer.txt failed", "open <path> failed", /alice|ConfidentialProject|offer\.txt/],
	["request failed access_token=supersecretvalue", "request failed <credential>", /supersecretvalue/],
	["request failed refresh_token=supersecretvalue", "request failed <credential>", /supersecretvalue/],
	["request failed client_secret=supersecretvalue", "request failed <credential>", /supersecretvalue/],
	["repository acme/secret-project not found", "repository acme/secret-project not found", /secret-project/],
	["git checkout feature/acme-merger failed", "git checkout feature/acme-merger failed", /acme-merger/],
	["getaddrinfo ENOTFOUND build.acme-internal.corp", "getaddrinfo enotfound build.acme-internal.corp", /acme-internal/],
	["Session alice-laptop not found", "session alice-laptop not found", /alice-laptop/],
	["HTTP 401 Bad credentials sk-ant-abcdefghijklmnop", "http <n> bad credentials sk-ant-abcdefghijklmnop", /abcdefghijklmnop/],
] as const)("scrubs sensitive values in the final event", async (message, normalized, privateText) => {
	const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
	transport.capture({ consentGeneration: "g", kind: "exception", message, exception: { type: "Error", frames: [] } });
	const event = await sdk.init.mock.calls[0][0].beforeSend!(sdk.captureEvent.mock.calls[0][0], {});
	expect(event?.message).toBe("Renderer Error in unknown (unknown)");
	expect(event?.fingerprint?.slice(-2)).toEqual(hashParts(normalized));
	expect(JSON.stringify(event)).not.toMatch(privateText);
});

it("keeps application codes scoped by source and preserves renderer source tags", async () => {
	const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
	const beforeSend = sdk.init.mock.calls[0][0].beforeSend!;
	const error = new AoError("READ_FAILED", "Private detail");
	transport.captureMain?.({ category: "update", reason: "download", error });
	const handled = await beforeSend(sdk.captureEvent.mock.calls.at(-1)![0], {});
	transport.capture({
		consentGeneration: "g", kind: "exception", message: error.message,
		tags: { category: "renderer_rejection", code: "UNHANDLED_REJECTION", error_code: error.code },
		exception: { type: "Error", frames: [] },
	});
	const renderer = await beforeSend(sdk.captureEvent.mock.calls.at(-1)![0], {});
	expect(renderer?.tags).toMatchObject({ code: "UNHANDLED_REJECTION", error_code: "READ_FAILED" });
	expect(renderer?.message).toContain("READ_FAILED");
	expect(handled?.tags).toMatchObject({ error_code: "READ_FAILED" });
	expect(renderer?.fingerprint).not.toEqual(handled?.fingerprint);
});

it("shows an IPC engine message and channel while keeping the hashed fingerprint", async () => {
	const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
	const message = "Error invoking remote method 'browser:ensure': TypeError: Cannot read properties of undefined (reading 'x')";
	transport.capture({
		consentGeneration: "g", kind: "exception", message,
		tags: { code: "UNHANDLED_REJECTION" },
		exception: { type: "Error", frames: [] },
	});
	const event = await sdk.init.mock.calls[0][0].beforeSend!(sdk.captureEvent.mock.calls[0][0], {});
	expect(event?.tags?.ipc_channel).toBe("browser:ensure");
	expect(event?.message).toBe("Renderer Error in browser:ensure (UNHANDLED_REJECTION): Cannot read properties of undefined (reading '?')");
	expect(event?.fingerprint).toEqual(["electron", "ipc", hashParts("browser:ensure")[1], ...hashParts("cannot read properties of undefined (reading '?')")]);
	expect(JSON.stringify(event)).not.toContain("reading 'x'");
});

it.each([["Error", undefined], ["TypeError", "READ_FAILED"]] as const)("keeps uncoded plain and coded engine errors private", async (type, code) => {
	const transport = await initMainSentry("1.0.0", dataDir, "/tmp/ao-electron-test");
	const message = "Cannot read properties of undefined (reading 'id')";
	transport.capture({ consentGeneration: "g", kind: "exception", message, tags: code ? { error_code: code } : undefined, exception: { type, frames: [] } });
	const event = await sdk.init.mock.calls[0][0].beforeSend!(sdk.captureEvent.mock.calls[0][0], {});
	expect(event?.message).toBe(code ? "Renderer TypeError in unknown (unknown) (READ_FAILED)" : "Renderer Error in unknown (unknown)");
	expect(event?.fingerprint?.slice(-2)).toEqual(code ? ["code", code] : hashParts("cannot read properties of undefined (reading '?')"));
	expect(JSON.stringify(event)).not.toContain("Cannot read properties");
});
