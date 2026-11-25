import { beforeEach, describe, expect, it, vi } from "vitest";

// initTelemetry memoizes its result in a module-scoped promise, so each test
// resets the module registry and re-mocks the heavy boundaries (posthog, the
// Electron bridge) before importing. This exercises the real init control flow.

const posthogStub = {
	init: vi.fn(),
	register: vi.fn(),
	unregister: vi.fn(),
	get_distinct_id: vi.fn(() => "ins_test"),
	capture: vi.fn((_event?: string, _properties?: Record<string, unknown>, _options?: Record<string, unknown>): object | undefined => ({})),
	captureException: vi.fn(),
	addExceptionStep: vi.fn(),
	identify: vi.fn(),
	alias: vi.fn((_alias?: string, _original?: string): object | number | undefined => ({})),
	reset: vi.fn(),
	setPersonProperties: vi.fn(),
	opt_in_capturing: vi.fn(),
	opt_out_capturing: vi.fn(),
	set_config: vi.fn(),
	reloadFeatureFlags: vi.fn(),
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

	it("captures every renamed event under its new name and never an old one", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const { LEGACY_EVENT_NAMES } = await import("./telemetry");
		for (const name of Object.keys(LEGACY_EVENT_NAMES)) {
			posthogStub.capture.mockClear();
			expect(await captureRendererEvent(name, { phase: "download", error_category: "network", to_version: "9.9.9", surface: "home" }), name).toBe(true);
			expect(posthogStub.capture.mock.calls.map(([event]) => event), name).toContain(name);
		}
		const legacy = new Set(Object.values(LEGACY_EVENT_NAMES).flat());
		expect(posthogStub.capture.mock.calls.some(([event]) => legacy.has(String(event)))).toBe(false);
	});

	it("sends an onboarding event from a closing window at once by sendBeacon, sanitized", async () => {
		await initializedUpdateCapture();
		const { captureRendererEventOnUnload } = await import("./telemetry");
		expect(captureRendererEventOnUnload("ao.onboarding.abandoned", { step: "tools", run_kind: "first_run", duration_ms: 60_000, path: "/Users/me/repo" })).toBe(true);
		expect(posthogStub.capture).toHaveBeenCalledTimes(1);
		const [event, properties, options] = posthogStub.capture.mock.calls[0]!;
		expect(event).toBe("ao.onboarding.abandoned");
		expect(properties).toMatchObject({ step: "tools", step_index: 3, run_kind: "first_run", duration_ms: 60_000 });
		expect(properties).not.toHaveProperty("path");
		expect(options).toEqual({ send_instantly: true, transport: "sendBeacon" });
	});

	it("sends nothing from a closing window for other events or once opted out", async () => {
		await initializedUpdateCapture();
		const { applyAnalyticsOptOut, captureRendererEventOnUnload } = await import("./telemetry");
		expect(captureRendererEventOnUnload("ao.app.loaded")).toBe(false);
		await applyAnalyticsOptOut(true);
		expect(captureRendererEventOnUnload("ao.onboarding.step_abandoned", { step: "tools" })).toBe(false);
		expect(posthogStub.capture).not.toHaveBeenCalled();
	});

	it("sends nothing from a closing window before PostHog is ready", async () => {
		mockPosthog();
		const { captureRendererEventOnUnload } = await import("./telemetry");
		expect(captureRendererEventOnUnload("ao.onboarding.abandoned", { step: "tools" })).toBe(false);
		expect(posthogStub.capture).not.toHaveBeenCalled();
	});

	it("writes log-only events to the console instead of PostHog", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const info = vi.spyOn(console, "info").mockImplementation(() => undefined);
		try {
			for (const name of ["ao.renderer.agents_available", "ao.renderer.cloud_offering_resolved"]) {
				expect(await captureRendererEvent(name, { installed_count: 2, org_kind: "b2c" })).toBe(true);
				expect(info).toHaveBeenCalledWith(`[telemetry] ${name}`, expect.any(Object));
			}
			expect(posthogStub.capture).not.toHaveBeenCalled();
		} finally {
			info.mockRestore();
		}
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

	it("deduplicates updater failures at the real capture boundary", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const failure = { phase: "download", error_category: "network", to_version: "2.0.0" };
		expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(true);
		expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(false);
		expect(await captureRendererEvent("ao.updates.downloaded", failure)).toBe(true);
		expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(true);
		expect(posthogStub.capture.mock.calls.map(([name]) => name)).toEqual([
			"ao.updates.failed", "ao.updates.downloaded", "ao.updates.failed",
		]);
	});

	it("does not deduplicate an update failure rejected by the rate limit", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const clock = vi.spyOn(Date, "now").mockReturnValue(1_000_000_000);
		try {
			for (let index = 0; index < 5; index++) {
				expect(await captureRendererEvent("ao.updates.failed", {
					phase: "download", error_category: "network", to_version: `2.0.${index}`,
				})).toBe(true);
			}
			const sixth = { phase: "download", error_category: "network", to_version: "2.0.5" };
			expect(await captureRendererEvent("ao.updates.failed", sixth)).toBe(false);
			clock.mockReturnValue(1_000_060_001);
			expect(await captureRendererEvent("ao.updates.failed", sixth)).toBe(true);
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
			expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(true);
			expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(false);
			vi.setSystemTime(new Date("2026-10-21T00:05:00.000Z"));
			expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(true);
			expect(posthogStub.capture.mock.calls.at(-1)?.[0]).toBe("ao.usage.update_failures_daily");
			expect(posthogStub.capture.mock.calls.at(-1)?.[1]).toMatchObject({ day: "2026-10-20", failure_count: 2 });
			expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(false);
		} finally {
			vi.useRealTimers();
		}
	});

	async function initializedWithLaunchStart() {
		localStorage.clear();
		mockPosthog();
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: {
					getBootstrap: vi.fn(async () => ({
						appVersion: "1.2.3", platform: "darwin", distinctId: "ins_test",
						disabledEvents: [], eventsEnabled: true, consentGeneration: "generation-1",
					})),
					takeLaunchStart: vi.fn(async () => ({ kind: "cold", startedAtMs: Date.now() - 500 })),
				},
				updateSettings: { get: vi.fn(async () => ({})) },
			},
		}));
		const telemetry = await import("./telemetry");
		const launch = await import("./launch-telemetry");
		expect(await telemetry.initTelemetry()).toBe(true);
		return { ...telemetry, markLaunchUsable: launch.markLaunchUsable };
	}
	const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
	const launchTimedCaptures = () => posthogStub.capture.mock.calls.filter(([event]) => event === "ao.launches.timed");

	it("sends launch timings once the window is usable", async () => {
		const { markLaunchUsable } = await initializedWithLaunchStart();
		markLaunchUsable();
		await settle();
		await settle();
		expect(launchTimedCaptures()).toHaveLength(1);
		expect(launchTimedCaptures()[0]![1]).toMatchObject({ launch_kind: "cold" });
	});

	it("does not send a launch measured before an opt-out after a later opt-in", async () => {
		const { applyAnalyticsOptOut, markLaunchUsable } = await initializedWithLaunchStart();
		await applyAnalyticsOptOut(true);
		await applyAnalyticsOptOut(false);
		markLaunchUsable();
		await settle();
		await settle();
		expect(launchTimedCaptures()).toHaveLength(0);
	});

	it("does not export updater failures counted while opted out", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const { applyAnalyticsOptOut } = await import("./telemetry");
		const failure = { phase: "check", error_category: "network" };
		vi.useFakeTimers();
		vi.setSystemTime(new Date("2026-10-20T12:00:00.000Z"));
		try {
			expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(true);
			await applyAnalyticsOptOut(true);
			posthogStub.capture.mockImplementation(() => undefined);
			expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(false);
			posthogStub.capture.mockImplementation(() => ({}));
			await applyAnalyticsOptOut(false);
			vi.setSystemTime(new Date("2026-10-21T00:05:00.000Z"));
			posthogStub.capture.mockClear();
			expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(true);
			expect(posthogStub.capture.mock.calls.map(([event]) => event)).toEqual(["ao.updates.failed"]);
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
			expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(true);
			vi.setSystemTime(new Date("2026-10-21T00:05:00.000Z"));
			posthogStub.capture.mockImplementationOnce(() => {
				void applyAnalyticsOptOut(true);
				return undefined;
			});
			expect(await captureRendererEvent("ao.updates.failed", failure)).toBe(false);
			expect(localStorage.getItem("ao.telemetry.updateFailures.v1")).toBeNull();
		} finally {
			vi.useRealTimers();
		}
	});

	it("allows retry after PostHog rejects or throws while capturing an update failure", async () => {
		const captureRendererEvent = await initializedUpdateCapture();
		const rejected = { phase: "download", error_category: "network", to_version: "2.0.0" };
		posthogStub.capture.mockImplementationOnce(() => undefined);
		expect(await captureRendererEvent("ao.updates.failed", rejected)).toBe(false);
		expect(await captureRendererEvent("ao.updates.failed", rejected)).toBe(true);

		const thrown = { phase: "download", error_category: "network", to_version: "2.0.1" };
		posthogStub.capture.mockImplementationOnce(() => { throw new Error("capture failed"); });
		await expect(captureRendererEvent("ao.updates.failed", thrown)).rejects.toThrow("capture failed");
		expect(await captureRendererEvent("ao.updates.failed", thrown)).toBe(true);
	});
});

