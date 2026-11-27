import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Exit feedback events through the real telemetry module and the real PostHog
// SDK. Network is never reached: the SDK's own request function is replaced and
// the bridge is a fake, so nothing goes to production PostHog.

type Sent = { url: string; data: unknown };

async function loadTelemetry(options: { disabledEvents?: string[] } = {}) {
	localStorage.clear();
	const sdkRequests: Sent[] = [];
	const handedOff: Array<Record<string, unknown>> = [];
	vi.doMock("posthog-js/dist/module.full.no-external", async (importOriginal) => {
		const actual = await importOriginal<typeof import("posthog-js/dist/module.full.no-external")>();
		const client = new actual.PostHog();
		(client as unknown as { _send_request: (options: { url: string; data?: unknown }) => void })._send_request = (request) => {
			sdkRequests.push({ url: request.url, data: request.data });
		};
		return { ...actual, default: client };
	});
	vi.doMock("./bridge", () => ({
		aoBridge: {
			telemetry: {
				getBootstrap: vi.fn(async () => ({
					appVersion: "1.2.3", platform: "darwin", distinctId: "ins_exit-test", installId: "ins_exit-test",
					disabledEvents: options.disabledEvents ?? [], eventsEnabled: true, consentGeneration: "generation-1",
				})),
				getGithubLogin: vi.fn(async () => null),
				getLifetimeSessions: vi.fn(async () => undefined),
				claimFirstOpen: vi.fn(async () => "ineligible"),
				sendExitFeedbackEvent: vi.fn((event: Record<string, unknown>) => handedOff.push(event)),
			},
			updateSettings: { get: vi.fn(async () => ({})) },
		},
	}));
	const telemetry = await import("./telemetry");
	return { telemetry, sdkRequests, handedOff };
}

const sdkEvents = (requests: Sent[]) =>
	requests
		.filter((request) => request.data !== undefined && !request.url.includes("/flags"))
		.flatMap((request) => (Array.isArray(request.data) ? request.data : [request.data])) as Array<Record<string, unknown>>;

