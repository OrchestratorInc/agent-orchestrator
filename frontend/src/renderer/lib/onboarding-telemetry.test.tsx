import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const capture = vi.hoisted(() => vi.fn(async (_event: string, _props?: unknown) => undefined));
// The window-close path, recorded in the same list so event order reads across both.
const unload = vi.hoisted(() => vi.fn((event: string, props?: unknown) => {
	void capture(event, props);
	return true;
}));
vi.mock("./telemetry", () => ({ captureRendererEvent: capture, captureRendererEventOnUnload: unload }));
const sentry = vi.hoisted(() => ({
	failure: vi.fn((_action: string, _reason: string) => undefined),
	flowSource: { current: (() => undefined) as () => string | undefined },
}));
vi.mock("./sentry", () => ({
	captureOnboardingFailureToSentry: sentry.failure,
	setSentryFlowSource: (source: () => string | undefined) => { sentry.flowSource.current = source; },
}));

import { setAnalyticsOptedOut } from "./analytics-opt-out";
import { notifyComposerSent } from "./guided-tour-signals";
import {
	beginOnboardingRun,
	completeOnboardingRun,
	completeOnboardingStepIfViewed,
	endOnboardingReplay,
	ensureOnboardingStepViewed,
	finishOnboardingAction,
	installJobOutcome,
	onboardingActionAttempt,
	onboardingInProgress,
	onboardingProjectSource,
	recordOnboardingActivation,
	reportOnboardingBack,
	reportOnboardingProjectAdded,
	reportOnboardingStep,
	resetOnboardingTelemetryForTests,
	setOnboardingTourStep,
	skipOnboardingStep,
	startOnboardingAction,
	supersedeOnboardingRun,
	useOnboardingActivation,
	useOnboardingStep,
} from "./onboarding-telemetry";

const events = () => capture.mock.calls.map((call) => call[0]);
const lastProps = (event: string) => [...capture.mock.calls].reverse().find((call) => call[0] === event)?.[1] as Record<string, unknown> | undefined;

