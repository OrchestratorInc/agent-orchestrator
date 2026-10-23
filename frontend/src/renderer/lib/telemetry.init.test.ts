import { beforeEach, describe, expect, it, vi } from "vitest";

// initTelemetry memoizes its result in a module-scoped promise, so each test
// resets the module registry and re-mocks the heavy boundaries (posthog, the
// Electron bridge) before importing. This exercises the real init control flow.

const posthogStub = {
	init: vi.fn(),
	register: vi.fn(),
	capture: vi.fn(() => ({})),
	captureException: vi.fn(),
	addExceptionStep: vi.fn(),
	identify: vi.fn(),
	reset: vi.fn(),
	setPersonProperties: vi.fn(),
	opt_in_capturing: vi.fn(),
	opt_out_capturing: vi.fn(),
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
});

describe("PostHog identity and opt-out", () => {
	const bootstrap = { appVersion: "1.2.3", platform: "darwin", distinctId: "ins_test", installId: "ins_test", disabledEvents: [], eventsEnabled: true, consentGeneration: "g" };

	function mockBridge(getBootstrap: () => Promise<unknown>) {
		mockPosthog();
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: { getBootstrap, getGithubLogin: vi.fn(async () => null), capture: vi.fn(async () => undefined) },
				updateSettings: { get: vi.fn(async () => ({})) },
			},
		}));
	}

	beforeEach(() => {
		vi.resetModules();
		vi.clearAllMocks();
	});

	it("identifies the WorkOS user once with email as a person property, never as a super property", async () => {
		mockBridge(async () => bootstrap);
		const { identifyCloudUser, captureRendererEvent, captureRendererException } = await import("./telemetry");
		await identifyCloudUser({ id: "user_01H", email: "dev@example.com" });
		await identifyCloudUser({ id: "user_01H", email: "dev@example.com" });

		expect(posthogStub.identify).toHaveBeenCalledTimes(1);
		expect(posthogStub.identify).toHaveBeenCalledWith("user_01H", {
			email: "dev@example.com",
			ao_cloud_user_id: "user_01H",
		});
		// Email must not be stamped on events: not registered, not on a capture.
		for (const [props] of posthogStub.register.mock.calls as unknown as [Record<string, unknown>][]) {
			expect(JSON.stringify(props)).not.toContain("dev@example.com");
			expect(props).not.toHaveProperty("email");
		}
		expect(posthogStub.register).toHaveBeenLastCalledWith(expect.objectContaining({ ao_cloud_user_id: "user_01H" }));
		await captureRendererEvent("ao.renderer.support_opened");
		const captured = posthogStub.capture.mock.calls.at(-1) as unknown as [string, Record<string, unknown>];
		expect(JSON.stringify(captured)).not.toContain("dev@example.com");
		expect(captured[1]).not.toHaveProperty("$process_person_profile");
		expect(captured[1].install_id).toBe("ins_test");
		await captureRendererException(new Error("test"), { install_id: "spoofed" });
		expect((posthogStub.captureException.mock.calls.at(-1) as unknown as [Error, Record<string, unknown>])[1].install_id).toBe("ins_test");
		const init = posthogStub.init.mock.calls[0] as unknown as [string, { before_send: (event: Record<string, unknown>) => Record<string, unknown> }];
		const sent = init[1].before_send({ event: "ao.test", properties: { install_id: "spoofed" } });
		expect((sent.properties as Record<string, unknown>).install_id).toBe("ins_test");
	});

	it("keeps events anonymous before sign-in and after sign-out", async () => {
		mockBridge(async () => bootstrap);
		const { identifyCloudUser, clearCloudUser, captureRendererEvent } = await import("./telemetry");
		await captureRendererEvent("ao.renderer.support_opened");
		expect((posthogStub.capture.mock.calls.at(-1) as unknown as [string, Record<string, unknown>])[1]).toMatchObject({ $process_person_profile: false, install_id: "ins_test" });

		await identifyCloudUser({ id: "user_01H", email: "dev@example.com" });
		clearCloudUser();
		expect(posthogStub.reset).toHaveBeenCalledTimes(1);
		await captureRendererEvent("ao.renderer.mobile_connect_opened", { bridge_enabled: true });
		expect((posthogStub.capture.mock.calls.at(-1) as unknown as [string, Record<string, unknown>])[1]).toMatchObject({ $process_person_profile: false, install_id: "ins_test" });
	});

	it("does not identify a user who signed out while init was still pending", async () => {
		let release!: () => void;
		const gate = new Promise<void>((resolve) => (release = resolve));
		mockBridge(async () => {
			await gate;
			return bootstrap;
		});
		const { identifyCloudUser, clearCloudUser } = await import("./telemetry");
		const pending = identifyCloudUser({ id: "user_A", email: "a@example.com" });
		await Promise.resolve();
		clearCloudUser(); // init has not finished, so there is nothing to reset yet
		release();
		await pending;
		expect(posthogStub.identify).not.toHaveBeenCalled();
	});

	it("a pending identify for user A loses to a newer sign-in as user B", async () => {
		let release!: () => void;
		const gate = new Promise<void>((resolve) => (release = resolve));
		mockBridge(async () => {
			await gate;
			return bootstrap;
		});
		const { identifyCloudUser } = await import("./telemetry");
		const a = identifyCloudUser({ id: "user_A", email: "a@example.com" });
		const b = identifyCloudUser({ id: "user_B", email: "b@example.com" });
		release();
		await Promise.all([a, b]);
		expect(posthogStub.identify).toHaveBeenCalledTimes(1);
		expect(posthogStub.identify).toHaveBeenCalledWith("user_B", expect.objectContaining({ email: "b@example.com" }));
	});

	it("A, sign-out, B: each identify starts from a reset, so B never joins A's person", async () => {
		mockBridge(async () => bootstrap);
		const { identifyCloudUser, clearCloudUser } = await import("./telemetry");
		const order: string[] = [];
		posthogStub.identify.mockImplementation((id: string) => order.push(`identify:${id}`));
		posthogStub.reset.mockImplementation(() => order.push("reset"));

		await identifyCloudUser({ id: "user_A", email: "a@example.com" });
		clearCloudUser();
		await identifyCloudUser({ id: "user_B", email: "b@example.com" });
		// Direct switch with no sign-out in between also resets first.
		await identifyCloudUser({ id: "user_C", email: "c@example.com" });
		expect(order).toEqual(["identify:user_A", "reset", "identify:user_B", "reset", "identify:user_C"]);
		posthogStub.identify.mockReset();
		posthogStub.reset.mockReset();
	});

	it("opt-out stops capture and resets the SDK identity; a later sign-in does not identify", async () => {
		mockBridge(async () => bootstrap);
		const { initTelemetry, identifyCloudUser, applyAnalyticsOptOut, captureRendererEvent } = await import("./telemetry");
		await initTelemetry();
		await applyAnalyticsOptOut(true);
		expect(posthogStub.opt_out_capturing).toHaveBeenCalledTimes(1);
		expect(posthogStub.reset).toHaveBeenCalledTimes(1);

		await identifyCloudUser({ id: "user_01H", email: "dev@example.com" });
		expect(posthogStub.identify).not.toHaveBeenCalled();

		// Opting back in resumes capture and identifies the signed-in user.
		await applyAnalyticsOptOut(false);
		expect(posthogStub.opt_in_capturing).toHaveBeenLastCalledWith({ captureEventName: false });
		expect(posthogStub.identify).toHaveBeenCalledWith("user_01H", expect.objectContaining({ email: "dev@example.com" }));
		await captureRendererEvent("ao.renderer.support_opened", { install_id: "spoofed" });
		expect((posthogStub.capture.mock.calls.at(-1) as unknown as [string, Record<string, unknown>])[1].install_id).toBe("ins_test");
	});

	it("a launch that started opted out never builds a client, and opting in initializes one", async () => {
		let optedOut = true;
		const getBootstrap = vi.fn(async () => (optedOut ? null : bootstrap));
		mockBridge(getBootstrap);
		const { initTelemetry, applyAnalyticsOptOut } = await import("./telemetry");
		expect(await initTelemetry()).toBe(false);
		expect(posthogStub.init).not.toHaveBeenCalled();

		optedOut = false;
		await applyAnalyticsOptOut(false);
		expect(posthogStub.init).toHaveBeenCalledTimes(1);
	});

	it("honors an opt-out that arrives while init is still reading update settings", async () => {
		let releaseSettings!: () => void;
		const settingsGate = new Promise<void>((resolve) => (releaseSettings = resolve));
		mockPosthog();
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: { getBootstrap: vi.fn(async () => bootstrap), getGithubLogin: vi.fn(async () => null) },
				updateSettings: { get: vi.fn(async () => { await settingsGate; return {}; }) },
			},
		}));
		const { initTelemetry, applyAnalyticsOptOut } = await import("./telemetry");
		const init = initTelemetry();
		await Promise.resolve();
		await applyAnalyticsOptOut(true); // client is not ready yet: only the flag can be set
		releaseSettings();

		expect(await init).toBe(false);
		expect(posthogStub.init).not.toHaveBeenCalled();
		expect(posthogStub.opt_in_capturing).not.toHaveBeenCalled();
	});

	it("honors an opt-out while the PostHog module is loading", async () => {
		let releaseModule!: () => void;
		let moduleStarted!: () => void;
		const gate = new Promise<void>((resolve) => (releaseModule = resolve));
		const started = new Promise<void>((resolve) => (moduleStarted = resolve));
		vi.doMock("posthog-js/dist/module.full.no-external", async () => {
			moduleStarted();
			await gate;
			return { default: posthogStub, PostHog: class {} };
		});
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: { getBootstrap: vi.fn(async () => bootstrap), getGithubLogin: vi.fn(async () => null) },
				updateSettings: { get: vi.fn(async () => ({})) },
			},
		}));
		const { initTelemetry, applyAnalyticsOptOut } = await import("./telemetry");
		const init = initTelemetry();
		await started;
		await applyAnalyticsOptOut(true);
		releaseModule();
		expect(await init).toBe(false);
		expect(posthogStub.init).not.toHaveBeenCalled();
		expect(posthogStub.opt_in_capturing).not.toHaveBeenCalled();
	});
});