describe("exit feedback events with the real SDK", () => {
	beforeEach(() => {
		vi.resetModules();
		vi.clearAllMocks();
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "Date"] });
	});
	afterEach(() => vi.useRealTimers());

	it("hands each event to main with a valid ISO timestamp and the usual context, never through the SDK batch", async () => {
		const { telemetry, sdkRequests, handedOff } = await loadTelemetry();
		expect(await telemetry.initTelemetry()).toBe(true);
		expect(await telemetry.exitFeedbackAllowed()).toBe(true);
		expect(await telemetry.captureRendererEvent("ao.exit_feedback.shown", { version: "no_project" })).toBe(true);
		expect(await telemetry.captureRendererEvent("ao.exit_feedback.answered", {
			version: "no_project", answer: "something_broke", seconds_open: 4.7,
			text: "  it crashed in /Users/alex/code/secret-repo and http://localhost:3000/x  ",
			extra: "must not ride along",
		})).toBe(true);
		expect(await telemetry.captureRendererEvent("ao.exit_feedback.dismissed", { version: "no_project", how: "timeout", seconds_open: 15, text: "unsent" })).toBe(true);
		await vi.advanceTimersByTimeAsync(10_000);

		expect(handedOff.map((event) => event.event)).toEqual(["ao.exit_feedback.shown", "ao.exit_feedback.answered", "ao.exit_feedback.dismissed"]);
		for (const event of handedOff) {
			expect(typeof event.timestamp, JSON.stringify(event.timestamp)).toBe("string");
			expect(new Date(event.timestamp as string).toISOString()).toBe(event.timestamp);
			const properties = event.properties as Record<string, unknown>;
			expect(properties.install_id).toBe("ins_exit-test");
			expect(properties.app_version).toBe("1.2.3");
			expect(properties.platform).toBe("darwin");
			expect(properties.distinct_id).toBe("ins_exit-test");
			expect(typeof properties.token).toBe("string");
		}
		const answered = handedOff[1]!.properties as Record<string, unknown>;
		expect(answered).toMatchObject({ version: "no_project", answer: "something_broke", has_text: true, seconds_open: 4 });
		expect(answered.text).toBe("it crashed in [redacted-local-path] and [redacted-local-url]");
		expect(answered).not.toHaveProperty("extra");
		const dismissed = handedOff[2]!.properties as Record<string, unknown>;
		expect(dismissed).toMatchObject({ how: "timeout", seconds_open: 15 });
		expect(dismissed).not.toHaveProperty("text");
		expect(JSON.stringify(handedOff)).not.toContain("/Users/alex");

		// The fake transport does see ordinary batched events, so the check below means something.
		expect(sdkEvents(sdkRequests).some((event) => event.event === "ao.app.loaded")).toBe(true);
		expect(sdkEvents(sdkRequests).filter((event) => String(event.event).startsWith("ao.exit_feedback."))).toEqual([]);
	});

	it("does not show or send while usage analytics is off", async () => {
		const { telemetry, sdkRequests, handedOff } = await loadTelemetry();
		await telemetry.applyAnalyticsOptOut(true);
		expect(await telemetry.exitFeedbackAllowed()).toBe(false);
		expect(await telemetry.captureRendererEvent("ao.exit_feedback.answered", { version: "no_project", answer: "not_a_fit" })).toBe(false);
		await vi.advanceTimersByTimeAsync(10_000);
		expect(handedOff).toEqual([]);
		expect(sdkEvents(sdkRequests)).toEqual([]);
	});

	it("does not show when the kill switch silences any of its events", async () => {
		const { telemetry, handedOff } = await loadTelemetry({ disabledEvents: ["ao.exit_feedback.*"] });
		expect(await telemetry.exitFeedbackAllowed()).toBe(false);
		expect(await telemetry.captureRendererEvent("ao.exit_feedback.shown", { version: "no_project" })).toBe(false);
		expect(handedOff).toEqual([]);
	});
});

describe("sanitizeExitFeedbackProperties", () => {
	beforeEach(() => vi.resetModules());

	it("keeps only the fixed vocabulary and caps text at 500 characters", async () => {
		const { sanitizeExitFeedbackProperties } = await import("./telemetry");
		expect(sanitizeExitFeedbackProperties("ao.exit_feedback.shown", { version: "other" })).toEqual({});
		expect(sanitizeExitFeedbackProperties("ao.exit_feedback.shown", { version: "project_no_work", answer: "x" })).toEqual({ version: "project_no_work" });
		// An answer from the other version is not accepted.
		expect(sanitizeExitFeedbackProperties("ao.exit_feedback.answered", { version: "project_no_work", answer: "just_looking", text: "kept out" }))
			.toEqual({ version: "project_no_work" });
		expect(sanitizeExitFeedbackProperties("ao.exit_feedback.answered", { version: "project_no_work", answer: "not_comfortable", text: "   " }))
			.toEqual({ version: "project_no_work", answer: "not_comfortable", has_text: false });
		expect(sanitizeExitFeedbackProperties("ao.exit_feedback.dismissed", { version: "no_project", how: "closed", seconds_open: -1 })).toEqual({ version: "no_project" });
		const long = sanitizeExitFeedbackProperties("ao.exit_feedback.answered", { version: "no_project", answer: "not_a_fit", text: "a".repeat(900) });
		expect((long.text as string).length).toBe(500);
		const windowsPath = sanitizeExitFeedbackProperties("ao.exit_feedback.answered", { version: "no_project", answer: "not_a_fit", text: "see C:\\Users\\me\\repo now" });
		expect(windowsPath.text).toBe("see [redacted-local-path] now");
		const forwardSlash = sanitizeExitFeedbackProperties("ao.exit_feedback.answered", { version: "no_project", answer: "not_a_fit", text: "Could not open C:/work/private-repo/file.txt" });
		expect(forwardSlash.text).toBe("Could not open [redacted-local-path]");
	});
});
