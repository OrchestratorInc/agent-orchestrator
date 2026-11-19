import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const capture = vi.hoisted(() => vi.fn(async (_event: string, _props?: unknown) => undefined));
vi.mock("./telemetry", () => ({ captureRendererEvent: capture }));

import { notifyComposerSent } from "./guided-tour-signals";
import {
	beginOnboardingRun,
	completeOnboardingRun,
	completeOnboardingStepIfViewed,
	finishOnboardingAction,
	installJobOutcome,
	onboardingProjectSource,
	recordOnboardingActivation,
	reportOnboardingBack,
	reportOnboardingProjectAdded,
	reportOnboardingProjectFailed,
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

	it("abandons when the window closes mid-step", () => {
		renderHook(() => useOnboardingStep("welcome", true));
		act(() => {
			window.dispatchEvent(new Event("pagehide"));
		});
		expect(events()).toEqual(["ao.onboarding.step_viewed", "ao.onboarding.step_abandoned"]);
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
		expect(lastProps("ao.onboarding.abandoned")).toMatchObject({ run_kind: "first_run", step: "tools", launch_count: 1 });
		end();
		resetOnboardingTelemetryForTests();
		window.dispatchEvent(new Event("pagehide"));
		expect(events().filter((event) => event === "ao.onboarding.abandoned")).toHaveLength(1);
	});

	it("ends a run left open when a project made onboarding unnecessary, and nothing for one never shown", () => {
		supersedeOnboardingRun();
		expect(capture).not.toHaveBeenCalled();
		beginOnboardingRun("first_run", "welcome")();
		supersedeOnboardingRun();
		expect(lastProps("ao.onboarding.completed")).toMatchObject({ outcome: "superseded" });
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

	it("reports project clone and add results only during the tour's project step", () => {
		reportOnboardingProjectFailed("project_clone", "CLONE_FAILED");
		expect(capture).not.toHaveBeenCalled();
		setOnboardingTourStep("tour_project");
		reportOnboardingProjectFailed("project_clone", "CLONE_FAILED");
		reportOnboardingProjectFailed("project_clone", "unknown");
		reportOnboardingProjectAdded("clone");
		const finished = capture.mock.calls.filter((call) => call[0] === "ao.onboarding.action_finished").map((call) => call[1]);
		expect(finished).toEqual([
			expect.objectContaining({ action: "project_clone", attempt: 1, outcome: "failed", reason: "refused", error_code: "CLONE_FAILED" }),
			expect.objectContaining({ action: "project_clone", attempt: 2, outcome: "failed", reason: "request_failed", error_code: undefined }),
			expect.objectContaining({ action: "project_clone", attempt: 3, outcome: "succeeded" }),
		]);
	});

	it("reads where the tour's project came from", () => {
		reportOnboardingProjectAdded("local_folder");
		expect(onboardingProjectSource(true)).toBe("local_folder");
		// The note is used up.
		expect(onboardingProjectSource(true)).toBe("other");
		expect(onboardingProjectSource(false)).toBe("existing");
	});
});

describe("onboarding activation telemetry", () => {
	beforeEach(() => {
		capture.mockClear();
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

	it("does not count a replay as activation", () => {
		beginOnboardingRun("replay", "welcome");
		completeOnboardingRun("finished");
		capture.mockClear();
		recordOnboardingActivation(false);
		expect(capture).not.toHaveBeenCalled();
	});
});
