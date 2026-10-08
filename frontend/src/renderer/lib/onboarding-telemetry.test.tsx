import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const capture = vi.hoisted(() => vi.fn(async () => undefined));
vi.mock("./telemetry", () => ({ captureRendererEvent: capture }));

import { reportOnboardingStep, useOnboardingStep } from "./onboarding-telemetry";

describe("onboarding step telemetry", () => {
	beforeEach(() => {
		capture.mockClear();
		window.localStorage.clear();
	});

	it("reports each step phase once per install", () => {
		reportOnboardingStep("welcome", "viewed");
		reportOnboardingStep("welcome", "viewed");
		expect(capture.mock.calls).toEqual([["ao.onboarding.step_viewed", { step: "welcome" }]]);
		// A fresh module load (next launch) reads the persisted record.
		expect(JSON.parse(window.localStorage.getItem("ao.telemetry.onboardingSteps") ?? "[]")).toEqual(["welcome:viewed"]);
	});

	it("does not call a completed step abandoned", () => {
		reportOnboardingStep("github_auth", "completed");
		reportOnboardingStep("github_auth", "abandoned");
		expect(capture.mock.calls.map((c) => c[0])).toEqual(["ao.onboarding.step_completed"]);
	});

	it("views while active, abandons on window close, completes when the step clears", () => {
		const { rerender } = renderHook(({ active }) => useOnboardingStep("github_auth", active), { initialProps: { active: true } });
		expect(capture.mock.calls.map((c) => c[0])).toEqual(["ao.onboarding.step_viewed"]);

		act(() => {
			rerender({ active: false });
		});
		expect(capture.mock.calls.map((c) => c[0])).toEqual(["ao.onboarding.step_viewed", "ao.onboarding.step_completed"]);
		act(() => {
			window.dispatchEvent(new Event("pagehide"));
		});
		expect(capture).toHaveBeenCalledTimes(2);
	});

	it("abandons when the window closes mid-step", () => {
		renderHook(() => useOnboardingStep("welcome", true));
		act(() => {
			window.dispatchEvent(new Event("pagehide"));
		});
		expect(capture.mock.calls.map((c) => c[0])).toEqual(["ao.onboarding.step_viewed", "ao.onboarding.step_abandoned"]);
	});
});
