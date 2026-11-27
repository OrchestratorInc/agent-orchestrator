import { AoError } from "../../packages/shared/telemetry/ao-error";
import { afterEach, expect, it, vi } from "vitest";
import { sanitizeRendererCapture } from "./main/sentry-main";
import { DesktopTelemetryController } from "./main/desktop-telemetry-controller";
import type { RendererTelemetryCapture, TelemetryPolicyView } from "./shared/telemetry-policy";

const mocks = vi.hoisted(() => ({ invoke: vi.fn(), listeners: new Map<string, (...args: unknown[]) => void>() }));
vi.mock("@sentry/electron/main", () => ({}));
vi.mock("electron", () => ({
	contextBridge: {
		exposeInMainWorld: (key: string, value: unknown) => Object.defineProperty(window, key, { value, configurable: true }),
		// Exercise the serialized function, which must have no module closures.
		executeInMainWorld: ({ func }: { func: () => void }) => { Function(`(${func.toString()})()`)(); },
	},
	ipcRenderer: { invoke: (...args: unknown[]) => mocks.invoke(...args), on: (key: string, fn: (...args: unknown[]) => void) => mocks.listeners.set(key, fn), send: vi.fn() },
	webUtils: { getPathForFile: vi.fn() },
}));

const addListener = vi.spyOn(window, "addEventListener");
afterEach(() => {
	for (const [name, listener, options] of addListener.mock.calls) window.removeEventListener(name, listener, options);
	addListener.mockClear();
	Reflect.deleteProperty(window, "__aoRendererErrors"); Reflect.deleteProperty(window, "ao");
	mocks.invoke.mockReset(); mocks.listeners.clear();
});

async function setup(enabled: boolean) {
	vi.resetModules();
	let snapshot = { eventsEnabled: enabled, consentGeneration: "generation-1", acknowledged: true, updatedAt: "2026-11-04T00:00:00Z" };
	const capture = vi.fn<(input: RendererTelemetryCapture) => void>();
	const controller = new DesktopTelemetryController({
		environmentAllowsEvents: true, productionEnabled: true,
		authority: {
			durabilitySupported: true, load: async () => snapshot, snapshot: () => snapshot,
			setEventsEnabled: async (eventsEnabled) => { snapshot = { ...snapshot, eventsEnabled, consentGeneration: "generation-2" }; return snapshot; },
			retryPendingReplacement: async () => snapshot,
		},
		transportFactory: async () => ({ capture, closeAndDrain: async () => {}, clearCache: async () => {} }),
	});
	await controller.initialize();
	let resolvePolicy!: (view: TelemetryPolicyView) => void;
	const policy = new Promise<TelemetryPolicyView>((resolve) => { resolvePolicy = resolve; });
	mocks.invoke.mockImplementation(async (channel: string, input: RendererTelemetryCapture) => channel === "telemetry:getPolicy" ? policy : channel === "telemetry:capture" ? controller.capture(sanitizeRendererCapture(input)!) : null);
	await import("./preload");
	return { controller, capture, resolvePolicy };
}

function fail(error = new TypeError("private error, token and project")): Error {
	window.dispatchEvent(new ErrorEvent("error", { error, message: error.message, filename: "file:///Users/private/project/main.js", lineno: 10, colno: 2 }));
	return error;
}

it("captures an entry-module failure before the renderer runs, using main-owned consent and bounded message text", async () => {
	const { controller, capture, resolvePolicy } = await setup(true);
	const error = fail();
	expect(capture).not.toHaveBeenCalled();
	expect(mocks.invoke).toHaveBeenCalledWith("telemetry:getPolicy");
	resolvePolicy(controller.snapshot());
	await vi.waitFor(() => expect(capture).toHaveBeenCalledOnce());
	expect(capture.mock.calls[0][0]).toMatchObject({ consentGeneration: "generation-1", kind: "exception", message: error.message, tags: { category: "renderer_exception", code: "WINDOW_ERROR" }, exception: { type: "TypeError" } });
	expect(JSON.stringify(capture.mock.calls[0][0].exception)).not.toContain("Users");
	// Runtime and preload use one listener. Re-observing the same early error
	// through another boundary cannot send it a second time.
	const { bindRendererErrorHandlers, captureRendererException } = await import("./renderer/lib/telemetry");
	bindRendererErrorHandlers();
	await captureRendererException(error);
	expect(capture).toHaveBeenCalledOnce();
	const next = new ReferenceError("private runtime error");
	fail(next);
	await vi.waitFor(() => expect(capture).toHaveBeenCalledTimes(2));
});