describe("PostHog identity and opt-out", () => {
	const bootstrap = { appVersion: "1.2.3", platform: "darwin", distinctId: "ins_test", installId: "ins_test", disabledEvents: [], eventsEnabled: true, consentGeneration: "g" };

	function mockBridge(getBootstrap: () => Promise<unknown>, claimFirstOpen: () => Promise<string> = async () => "ineligible") {
		mockPosthog();
		const capture = vi.fn(async () => true);
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: { getBootstrap, getGithubIdentity: vi.fn(async () => ({ login: null, signedIn: false })), getShareIdentity: vi.fn(async () => false), getLifetimeSessions: vi.fn(async () => null), claimFirstOpen, capture },
				updateSettings: { get: vi.fn(async () => ({})) },
			},
		}));
		return capture;
	}

	beforeEach(() => {
		vi.resetModules();
		vi.clearAllMocks();
	});

	it("claims app open only when about to send it, retrying while the session count is unknown", async () => {
		localStorage.clear();
		vi.useFakeTimers();
		const installedAt = new Date("2026-10-20T10:00:00.000Z").getTime();
		vi.setSystemTime(new Date("2026-10-20T12:00:00.000Z"));
		try {
			const claim = vi.fn().mockResolvedValueOnce("unknown").mockResolvedValue("claimed");
			mockBridge(async () => ({ ...bootstrap, daysSinceInstall: 0, installTimestampMs: installedAt, lifetimeSessions: 0 }), claim);
			const { initTelemetry } = await import("./telemetry");
			await initTelemetry();
			await vi.advanceTimersByTimeAsync(0);
			expect(posthogStub.capture.mock.calls.some(([name]) => name === "ao.app.first_opened")).toBe(false);
			await vi.advanceTimersByTimeAsync(2_000);
			const sent = posthogStub.capture.mock.calls.filter(([name]) => name === "ao.app.first_opened");
			expect(sent).toHaveLength(1);
			expect(sent[0][1]).toEqual(expect.objectContaining({ days_since_install: 0 }));
			await vi.advanceTimersByTimeAsync(600_000);
			expect(claim).toHaveBeenCalledTimes(2);
			// Next-day return is computed in PostHog retention, not sent.
			expect(posthogStub.capture.mock.calls.some(([name]) => /next_day/.test(String(name)))).toBe(false);
		} finally {
			vi.useRealTimers();
		}
	});

	it("never spends the first-open claim when the event is on the kill switch, or after install day", async () => {
		localStorage.clear();
		const claim = vi.fn(async () => "claimed");
		mockBridge(async () => ({ ...bootstrap, daysSinceInstall: 0, disabledEvents: ["ao.app.first_opened"] }), claim);
		const { initTelemetry } = await import("./telemetry");
		await initTelemetry();
		await new Promise((resolve) => setTimeout(resolve, 0));
		vi.resetModules();
		mockBridge(async () => ({ ...bootstrap, daysSinceInstall: 3 }), claim);
		const later = await import("./telemetry");
		await later.initTelemetry();
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(claim).not.toHaveBeenCalled();
	});

	it("refreshes lifetime_sessions after the desktop creates a session", async () => {
		localStorage.clear();
		mockPosthog();
		const getLifetimeSessions = vi.fn(async () => 1);
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: { getBootstrap: vi.fn(async () => ({ ...bootstrap, lifetimeSessions: 0 })), getGithubIdentity: vi.fn(async () => ({ login: null, signedIn: false })), getShareIdentity: vi.fn(async () => false), getLifetimeSessions, claimFirstOpen: vi.fn(async () => "ineligible") },
				updateSettings: { get: vi.fn(async () => ({})) },
			},
		}));
		const { initTelemetry, captureRendererEvent, withTelemetryContext } = await import("./telemetry");
		await initTelemetry();
		expect(withTelemetryContext({})).toHaveProperty("lifetime_sessions", 0);
		await captureRendererEvent("ao.tasks.created", {});
		await vi.waitFor(() => expect(withTelemetryContext({})).toHaveProperty("lifetime_sessions", 1));
	});

	it("refreshes install age on captures from a long-lived renderer", async () => {
		localStorage.clear();
		vi.useFakeTimers();
		const installedAt = new Date("2026-10-20T10:00:00.000Z").getTime();
		vi.setSystemTime(new Date("2026-10-20T11:00:00.000Z"));
		try {
			mockBridge(async () => ({ ...bootstrap, daysSinceInstall: 0, installTimestampMs: installedAt }));
			const { captureRendererEvent, initTelemetry } = await import("./telemetry");
			await initTelemetry();
			expect(posthogStub.capture.mock.calls.at(-1)?.[1]).toHaveProperty("days_since_install", 0);
			posthogStub.capture.mockClear();
			vi.setSystemTime(new Date("2026-10-21T11:00:00.000Z"));
			expect(await captureRendererEvent("ao.app.loaded")).toBe(true);
			expect(posthogStub.capture.mock.calls.at(-1)?.[1]).toHaveProperty("days_since_install", 1);
		} finally {
			vi.useRealTimers();
		}
	});

	it("boots anonymous even with a bound Cloud user, and never identifies or aliases", async () => {
		localStorage.clear();
		mockBridge(async () => ({ ...bootstrap, distinctId: "anon_launch", cloudUserId: "user_01H" }));
		const { initTelemetry, identifyCloudUser, clearCloudUser, withTelemetryContext } = await import("./telemetry");
		await initTelemetry();
		expect(posthogStub.init).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ person_profiles: "never", bootstrap: { distinctID: "anon_launch", isIdentifiedID: false } }));
		await identifyCloudUser({ id: "user_01H", email: "dev@example.com" });
		expect(withTelemetryContext({})).toMatchObject({ is_signed_in: true, $process_person_profile: false, $geoip_disable: true, install_id: "ins_test" });
		clearCloudUser();
		expect(withTelemetryContext({})).toMatchObject({ is_signed_in: false, $process_person_profile: false });
		expect(posthogStub.identify).not.toHaveBeenCalled();
		expect(posthogStub.alias).not.toHaveBeenCalled();
		expect(posthogStub.reset).not.toHaveBeenCalled();
	});

	it("boots anonymous without a verified Cloud binding", async () => {
		localStorage.clear();
		mockBridge(async () => ({ ...bootstrap, distinctId: "anon_launch" }));
		const { initTelemetry, withTelemetryContext } = await import("./telemetry");
		await initTelemetry();
		expect(posthogStub.init).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ bootstrap: { distinctID: "anon_launch", isIdentifiedID: false } }));
		expect(withTelemetryContext({})).toMatchObject({ $process_person_profile: false });
	});

	it("fills lifetime_sessions in once the daemon answers when the bootstrap had none", async () => {
		localStorage.clear();
		vi.useFakeTimers();
		try {
			mockPosthog();
			const getLifetimeSessions = vi.fn().mockResolvedValueOnce(null).mockResolvedValue(7);
			vi.doMock("./bridge", () => ({
				aoBridge: {
					telemetry: { getBootstrap: vi.fn(async () => bootstrap), getGithubIdentity: vi.fn(async () => ({ login: null, signedIn: false })), getShareIdentity: vi.fn(async () => false), getLifetimeSessions },
					updateSettings: { get: vi.fn(async () => ({})) },
				},
			}));
			const { initTelemetry, withTelemetryContext } = await import("./telemetry");
			await initTelemetry();
			expect(withTelemetryContext({})).not.toHaveProperty("lifetime_sessions");
			await vi.advanceTimersByTimeAsync(2_000);
			expect(withTelemetryContext({})).not.toHaveProperty("lifetime_sessions");
			await vi.advanceTimersByTimeAsync(30_000);
			expect(withTelemetryContext({})).toHaveProperty("lifetime_sessions", 7);
			await vi.advanceTimersByTimeAsync(600_000);
			expect(getLifetimeSessions).toHaveBeenCalledTimes(2);
		} finally {
			vi.useRealTimers();
		}
	});

	it("pauses feature flag reloads on an in-session analytics opt-out and resumes on opt-in", async () => {
		localStorage.clear();
		mockBridge(async () => bootstrap);
		const { initTelemetry, applyAnalyticsOptOut } = await import("./telemetry");
		await initTelemetry();
		await applyAnalyticsOptOut(true);
		expect(posthogStub.set_config).toHaveBeenLastCalledWith({ advanced_disable_flags: true });
		expect(posthogStub.opt_out_capturing).toHaveBeenCalled();
		await applyAnalyticsOptOut(false);
		expect(posthogStub.set_config).toHaveBeenLastCalledWith({ advanced_disable_flags: false });
		expect(posthogStub.reloadFeatureFlags).toHaveBeenCalled();
	});

	it("stamps is_signed_in, days_since_install and lifetime_sessions on every event", async () => {
		localStorage.clear();
		mockBridge(async () => ({ ...bootstrap, installTimestampMs: Date.now() - 3 * 86_400_000, lifetimeSessions: 4 }));
		const { initTelemetry, identifyCloudUser, clearCloudUser, withTelemetryContext } = await import("./telemetry");
		await initTelemetry();
		expect(withTelemetryContext({})).toMatchObject({ is_signed_in: false, days_since_install: 3, lifetime_sessions: 4 });
		await identifyCloudUser({ id: "user_01H", email: "dev@example.com" });
		expect(withTelemetryContext({})).toHaveProperty("is_signed_in", true);
		clearCloudUser();
		expect(withTelemetryContext({})).toHaveProperty("is_signed_in", false);
	});

	it("never identifies the WorkOS user or sends the email; the user ID rides only while sharing identity", async () => {
		mockBridge(async () => bootstrap);
		const { initTelemetry, identifyCloudUser, applyShareIdentity, captureRendererEvent } = await import("./telemetry");
		await initTelemetry();
		await identifyCloudUser({ id: "user_01H", email: "dev@example.com" });
		expect(posthogStub.identify).not.toHaveBeenCalled();
		const registered = () => (posthogStub.register.mock.calls as unknown as [Record<string, unknown>][]).map(([props]) => props);
		for (const props of registered()) {
			expect(JSON.stringify(props)).not.toContain("dev@example.com");
			expect(props).not.toHaveProperty("ao_cloud_user_id");
		}
		expect(posthogStub.unregister).toHaveBeenCalledWith("ao_cloud_user_id");
		applyShareIdentity(true);
		expect(registered().at(-1)).toMatchObject({ ao_cloud_user_id: "user_01H", $process_person_profile: false });
		applyShareIdentity(false);
		expect(registered().at(-1)).not.toHaveProperty("ao_cloud_user_id");
		await captureRendererEvent("ao.support.opened");
		const captured = posthogStub.capture.mock.calls.at(-1) as unknown as [string, Record<string, unknown>];
		expect(JSON.stringify(captured)).not.toContain("dev@example.com");
		expect(captured[1]).toMatchObject({ $process_person_profile: false, $geoip_disable: true, install_id: "ins_test" });
		const init = posthogStub.init.mock.calls[0] as unknown as [string, { before_send: (event: Record<string, unknown>) => Record<string, unknown> }];
		// before_send is the last gate for SDK-generated events too.
		const sent = init[1].before_send({ event: "$pageleave", $set: { email: "x@y.z" }, properties: { install_id: "spoofed", $process_person_profile: true, $geoip_disable: false, $set: { a: 1 } } });
		expect(sent.properties).toMatchObject({ install_id: "ins_test", $process_person_profile: false, $geoip_disable: true });
		expect(sent.properties).not.toHaveProperty("$set");
		expect(sent).not.toHaveProperty("$set");
	});

	it("forwards renderer crashes even when PostHog initialization is unavailable", async () => {
		const captureToMain = mockBridge(async () => null);
		const { captureRendererException } = await import("./telemetry");
		await captureRendererException(new Error("resume agent ai-sports-coach-113"), { source: "react-error-boundary" });
		expect(captureToMain).toHaveBeenCalledWith(expect.objectContaining({ kind: "exception", tags: expect.objectContaining({ category: "render_crash" }) }));
		expect(posthogStub.init).not.toHaveBeenCalled();
		expect(posthogStub.captureException).not.toHaveBeenCalled();
	});

	it("honors the remote exception deny list before forwarding to Electron", async () => {
		const captureToMain = mockBridge(async () => ({ ...bootstrap, disabledEvents: ["$exception"] }));
		const { captureRendererException } = await import("./telemetry");
		await captureRendererException(new TypeError("private project"));
		expect(captureToMain).not.toHaveBeenCalled();
	});

	it("keeps events anonymous before sign-in and after sign-out", async () => {
		mockBridge(async () => bootstrap);
		const { identifyCloudUser, clearCloudUser, captureRendererEvent } = await import("./telemetry");
		await captureRendererEvent("ao.support.opened");
		expect((posthogStub.capture.mock.calls.at(-1) as unknown as [string, Record<string, unknown>])[1]).toMatchObject({ $process_person_profile: false, install_id: "ins_test" });

		await identifyCloudUser({ id: "user_01H", email: "dev@example.com" });
		clearCloudUser();
		expect(posthogStub.reset).not.toHaveBeenCalled();
		await captureRendererEvent("ao.mobile.connect_opened", { bridge_enabled: true });
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

	it("opt-out stops capture and resets the SDK identity; a later sign-in does not identify", async () => {
		mockBridge(async () => bootstrap);
		const { initTelemetry, identifyCloudUser, applyAnalyticsOptOut, captureRendererEvent } = await import("./telemetry");
		await initTelemetry();
		await applyAnalyticsOptOut(true);
		expect(posthogStub.opt_out_capturing).toHaveBeenCalledTimes(1);
		expect(posthogStub.reset).toHaveBeenCalledTimes(1);

		await identifyCloudUser({ id: "user_01H", email: "dev@example.com" });

		// Opting back in resumes capture, still without identifying anyone.
		await applyAnalyticsOptOut(false);
		expect(posthogStub.opt_in_capturing).toHaveBeenLastCalledWith({ captureEventName: false });
		expect(posthogStub.identify).not.toHaveBeenCalled();
		await captureRendererEvent("ao.support.opened", { install_id: "spoofed" });
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
				telemetry: { getBootstrap: vi.fn(async () => bootstrap), getGithubIdentity: vi.fn(async () => ({ login: null, signedIn: false })), getShareIdentity: vi.fn(async () => false) },
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
				telemetry: { getBootstrap: vi.fn(async () => bootstrap), getGithubIdentity: vi.fn(async () => ({ login: null, signedIn: false })), getShareIdentity: vi.fn(async () => false) },
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

	it("hands each event, exactly as sent, to the local telemetry log", async () => {
		const recordExported = vi.fn();
		mockPosthog();
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: { getBootstrap: vi.fn(async () => bootstrap), getGithubIdentity: vi.fn(async () => ({ login: null, signedIn: false })), getShareIdentity: vi.fn(async () => false), getLifetimeSessions: vi.fn(async () => null), claimFirstOpen: vi.fn(async () => "ineligible"), capture: vi.fn(async () => true), recordExported },
				updateSettings: { get: vi.fn(async () => ({})) },
			},
		}));
		const { initTelemetry, applyAnalyticsOptOut } = await import("./telemetry");
		await initTelemetry();
		const init = posthogStub.init.mock.calls[0] as unknown as [string, { before_send: (event: Record<string, unknown>) => Record<string, unknown> }];
		const sent = init[1].before_send({ event: "ao.routes.viewed", properties: { surface: "board" } });
		expect(recordExported).toHaveBeenLastCalledWith({ event: "ao.routes.viewed", distinctId: expect.any(String), properties: sent.properties });
		recordExported.mockClear();
		await applyAnalyticsOptOut(true);
		init[1].before_send({ event: "ao.routes.viewed", properties: {} });
		expect(recordExported).not.toHaveBeenCalled();
	});

	it("attaches a Cloud user noted before init once the loaded preference shares identity", async () => {
		mockPosthog();
		vi.doMock("./bridge", () => ({
			aoBridge: {
				telemetry: { getBootstrap: vi.fn(async () => bootstrap), getGithubIdentity: vi.fn(async () => ({ login: null, signedIn: false })), getShareIdentity: vi.fn(async () => true), getLifetimeSessions: vi.fn(async () => null), claimFirstOpen: vi.fn(async () => "ineligible"), capture: vi.fn(async () => true) },
				updateSettings: { get: vi.fn(async () => ({})) },
			},
		}));
		const { identifyCloudUser, initTelemetry } = await import("./telemetry");
		await identifyCloudUser({ id: "user_01H" });
		await initTelemetry();
		const registered = (posthogStub.register.mock.calls as unknown as [Record<string, unknown>][]).map(([props]) => props);
		expect(registered.at(-1)).toMatchObject({ ao_cloud_user_id: "user_01H" });
	});

	// The website visitor id a sign-in may carry is never aliased into anything.
	it("are never linked: no alias and no link event", async () => {
		mockBridge(async () => bootstrap);
		const telemetry = await import("./telemetry");
		await telemetry.initTelemetry();
		await telemetry.identifyCloudUser({ id: "user_A", email: "a@example.com" });
		expect(telemetry).not.toHaveProperty("noteCloudSignInWebVisitor");
		expect(posthogStub.alias).not.toHaveBeenCalled();
		expect(posthogStub.capture.mock.calls.map(([name]) => name)).not.toContain("ao.renderer.cloud_web_visitor_link_requested");
	});
});

