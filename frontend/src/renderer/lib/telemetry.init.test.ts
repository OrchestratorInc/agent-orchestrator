import { beforeEach, describe, expect, it, vi } from "vitest";

// initTelemetry memoizes its result in a module-scoped promise, so each test
// resets the module registry and re-mocks the heavy boundaries (posthog, the
// Electron bridge) before importing. This exercises the real init control flow.

const posthogStub = {
	init: vi.fn(),
	register: vi.fn(),
	capture: vi.fn((_event?: string, _properties?: Record<string, unknown>): object | undefined => ({})),
	captureException: vi.fn(),
	addExceptionStep: vi.fn(),
};

function mockPosthog() {
	vi.doMock("posthog-js/dist/module.full.no-external", () => ({
		default: posthogStub,
		PostHog: class {},
	}));
}

describe("initTelemetry recovery", () => {
	beforeEach(() => {
		vi.resetModules();
		vi.clearAllMocks();
	});

	async function initializedUpdateCapture() {
		localStorage.clear();
		mockPosthog();
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: { getBootstrap: vi.fn(async () => ({
					appVersion: "1.2.3", platform: "darwin", distinctId: "ins_test",
					disabledEvents: [], eventsEnabled: true, consentGeneration: "generation-1",
				})) },
				updateSettings: { get: vi.fn(async () => ({})) },
			},
		}));
		const { captureRendererEvent, initTelemetry } = await import("./telemetry");
		expect(await initTelemetry()).toBe(true);
		posthogStub.capture.mockClear();
		return captureRendererEvent;
	}

	it("retries init after a transient thrown failure instead of caching it", async () => {
		let calls = 0;
		mockPosthog();
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: {
					getBootstrap: vi.fn(async () => {
						calls++;
						if (calls === 1) throw new Error("transient bootstrap failure");
						return { appVersion: "1.2.3", platform: "darwin", distinctId: "ins_test", disabledEvents: [], eventsEnabled: true, consentGeneration: "generation-1" };
					}),
				},
				updateSettings: { get: vi.fn(async () => ({})) },
			},
		}));

		const { initTelemetry } = await import("./telemetry");
		// First attempt throws -> caught -> false, and the memoized promise is dropped.
		expect(await initTelemetry()).toBe(false);
		// A later call retries and succeeds, rather than the whole session staying dark.
		expect(await initTelemetry()).toBe(true);
		expect(calls).toBe(2);
	});

	it("keeps a deliberately withheld result memoized (no retry, no re-bootstrap)", async () => {
		// Null bootstrap = telemetry withheld (no key / not opted in). That is a
		// deliberate false, not an error, so it must stay cached and NOT retry.
		const getBootstrap = vi.fn(async () => null);
		mockPosthog();
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: { getBootstrap },
				updateSettings: { get: vi.fn(async () => ({})) },
			},
		}));

		const { initTelemetry } = await import("./telemetry");
		expect(await initTelemetry()).toBe(false);
		expect(await initTelemetry()).toBe(false);
		// Memoized: the withheld result is not re-attempted.
		expect(getBootstrap).toHaveBeenCalledTimes(1);
	});

	it("initializes PostHog when failure reporting is disabled", async () => {
		mockPosthog();
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: {
					getBootstrap: vi.fn(async () => ({
						appVersion: "1.2.3", platform: "darwin", distinctId: "ins_test",
						disabledEvents: [], eventsEnabled: false, consentGeneration: "generation-off",
					})),
				},
				updateSettings: { get: vi.fn(async () => ({})) },
			},
		}));

		const { applyRendererTelemetryPolicy, clearRendererTelemetryQueues, initTelemetry } = await import("./telemetry");
		expect(await initTelemetry()).toBe(true);
		expect(posthogStub.init).toHaveBeenCalledOnce();
		expect(() => clearRendererTelemetryQueues()).not.toThrow();
		expect(() => applyRendererTelemetryPolicy(false)).not.toThrow();
	});

	it("deduplicates updater failures at the real capture boundary", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const failure = { phase: "download", error_category: "network", to_version: "2.0.0" };
		expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(true);
		expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(false);
		expect(await captureRendererEvent("ao.renderer.update_downloaded", failure)).toBe(true);
		expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(true);
		expect(posthogStub.capture.mock.calls.map(([name]) => name)).toEqual([
			"ao.renderer.update_failed", "ao.renderer.update_downloaded", "ao.renderer.update_failed",
		]);
	});

	it("does not deduplicate an update failure rejected by the rate limit", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const clock = vi.spyOn(Date, "now").mockReturnValue(1_000_000_000);
		try {
			for (let index = 0; index < 5; index++) {
				expect(await captureRendererEvent("ao.renderer.update_failed", {
					phase: "download", error_category: "network", to_version: `2.0.${index}`,
				})).toBe(true);
			}
			const sixth = { phase: "download", error_category: "network", to_version: "2.0.5" };
			expect(await captureRendererEvent("ao.renderer.update_failed", sixth)).toBe(false);
			clock.mockReturnValue(1_000_060_001);
			expect(await captureRendererEvent("ao.renderer.update_failed", sixth)).toBe(true);
			expect(posthogStub.capture).toHaveBeenCalledTimes(6);
		} finally {
			clock.mockRestore();
		}
	});

	it("emits one update failure per streak and one daily count after the UTC day ends", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const failure = { phase: "check", error_category: "network", to_version: "2.0.0" };
		vi.useFakeTimers();
		vi.setSystemTime(new Date("2026-10-20T12:00:00.000Z"));
		try {
			expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(true);
			expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(false);
			vi.setSystemTime(new Date("2026-10-21T00:05:00.000Z"));
			expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(true);
			expect(posthogStub.capture.mock.calls.at(-1)?.[0]).toBe("ao.renderer.update_failures_daily");
			expect(posthogStub.capture.mock.calls.at(-1)?.[1]).toMatchObject({ day: "2026-10-20", failure_count: 2 });
			expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(false);
		} finally {
			vi.useRealTimers();
		}
	});

	it("does not export updater failures counted while opted out", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const { applyAnalyticsOptOut } = await import("./telemetry");
		const failure = { phase: "check", error_category: "network" };
		vi.useFakeTimers();
		vi.setSystemTime(new Date("2026-10-20T12:00:00.000Z"));
		try {
			expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(true);
			await applyAnalyticsOptOut(true);
			posthogStub.capture.mockImplementation(() => undefined);
			expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(false);
			posthogStub.capture.mockImplementation(() => ({}));
			await applyAnalyticsOptOut(false);
			vi.setSystemTime(new Date("2026-10-21T00:05:00.000Z"));
			posthogStub.capture.mockClear();
			expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(true);
			expect(posthogStub.capture.mock.calls.map(([event]) => event)).toEqual(["ao.renderer.update_failed"]);
		} finally {
			vi.useRealTimers();
		}
	});

	it("does not restore updater counts when an opt-out lands while a daily count is sending", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const { applyAnalyticsOptOut } = await import("./telemetry");
		const failure = { phase: "check", error_category: "network" };
		vi.useFakeTimers();
		vi.setSystemTime(new Date("2026-10-20T12:00:00.000Z"));
		try {
			expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(true);
			vi.setSystemTime(new Date("2026-10-21T00:05:00.000Z"));
			posthogStub.capture.mockImplementationOnce(() => {
				void applyAnalyticsOptOut(true);
				return undefined;
			});
			expect(await captureRendererEvent("ao.renderer.update_failed", failure)).toBe(false);
			expect(localStorage.getItem("ao.telemetry.updateFailures.v1")).toBeNull();
		} finally {
			vi.useRealTimers();
		}
	});

	it("allows retry after PostHog rejects or throws while capturing an update failure", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const rejected = { phase: "download", error_category: "network", to_version: "2.0.0" };
		posthogStub.capture.mockImplementationOnce(() => undefined);
		expect(await captureRendererEvent("ao.renderer.update_failed", rejected)).toBe(false);
		expect(await captureRendererEvent("ao.renderer.update_failed", rejected)).toBe(true);

		const thrown = { phase: "download", error_category: "network", to_version: "2.0.1" };
		posthogStub.capture.mockImplementationOnce(() => { throw new Error("capture failed"); });
		await expect(captureRendererEvent("ao.renderer.update_failed", thrown)).rejects.toThrow("capture failed");
		expect(await captureRendererEvent("ao.renderer.update_failed", thrown)).toBe(true);
	});
});