it("discards opted-out startup errors without replaying them on a later opt-in", async () => {
	const { controller, capture, resolvePolicy } = await setup(false);
	fail(); resolvePolicy(controller.snapshot());
	await vi.waitFor(() => expect(mocks.invoke).toHaveBeenCalledWith("telemetry:capture", expect.anything()));
	expect(capture).not.toHaveBeenCalled();
	const enabled = await controller.setEventsEnabled(true, "generation-1");
	mocks.listeners.get("telemetry:policyChanged")?.({}, enabled);
	expect(capture).not.toHaveBeenCalled();
	fail(new SyntaxError("new failure"));
	await vi.waitFor(() => expect(capture).toHaveBeenCalledOnce());
});

it("bounds repeated startup failures and reports script-load and rejected-promise failures", async () => {
	const { controller, capture, resolvePolicy } = await setup(true);
	for (let i = 0; i < 6; i++) fail();
	resolvePolicy(controller.snapshot());
	await vi.waitFor(() => expect(capture).toHaveBeenCalledTimes(5));
	const script = document.createElement("script"); document.body.append(script);
	script.dispatchEvent(new Event("error"));
	const rejection = new Event("unhandledrejection"); Object.defineProperty(rejection, "reason", { value: new RangeError("private rejection") });
	window.dispatchEvent(rejection);
	await vi.waitFor(() => expect(capture).toHaveBeenCalledTimes(7));
	script.remove();
});

it("does not let a late initial policy response replace a newer opt-out broadcast", async () => {
	const { controller, capture, resolvePolicy } = await setup(true);
	const old = controller.snapshot();
	const disabled = await controller.setEventsEnabled(false, "generation-1");
	mocks.listeners.get("telemetry:policyChanged")?.({}, disabled);
	resolvePolicy(old);
	await Promise.resolve(); await Promise.resolve();
	fail();
	await vi.waitFor(() => expect(mocks.invoke).toHaveBeenCalledWith("telemetry:capture", expect.objectContaining({ consentGeneration: disabled.consentGeneration })));
	expect(capture).not.toHaveBeenCalled();
});


it("drops Chromium's ResizeObserver loop notice before and after the renderer takes over", async () => {
	const { controller, capture, resolvePolicy } = await setup(true);
	resolvePolicy(controller.snapshot());
	const notice = (message: string) => window.dispatchEvent(new ErrorEvent("error", { message, filename: "app://renderer/index.html" }));
	notice("ResizeObserver loop completed with undelivered notifications.");
	notice("ResizeObserver loop limit exceeded");
	const { bindRendererErrorHandlers } = await import("./renderer/lib/telemetry");
	bindRendererErrorHandlers();
	notice("ResizeObserver loop completed with undelivered notifications.");
	// Only the notice itself, with no error object, is dropped. A leaked notice
	// would arrive first, typed Error, ahead of this TypeError.
	const thrown = new TypeError("ResizeObserver loop completed with undelivered notifications.");
	window.dispatchEvent(new ErrorEvent("error", { error: thrown, message: thrown.message }));
	await vi.waitFor(() => expect(capture).toHaveBeenCalled());
	expect(capture).toHaveBeenCalledOnce();
	expect(capture.mock.calls[0][0]).toMatchObject({ tags: { code: "WINDOW_ERROR" }, exception: { type: "TypeError" } });
});

it("drops a bare cross-origin Script error but reports one with a file name or an error object", async () => {
	const { controller, capture, resolvePolicy } = await setup(true);
	resolvePolicy(controller.snapshot());
	window.dispatchEvent(new ErrorEvent("error", { message: "Script error." }));
	window.dispatchEvent(new ErrorEvent("error", { message: "Script error" }));
	window.dispatchEvent(new ErrorEvent("error", { message: "Script error.", filename: "app://renderer/assets/index-A1B2C3D4.js", lineno: 3, colno: 4 }));
	await vi.waitFor(() => expect(capture).toHaveBeenCalled());
	expect(capture).toHaveBeenCalledOnce();
	expect(capture.mock.calls[0][0]).toMatchObject({ message: "Script error.", exception: { frames: [{ filename: "assets/index-A1B2C3D4.js", lineno: 3 }] } });
	const thrown = new TypeError("Script error.");
	window.dispatchEvent(new ErrorEvent("error", { error: thrown, message: thrown.message }));
	await vi.waitFor(() => expect(capture).toHaveBeenCalledTimes(2));
	expect(capture.mock.calls[1][0]).toMatchObject({ exception: { type: "TypeError" } });
});