describe("onboarding step telemetry", () => {
	beforeEach(() => {
		capture.mockClear();
		unload.mockClear();
		setAnalyticsOptedOut(false);
		window.localStorage.clear();
		resetOnboardingTelemetryForTests();
	});

	it("reports each step phase once per install", () => {
		reportOnboardingStep("welcome", "viewed");
		reportOnboardingStep("welcome", "viewed");
		expect(capture.mock.calls).toEqual([["ao.onboarding.step_viewed", { run_kind: "first_run", step: "welcome" }]]);
		// The record keeps the format earlier builds wrote, so an upgrade does not re-send.
		expect(JSON.parse(window.localStorage.getItem("ao.telemetry.onboardingSteps") ?? "[]")).toEqual(["welcome:viewed"]);
	});

	it("measures time on step from its latest view", () => {
		vi.useFakeTimers();
		try {
			reportOnboardingStep("sign_in", "viewed");
			vi.advanceTimersByTime(4_000);
			reportOnboardingStep("sign_in", "completed", { extra: 1 });
			expect(lastProps("ao.onboarding.step_completed")).toEqual({ run_kind: "first_run", step: "sign_in", extra: 1, time_on_step_ms: 4_000 });
		} finally {
			vi.useRealTimers();
		}
	});

	it("completes github_auth once it was viewed, even after the window closed", () => {
		completeOnboardingStepIfViewed("github_auth");
		expect(capture).not.toHaveBeenCalled();
		reportOnboardingStep("github_auth", "viewed");
		reportOnboardingStep("github_auth", "abandoned");
		completeOnboardingStepIfViewed("github_auth");
		completeOnboardingStepIfViewed("github_auth");
		expect(events()).toEqual(["ao.onboarding.step_viewed", "ao.onboarding.step_abandoned", "ao.onboarding.step_completed"]);
	});

	it("does not call a completed step abandoned", () => {
		reportOnboardingStep("github_auth", "completed");
		reportOnboardingStep("github_auth", "abandoned");
		expect(events()).toEqual(["ao.onboarding.step_completed"]);
	});

	it("views while active, abandons on window close, completes when the step clears", () => {
		const { rerender } = renderHook(({ active }) => useOnboardingStep("github_auth", active), { initialProps: { active: true } });
		expect(events()).toEqual(["ao.onboarding.step_viewed"]);
		act(() => rerender({ active: false }));
		expect(events()).toEqual(["ao.onboarding.step_viewed", "ao.onboarding.step_completed"]);
		act(() => {
			window.dispatchEvent(new Event("pagehide"));
		});
		expect(capture).toHaveBeenCalledTimes(2);
	});

	it("leaves a page that can be left by Back for its own completion", () => {
		const { rerender } = renderHook(({ active }) => useOnboardingStep("sign_in", active, { autoComplete: false, viewed: { missing: "gh" } }), {
			initialProps: { active: true },
		});
		act(() => rerender({ active: false }));
		expect(capture.mock.calls).toEqual([["ao.onboarding.step_viewed", { run_kind: "first_run", step: "sign_in", missing: "gh" }]]);
	});

	it("abandons when the window closes mid-step, sent as the page goes away", () => {
		renderHook(() => useOnboardingStep("welcome", true));
		act(() => {
			window.dispatchEvent(new Event("pagehide"));
		});
		expect(events()).toEqual(["ao.onboarding.step_viewed", "ao.onboarding.step_abandoned"]);
		// A queued event would be lost with the page; only abandoned takes the unload path.
		expect(unload.mock.calls).toEqual([["ao.onboarding.step_abandoned", expect.objectContaining({ run_kind: "first_run", step: "welcome" })]]);
	});

	it("holds back only the view while it is not ready, timing the step from when it opened", () => {
		vi.useFakeTimers();
		try {
			const { rerender } = renderHook(({ ready }) => useOnboardingStep("tools", true, { autoComplete: false, viewReady: ready, viewed: { missing: "gh" } }), {
				initialProps: { ready: false },
			});
			expect(capture).not.toHaveBeenCalled();
			vi.advanceTimersByTime(2_000);
			act(() => rerender({ ready: true }));
			expect(capture.mock.calls).toEqual([["ao.onboarding.step_viewed", { run_kind: "first_run", step: "tools", missing: "gh" }]]);
			vi.advanceTimersByTime(1_000);
			act(() => {
				window.dispatchEvent(new Event("pagehide"));
			});
			expect(events()).toEqual(["ao.onboarding.step_viewed", "ao.onboarding.step_abandoned"]);
			expect(lastProps("ao.onboarding.step_abandoned")).toMatchObject({ time_on_step_ms: 3_000 });
		} finally {
			vi.useRealTimers();
		}
	});

	it("still reports a step closed a second after it opened, before its checks answered", () => {
		vi.useFakeTimers();
		try {
			renderHook(() => useOnboardingStep("tools", true, { autoComplete: false, viewReady: false, viewed: { missing: "gh" } }));
			vi.advanceTimersByTime(1_000);
			act(() => {
				window.dispatchEvent(new Event("pagehide"));
			});
			// Both go out as the page closes; a queued view would be lost with it.
			expect(unload.mock.calls).toEqual([
				["ao.onboarding.step_viewed", { run_kind: "first_run", step: "tools", missing: "gh" }],
				["ao.onboarding.step_abandoned", { run_kind: "first_run", step: "tools", time_on_step_ms: 1_000 }],
			]);
		} finally {
			vi.useRealTimers();
		}
	});

	it("counts an automatic skip once per install, and a person's skip and Back per click", () => {
		skipOnboardingStep("sign_in", "already_done");
		skipOnboardingStep("sign_in", "already_done");
		skipOnboardingStep("sign_in", "user");
		skipOnboardingStep("sign_in", "user");
		reportOnboardingBack("tools", "sign_in");
		reportOnboardingBack("tools", "sign_in");
		expect(events()).toEqual([
			"ao.onboarding.step_skipped",
			"ao.onboarding.step_skipped",
			"ao.onboarding.step_skipped",
			"ao.onboarding.back_clicked",
			"ao.onboarding.back_clicked",
		]);
		expect(lastProps("ao.onboarding.back_clicked")).toEqual({ run_kind: "first_run", step: "tools", to_step: "sign_in" });
	});
});

