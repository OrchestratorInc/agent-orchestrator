import { beforeEach, describe, expect, it, vi } from "vitest";
const bridge = vi.hoisted(() => ({ getPolicy: vi.fn(), setEventsEnabled: vi.fn(), onPolicy: vi.fn() }));
vi.mock("../lib/bridge", () => ({ aoBridge: { telemetry: { ...bridge, capture: vi.fn(), getBootstrap: vi.fn() } } }));
import type { TelemetryPolicyView } from "../../shared/telemetry-policy";
import { useTelemetryPolicyStore } from "./telemetry-policy-store";

const offView = { eventsEnabled: false, consentGeneration: "generation-off", updatedAt: "2026-08-28T10:15:30.000Z", acknowledged: true, state: "applied" as const, environmentVeto: false, durabilitySupported: true };

describe("telemetry policy store", () => {
	// The store subscribes once per module, so later tests reuse the first listener it registers.
	let policyListener: (view: TelemetryPolicyView) => void = () => undefined;

	beforeEach(() => {
		bridge.getPolicy.mockReset().mockResolvedValue(offView); bridge.setEventsEnabled.mockReset(); bridge.onPolicy.mockReset().mockImplementation((next) => { policyListener = next; return () => undefined; });
		useTelemetryPolicyStore.setState({ view: null, loaded: false, saving: false, saveError: false, saveErrorView: null, loadError: false });
	});
	it("loads and subscribes to the main-owned mutable policy", async () => {
		await useTelemetryPolicyStore.getState().load();
		policyListener({ ...offView, eventsEnabled: true, consentGeneration: "generation-on" });
		expect(useTelemetryPolicyStore.getState().view?.consentGeneration).toBe("generation-on");
	});
	it("does not claim enablement when main reports cleanup pending", async () => {
		bridge.setEventsEnabled.mockResolvedValue({ ...offView, state: "cleanup_pending", acknowledged: false });
		await useTelemetryPolicyStore.getState().setEnabled(true);
		expect(useTelemetryPolicyStore.getState()).toMatchObject({ saving: false, saveError: false, view: { eventsEnabled: false, state: "cleanup_pending" } });
	});
	it("ignores a second choice while the first is still saving", async () => {
		let resolve!: (view: typeof offView) => void;
		bridge.setEventsEnabled.mockImplementationOnce(() => new Promise((next) => { resolve = next; })).mockRejectedValueOnce(new Error("stale telemetry consent generation"));
		const first = useTelemetryPolicyStore.getState().setEnabled(true);
		const second = useTelemetryPolicyStore.getState().setEnabled(false);
		resolve({ ...offView, eventsEnabled: true, consentGeneration: "generation-on" });
		await Promise.all([first, second]);
		expect(bridge.setEventsEnabled).toHaveBeenCalledTimes(1);
		expect(useTelemetryPolicyStore.getState()).toMatchObject({ saving: false, saveError: false, view: { eventsEnabled: true } });
	});

	describe("a failed save and later policy events", () => {
		const onView = { ...offView, eventsEnabled: true, consentGeneration: "generation-on" };
		const pendingView = { ...offView, state: "cleanup_pending" as const, acknowledged: false };

		async function loadWith(view: TelemetryPolicyView) {
			bridge.getPolicy.mockResolvedValue(view);
			await useTelemetryPolicyStore.getState().load();
		}

		it("drops the failure when a newer consent generation arrives", async () => {
			await loadWith(offView);
			bridge.setEventsEnabled.mockRejectedValue(new Error("write failed"));
			await useTelemetryPolicyStore.getState().setEnabled(true);
			expect(useTelemetryPolicyStore.getState().saveError).toBe(true);
			policyListener(onView);
			expect(useTelemetryPolicyStore.getState()).toMatchObject({ saveError: false, view: { consentGeneration: "generation-on", state: "applied" } });
		});

		it("drops the failure when the same generation finishes its cleanup", async () => {
			await loadWith(pendingView);
			bridge.setEventsEnabled.mockRejectedValue(new Error("prior desktop telemetry purge is incomplete"));
			await useTelemetryPolicyStore.getState().setEnabled(true);
			policyListener({ ...offView, consentGeneration: pendingView.consentGeneration });
			expect(useTelemetryPolicyStore.getState()).toMatchObject({ saveError: false, view: { state: "applied" } });
		});

		it("keeps the failure when the retry loop republishes the view it failed against", async () => {
			await loadWith(pendingView);
			bridge.setEventsEnabled.mockRejectedValue(new Error("prior desktop telemetry purge is incomplete"));
			await useTelemetryPolicyStore.getState().setEnabled(true);
			policyListener({ ...pendingView });
			expect(useTelemetryPolicyStore.getState()).toMatchObject({ saveError: true, view: { state: "cleanup_pending" } });
		});

		it("keeps an attempt in flight, and its failure, across the event main publishes before rejecting", async () => {
			await loadWith(onView);
			let reject!: (error: Error) => void;
			bridge.setEventsEnabled.mockImplementationOnce(() => new Promise((_, next) => { reject = next; }));
			const attempt = useTelemetryPolicyStore.getState().setEnabled(false);
			const failedView = { ...onView, eventsEnabled: false, state: "cleanup_failed" as const, acknowledged: false };
			policyListener(failedView);
			expect(useTelemetryPolicyStore.getState()).toMatchObject({ saving: true, saveError: false });
			reject(new Error("write failed"));
			await attempt;
			expect(useTelemetryPolicyStore.getState()).toMatchObject({ saving: false, saveError: true });
			policyListener({ ...failedView });
			expect(useTelemetryPolicyStore.getState().saveError).toBe(true);
		});

		it("measures a failure against the view at rejection, not the one the attempt started from", async () => {
			await loadWith(onView);
			let reject!: (error: Error) => void;
			bridge.setEventsEnabled.mockImplementationOnce(() => new Promise((_, next) => { reject = next; }));
			const attempt = useTelemetryPolicyStore.getState().setEnabled(false);
			const rolledBack = { ...offView, consentGeneration: "generation-rolled-back" };
			policyListener(rolledBack);
			reject(new Error("desktop telemetry transport is unavailable"));
			await attempt;
			expect(useTelemetryPolicyStore.getState().saveError).toBe(true);
			policyListener({ ...rolledBack });
			expect(useTelemetryPolicyStore.getState().saveError).toBe(true);
			policyListener({ ...offView, consentGeneration: "generation-after" });
			expect(useTelemetryPolicyStore.getState().saveError).toBe(false);
		});

		it("starts each attempt clean, so an earlier failure's view cannot affect a later one", async () => {
			await loadWith(offView);
			bridge.setEventsEnabled.mockRejectedValueOnce(new Error("write failed")).mockResolvedValueOnce(onView);
			await useTelemetryPolicyStore.getState().setEnabled(true);
			await useTelemetryPolicyStore.getState().setEnabled(true);
			expect(useTelemetryPolicyStore.getState()).toMatchObject({ saving: false, saveError: false, view: { consentGeneration: "generation-on" } });
		});

		it("reports a failed read as a load failure, not a save failure, until a view arrives", async () => {
			await loadWith(offView);
			useTelemetryPolicyStore.setState({ view: null, loaded: false });
			bridge.getPolicy.mockRejectedValueOnce(new Error("ipc unavailable"));
			await useTelemetryPolicyStore.getState().load();
			expect(useTelemetryPolicyStore.getState()).toMatchObject({ loaded: true, view: null, loadError: true, saveError: false });
			policyListener(onView);
			expect(useTelemetryPolicyStore.getState()).toMatchObject({ loadError: false, saveError: false, view: { consentGeneration: "generation-on" } });
		});
	});
});