it("keeps an early rejection's minified frame and debug ID", async () => {
	const { controller, capture, resolvePolicy } = await setup(true);
	const filename = "assets/index-A1B2C3D4.js";
	const debugId = "8a1f4c2e-1b3d-4e5f-9a6b-7c8d9e0f1a2b";
	vi.stubGlobal("_sentryDebugIds", { [`Error\n    at app://renderer/${filename}:1:2`]: debugId });
	try {
		const error = new TypeError("Cannot read properties of undefined (reading 'id')", { cause: new AoError("RENDERER_INIT_FAILED", "Import failed") });
		error.stack = `TypeError: ${error.message}\n    at a (app://renderer/${filename}:17:99)`;
		const rejection = new Event("unhandledrejection");
		Object.defineProperty(rejection, "reason", { value: error });
		window.dispatchEvent(rejection);
		resolvePolicy(controller.snapshot());
		await vi.waitFor(() => expect(capture).toHaveBeenCalledOnce());
		expect(capture.mock.calls[0][0]).toMatchObject({
			message: error.message,
			tags: { category: "renderer_rejection", code: "UNHANDLED_REJECTION", error_code: "RENDERER_INIT_FAILED" },
			exception: { type: "TypeError", frames: [{ function: "renderer_frame", filename, lineno: 17, colno: 99, debug_id: debugId }] },
		});
	} finally {
		vi.unstubAllGlobals();
	}
});

it("preserves a remotes:list code across the preload bridge without changing its displayed error", async () => {
	const { controller, resolvePolicy } = await setup(true);
	resolvePolicy(controller.snapshot());
	mocks.invoke.mockImplementation(async (channel: string) => channel === "remotes:list"
		? { code: "REMOTES_READ_FAILED", message: "Error: Could not read remote hosts" }
		: null);
	const { listRemoteHosts } = await import("./renderer/lib/remote-host-list");
	await expect(listRemoteHosts()).rejects.toMatchObject({
		code: "REMOTES_READ_FAILED",
		message: "Error invoking remote method 'remotes:list': Error: Could not read remote hosts",
	});
	expect(mocks.invoke).toHaveBeenCalledWith("remotes:list", true);
});

it("still captures an early rejection whose code getter throws", async () => {
	const { controller, resolvePolicy } = await setup(true);
	resolvePolicy(controller.snapshot());
	const error = Object.defineProperty(new Error("An early failure"), "code", {
		get() { throw new Error("getter failed"); },
	});
	const event = new Event("unhandledrejection");
	Object.defineProperty(event, "reason", { value: error });
	window.dispatchEvent(event);
	await vi.waitFor(() => expect(mocks.invoke).toHaveBeenCalledWith("telemetry:capture", expect.objectContaining({
		message: "An early failure", tags: expect.objectContaining({ code: "UNHANDLED_REJECTION" }),
	})));
});

it.each([
	["EXPECTED_IPC_ERROR", "Sign in to Orchestrator Cloud"],
	["LOCAL_IDENTITY_UNAVAILABLE_ERROR", "Local Orchestrator identity is unavailable; remote connection is offline"],
])("preserves the expected %s rejection without reporting it", async (code, message) => {
	const { controller, resolvePolicy } = await setup(true);
	resolvePolicy(controller.snapshot());
	mocks.invoke.mockImplementation(async (channel: string) => channel === "remotes:list"
		? { code, message: "Error: " + message, expected: true }
		: null);
	const { listRemoteHosts } = await import("./renderer/lib/remote-host-list");
	const error = await listRemoteHosts().catch((reason: unknown) => reason);
	expect(error).toMatchObject({ code, expected: true, message: "Error invoking remote method 'remotes:list': Error: " + message });
	const wrapped = new AoError("REMOTE_WRAPPER", "wrapped", { cause: error });
	const event = new Event("unhandledrejection");
	Object.defineProperty(event, "reason", { value: wrapped });
	window.dispatchEvent(event);
	const { captureExceptionToSentry } = await import("./renderer/lib/sentry");
	captureExceptionToSentry(wrapped);
	expect(mocks.invoke.mock.calls.some(([channel]) => channel === "telemetry:capture")).toBe(false);
});

it("forwards uncoded engine errors with their original type for main's display rule", async () => {
	const { controller, capture, resolvePolicy } = await setup(true);
	const message = "Cannot read properties of undefined (reading 'id')";
	fail(new TypeError(message));
	resolvePolicy(controller.snapshot());
	await vi.waitFor(() => expect(capture).toHaveBeenCalledOnce());
	expect(capture.mock.calls[0][0]).toMatchObject({ message, exception: { type: "TypeError" } });
});