describe("onboarding run telemetry", () => {
	beforeEach(() => {
		capture.mockClear();
		unload.mockClear();
		setAnalyticsOptedOut(false);
		window.localStorage.clear();
		resetOnboardingTelemetryForTests();
	});
	afterEach(() => vi.useRealTimers());

	it("starts once per install and completes once with the run's totals", () => {
		vi.useFakeTimers();
		const end = beginOnboardingRun("first_run", "welcome");
		beginOnboardingRun("first_run", "welcome");
		reportOnboardingStep("welcome", "viewed");
		skipOnboardingStep("sign_in", "already_done");
		reportOnboardingStep("tools", "viewed");
		reportOnboardingBack("tools", "welcome");
		vi.advanceTimersByTime(5 * 60_000);
		completeOnboardingRun("finished");
		completeOnboardingRun("finished");
		end();
		expect(events().filter((event) => event === "ao.onboarding.started")).toHaveLength(1);
		expect(events().filter((event) => event === "ao.onboarding.completed")).toHaveLength(1);
		expect(lastProps("ao.onboarding.completed")).toEqual({
			run_kind: "first_run",
			step: "tools",
			outcome: "finished",
			duration_ms: 5 * 60_000,
			launch_count: 1,
			resumed: false,
			back_count: 1,
			auto_skipped_count: 1,
		});
	});

	it("reports a run reopened on a later launch as resumed, with the time away", async () => {
		vi.useFakeTimers();
		beginOnboardingRun("first_run", "welcome");
		reportOnboardingStep("sign_in", "viewed");
		vi.advanceTimersByTime(2 * 3_600_000);
		// The next launch loads the module afresh.
		vi.resetModules();
		const next = await import("./onboarding-telemetry");
		capture.mockClear();
		next.beginOnboardingRun("first_run", "sign_in");
		expect(capture.mock.calls).toEqual([["ao.onboarding.resumed", { run_kind: "first_run", step: "sign_in", away_ms: 2 * 3_600_000, launch_count: 2 }]]);
		next.completeOnboardingRun("finished");
		expect(lastProps("ao.onboarding.completed")).toMatchObject({ resumed: true, launch_count: 2, duration_ms: 2 * 3_600_000 });
	});

	it("abandons once per launch when the window closes mid-flow, and not after it closed", () => {
		const end = beginOnboardingRun("first_run", "welcome");
		reportOnboardingStep("tools", "viewed");
		window.dispatchEvent(new Event("pagehide"));
		window.dispatchEvent(new Event("pagehide"));
		expect(events().filter((event) => event === "ao.onboarding.abandoned")).toHaveLength(1);
		expect(unload).toHaveBeenCalledWith("ao.onboarding.abandoned", expect.objectContaining({ step: "tools" }));
		expect(lastProps("ao.onboarding.abandoned")).toMatchObject({ run_kind: "first_run", step: "tools", launch_count: 1 });
		end();
		resetOnboardingTelemetryForTests();
		window.dispatchEvent(new Event("pagehide"));
		expect(events().filter((event) => event === "ao.onboarding.abandoned")).toHaveLength(1);
	});

	it("keeps the run on its page while a sub-step inside it shows", () => {
		beginOnboardingRun("first_run", "welcome");
		reportOnboardingStep("tools", "viewed");
		// github_auth and dependencies show inside a page or before the flow.
		reportOnboardingStep("github_auth", "viewed");
		reportOnboardingStep("dependencies", "viewed");
		completeOnboardingRun("finished");
		expect(lastProps("ao.onboarding.completed")).toMatchObject({ step: "tools" });
	});

	it("says onboarding is showing while its window or a tour step is", () => {
		expect(onboardingInProgress()).toBe(false);
		const end = beginOnboardingRun("first_run", "welcome");
		expect(onboardingInProgress()).toBe(true);
		end();
		expect(onboardingInProgress()).toBe(false);
		setOnboardingTourStep("tour_project");
		expect(onboardingInProgress()).toBe(true);
		setOnboardingTourStep(undefined);
		expect(onboardingInProgress()).toBe(false);
	});

	it("ends a run left open when a project made onboarding unnecessary, and nothing for one never shown", () => {
		supersedeOnboardingRun();
		expect(capture).not.toHaveBeenCalled();
		beginOnboardingRun("first_run", "welcome")();
		supersedeOnboardingRun();
		expect(lastProps("ao.onboarding.completed")).toMatchObject({ outcome: "superseded" });
	});

	it("still completes a run when storage refuses writes", () => {
		const setItem = vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
			throw new Error("QuotaExceededError");
		});
		try {
			beginOnboardingRun("first_run", "welcome");
			reportOnboardingStep("welcome", "viewed");
			reportOnboardingStep("welcome", "viewed");
			completeOnboardingRun("finished");
			expect(events()).toEqual(["ao.onboarding.started", "ao.onboarding.step_viewed", "ao.onboarding.completed"]);
		} finally {
			setItem.mockRestore();
		}
	});

	it("reports a view before a completion that came first, and only once", () => {
		ensureOnboardingStepViewed("tour_project");
		ensureOnboardingStepViewed("tour_project");
		reportOnboardingStep("tour_project", "completed");
		expect(events()).toEqual(["ao.onboarding.step_viewed", "ao.onboarding.step_completed"]);
	});

	it("gives each replay its own run and its own step dedupe", () => {
		beginOnboardingRun("first_run", "welcome");
		reportOnboardingStep("welcome", "viewed");
		completeOnboardingRun("finished");
		capture.mockClear();
		beginOnboardingRun("replay", "welcome");
		reportOnboardingStep("welcome", "viewed");
		completeOnboardingRun("skipped_setup");
		beginOnboardingRun("replay", "welcome");
		reportOnboardingStep("welcome", "viewed");
		expect(capture.mock.calls).toEqual([
			["ao.onboarding.started", { run_kind: "replay", step: "welcome" }],
			["ao.onboarding.step_viewed", { run_kind: "replay", step: "welcome" }],
			["ao.onboarding.completed", expect.objectContaining({ run_kind: "replay", outcome: "skipped_setup" })],
			["ao.onboarding.started", { run_kind: "replay", step: "welcome" }],
			["ao.onboarding.step_viewed", { run_kind: "replay", step: "welcome" }],
		]);
	});
});

