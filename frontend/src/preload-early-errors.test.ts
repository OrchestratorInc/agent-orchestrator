import { afterEach, expect, it, vi } from "vitest";
import { DesktopTelemetryController } from "./main/desktop-telemetry-controller";
import type { RendererTelemetryCapture, TelemetryPolicyView } from "./shared/telemetry-policy";

const mocks = vi.hoisted(() => ({ invoke: vi.fn(), listeners: new Map<string, (...args: unknown[]) => void>() }));
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
	mocks.invoke.mockImplementation(async (channel: string, input: RendererTelemetryCapture) => channel === "telemetry:getPolicy" ? policy : channel === "telemetry:capture" ? controller.capture(input) : null);
	await import("./preload");
	return { controller, capture, resolvePolicy };
}

function fail(error = new TypeError("private error, token and project")): Error {
	window.dispatchEvent(new ErrorEvent("error", { error, message: error.message, filename: "file:///Users/private/project/main.js", lineno: 10, colno: 2 }));
	return error;
}

it("captures an entry-module failure before the renderer runs, using main-owned consent and no private text", async () => {
	const { controller, capture, resolvePolicy } = await setup(true);
	const error = fail();
	expect(capture).not.toHaveBeenCalled();
	expect(mocks.invoke).toHaveBeenCalledWith("telemetry:getPolicy");
	resolvePolicy(controller.snapshot());
	await vi.waitFor(() => expect(capture).toHaveBeenCalledOnce());
	expect(capture.mock.calls[0][0]).toMatchObject({ consentGeneration: "generation-1", kind: "exception", message: "Renderer startup error", exception: { type: "TypeError" } });
	expect(JSON.stringify(capture.mock.calls)).not.toMatch(/private|token|project|Users/);
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