describe("onboarding action telemetry", () => {
	beforeEach(() => {
		capture.mockClear();
		unload.mockClear();
		setAnalyticsOptedOut(false);
		window.localStorage.clear();
		resetOnboardingTelemetryForTests();
	});

	it("counts attempts and finishes each one once", () => {
		startOnboardingAction("agent_install", { target: "codex", step: "tools" });
		expect(finishOnboardingAction("agent_install", "failed", { target: "codex", reason: "install_failed" })).toBe(true);
		expect(finishOnboardingAction("agent_install", "failed", { target: "codex", reason: "install_failed" })).toBe(false);
		startOnboardingAction("agent_install", { target: "codex", step: "tools" });
		finishOnboardingAction("agent_install", "succeeded", { target: "codex" });
		const finished = capture.mock.calls.filter((call) => call[0] === "ao.onboarding.action_finished").map((call) => call[1]);
		expect(finished).toEqual([
			expect.objectContaining({ action: "agent_install", target: "codex", step: "tools", attempt: 1, outcome: "failed", reason: "install_failed" }),
			expect.objectContaining({ action: "agent_install", target: "codex", step: "tools", attempt: 2, outcome: "succeeded" }),
		]);
	});

	it("does not finish an action that was never started", () => {
		expect(finishOnboardingAction("gh_sign_in", "succeeded", { target: "gh" })).toBe(false);
		expect(capture).not.toHaveBeenCalled();
	});

	it("maps install job end states", () => {
		expect(installJobOutcome("succeeded")).toEqual({ outcome: "succeeded" });
		expect(installJobOutcome("failed")).toEqual({ outcome: "failed", reason: "install_failed" });
		expect(installJobOutcome("interrupted")).toEqual({ outcome: "failed", reason: "interrupted" });
		expect(installJobOutcome("unsupported")).toEqual({ outcome: "failed", reason: "unsupported" });
		expect(installJobOutcome("running")).toBeNull();
	});

	it("only notes a project for the tour; ao.projects.* already reports its result", () => {
		setOnboardingTourStep("tour_project");
		reportOnboardingProjectAdded("clone", "repo-a");
		expect(capture).not.toHaveBeenCalled();
		expect(onboardingProjectSource("repo-a", true)).toBe("clone");
	});

	it("reads where the tour's project came from, for that project only", () => {
		reportOnboardingProjectAdded("local_folder", "repo-a");
		// Another project opened meanwhile keeps its own answer, and leaves the note.
		expect(onboardingProjectSource("repo-b", false)).toBe("existing");
		expect(onboardingProjectSource("repo-a", true)).toBe("local_folder");
		// The note is used up.
		expect(onboardingProjectSource("repo-a", true)).toBe("other");
		expect(onboardingProjectSource(undefined, false)).toBe("existing");
	});

	it("lets a check finish only the attempt it was started for", () => {
		startOnboardingAction("gh_sign_in", { target: "gh", step: "github_auth" });
		const first = onboardingActionAttempt("gh_sign_in", "gh");
		// A retry starts before the first attempt's check answers.
		startOnboardingAction("gh_sign_in", { target: "gh", step: "github_auth" });
		expect(finishOnboardingAction("gh_sign_in", "failed", { target: "gh", reason: "not_signed_in", attempt: first })).toBe(false);
		expect(finishOnboardingAction("gh_sign_in", "succeeded", { target: "gh", attempt: onboardingActionAttempt("gh_sign_in", "gh") })).toBe(true);
		expect(lastProps("ao.onboarding.action_finished")).toMatchObject({ attempt: 2, outcome: "succeeded" });
	});
});

describe("onboarding activation telemetry", () => {
	beforeEach(() => {
		capture.mockClear();
		unload.mockClear();
		setAnalyticsOptedOut(false);
		window.localStorage.clear();
		resetOnboardingTelemetryForTests();
	});
	afterEach(() => vi.useRealTimers());

	it("reports the first message after a first run once, with the time since it finished", () => {
		vi.useFakeTimers();
		renderHook(() => useOnboardingActivation(() => true));
		notifyComposerSent();
		expect(capture).not.toHaveBeenCalled();
		beginOnboardingRun("first_run", "welcome");
		completeOnboardingRun("finished");
		capture.mockClear();
		vi.advanceTimersByTime(3 * 60_000);
		notifyComposerSent();
		notifyComposerSent();
		expect(capture.mock.calls).toEqual([["ao.onboarding.activated", { milestone: "first_message", time_since_completed_ms: 3 * 60_000, during_tour: true }]]);
	});

	it("stops looking a week after onboarding", () => {
		beginOnboardingRun("first_run", "welcome");
		completeOnboardingRun("finished");
		capture.mockClear();
		recordOnboardingActivation(false, Date.now() + 8 * 24 * 3_600_000);
		expect(capture).not.toHaveBeenCalled();
	});

	it("does not count a message during a replay as activation", () => {
		// A finished, not yet activated first run, so only the replay guard stops it.
		beginOnboardingRun("first_run", "welcome")();
		completeOnboardingRun("finished");
		beginOnboardingRun("replay", "welcome");
		completeOnboardingRun("finished");
		capture.mockClear();
		recordOnboardingActivation(true);
		expect(capture).not.toHaveBeenCalled();
		// Its tour ended: the next message counts for the first run.
		endOnboardingReplay();
		recordOnboardingActivation(false);
		expect(events()).toEqual(["ao.onboarding.activated"]);
	});

	it("ends a replay whose setup was skipped, so later messages count again", () => {
		beginOnboardingRun("first_run", "welcome")();
		completeOnboardingRun("finished");
		beginOnboardingRun("replay", "welcome")();
		completeOnboardingRun("skipped_setup");
		capture.mockClear();
		recordOnboardingActivation(false);
		expect(capture.mock.calls).toEqual([["ao.onboarding.activated", expect.objectContaining({ during_tour: false })]]);
	});
});

describe("onboarding telemetry while opted out", () => {
	beforeEach(() => {
		capture.mockClear();
		unload.mockClear();
		setAnalyticsOptedOut(false);
		window.localStorage.clear();
		resetOnboardingTelemetryForTests();
	});
	afterEach(() => {
		setAnalyticsOptedOut(false);
		vi.useRealTimers();
	});

	it("counts nothing while opted out, and drops the run and attempts held before it", () => {
		vi.useFakeTimers();
		beginOnboardingRun("first_run", "welcome");
		startOnboardingAction("agent_install", { target: "codex", step: "tools" });
		capture.mockClear();
		setAnalyticsOptedOut(true);
		reportOnboardingStep("tools", "viewed");
		reportOnboardingBack("tools", "welcome");
		skipOnboardingStep("sign_in", "already_done");
		startOnboardingAction("agent_install", { target: "codex", step: "tools" });
		vi.advanceTimersByTime(21 * 60_000);
		window.dispatchEvent(new Event("pagehide"));
		expect(capture).not.toHaveBeenCalled();
		expect(window.localStorage.getItem("ao.telemetry.onboardingRun")).toBeNull();

		setAnalyticsOptedOut(false);
		// No completion without its start, and nothing from the opted-out period.
		completeOnboardingRun("finished");
		expect(events()).not.toContain("ao.onboarding.completed");
		// The pending attempt went with the opt-out, and the count starts again.
		expect(finishOnboardingAction("agent_install", "succeeded", { target: "codex" })).toBe(false);
		startOnboardingAction("agent_install", { target: "codex", step: "tools" });
		expect(lastProps("ao.onboarding.action_started")).toMatchObject({ attempt: 1 });
		// A step viewed while opted out was not used up.
		reportOnboardingStep("tools", "viewed");
		expect(events()).toContain("ao.onboarding.step_viewed");
	});

	it("does not bring back a run that storage failed to remove at the opt-out", () => {
		beginOnboardingRun("first_run", "welcome");
		// The test setup installs its own localStorage, so the spy goes on that object.
		const removeItem = vi.spyOn(window.localStorage, "removeItem").mockImplementation(() => {
			throw new Error("SecurityError");
		});
		try {
			setAnalyticsOptedOut(true);
		} finally {
			removeItem.mockRestore();
		}
		expect(window.localStorage.getItem("ao.telemetry.onboardingRun")).not.toBeNull();
		setAnalyticsOptedOut(false);
		capture.mockClear();
		completeOnboardingRun("finished");
		expect(capture).not.toHaveBeenCalled();
	});

	it("does not note a project added while opted out", () => {
		setAnalyticsOptedOut(true);
		reportOnboardingProjectAdded("clone", "repo-a");
		setAnalyticsOptedOut(false);
		expect(onboardingProjectSource("repo-a", true)).toBe("other");
	});

	it("does not start a run while opted out", () => {
		setAnalyticsOptedOut(true);
		const end = beginOnboardingRun("first_run", "welcome");
		end();
		setAnalyticsOptedOut(false);
		expect(capture).not.toHaveBeenCalled();
		expect(window.localStorage.getItem("ao.telemetry.onboardingRun")).toBeNull();
	});
});

describe("onboarding Sentry reports", () => {
	beforeEach(() => {
		capture.mockClear();
		sentry.failure.mockClear();
		setAnalyticsOptedOut(false);
		window.localStorage.clear();
		resetOnboardingTelemetryForTests();
	});
	afterEach(() => setAnalyticsOptedOut(false));

	it("reports each broken attempt once, by action and reason", () => {
		startOnboardingAction("gh_install", { target: "gh", step: "tools" });
		finishOnboardingAction("gh_install", "failed", { target: "gh", reason: "install_failed" });
		finishOnboardingAction("gh_install", "failed", { target: "gh", reason: "install_failed" });
		expect(sentry.failure.mock.calls).toEqual([["gh_install", "install_failed"]]);
		// A retry that breaks again is its own report.
		startOnboardingAction("gh_install", { target: "gh", step: "tools" });
		finishOnboardingAction("gh_install", "failed", { target: "gh", reason: "lost_contact" });
		expect(sentry.failure.mock.calls.at(-1)).toEqual(["gh_install", "lost_contact"]);
	});

	it("sends nothing for an attempt that succeeded, was cancelled or timed out", () => {
		for (const outcome of ["succeeded", "cancelled", "timed_out"] as const) {
			startOnboardingAction("agent_sign_in", { target: "codex", step: "tools" });
			finishOnboardingAction("agent_sign_in", outcome, { target: "codex" });
		}
		expect(sentry.failure).not.toHaveBeenCalled();
	});

	it("passes setup states on for the Sentry helper to drop", () => {
		startOnboardingAction("agent_sign_in", { target: "codex", step: "tools" });
		finishOnboardingAction("agent_sign_in", "failed", { target: "codex", reason: "not_signed_in" });
		// The helper owns the reported-reasons list; the attempt is used up either way.
		expect(sentry.failure.mock.calls).toEqual([["agent_sign_in", "not_signed_in"]]);
		finishOnboardingAction("agent_sign_in", "failed", { target: "codex", reason: "start_failed" });
		expect(sentry.failure).toHaveBeenCalledOnce();
	});

	it("sends nothing for the same panel outside onboarding, with no attempt started there", () => {
		finishOnboardingAction("gh_sign_in", "failed", { target: "gh", reason: "start_failed" });
		expect(sentry.failure).not.toHaveBeenCalled();
	});

	it("leaves a newer attempt alone when a stale check answers", () => {
		startOnboardingAction("gh_sign_in", { target: "gh", step: "github_auth" });
		const stale = onboardingActionAttempt("gh_sign_in", "gh");
		finishOnboardingAction("gh_sign_in", "cancelled", { target: "gh" });
		startOnboardingAction("gh_sign_in", { target: "gh", step: "github_auth" });
		expect(finishOnboardingAction("gh_sign_in", "failed", { target: "gh", reason: "not_signed_in", attempt: stale })).toBe(false);
		finishOnboardingAction("gh_sign_in", "failed", { target: "gh", reason: "start_failed" });
		expect(sentry.failure.mock.calls).toEqual([["gh_sign_in", "start_failed"]]);
	});

	it("still reports while analytics is opted out: Share error events governs Sentry", () => {
		setAnalyticsOptedOut(true);
		startOnboardingAction("dependency_install", { target: "tmux", step: "dependencies" });
		finishOnboardingAction("dependency_install", "failed", { target: "tmux", reason: "start_failed" });
		expect(sentry.failure.mock.calls).toEqual([["dependency_install", "start_failed"]]);
		expect(capture).not.toHaveBeenCalled();
	});

	it("tags Sentry events with the onboarding flow while the window or a tour step shows", async () => {
		// The source is whichever module copy registered last (another test
		// reloads the module), so load a fresh copy and drive that one.
		vi.resetModules();
		const telemetry = await import("./onboarding-telemetry");
		expect(sentry.flowSource.current()).toBeUndefined();
		const close = telemetry.beginOnboardingRun("first_run", "welcome");
		expect(sentry.flowSource.current()).toBe("onboarding");
		close();
		expect(sentry.flowSource.current()).toBeUndefined();
		telemetry.setOnboardingTourStep("tour_project");
		expect(sentry.flowSource.current()).toBe("onboarding");
		telemetry.setOnboardingTourStep(undefined);
		expect(sentry.flowSource.current()).toBeUndefined();
	});
});
