import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { activePlanStep, planAccessibilityLabel, planEdgeFades, planFollowOffset } from "./planModel";
import type { PlanStep } from "./types";

const step = (status: PlanStep["status"], text: string = status): PlanStep => ({ text, status });

describe("activePlanStep", () => {
	it("follows the step in progress", () => {
		expect(activePlanStep([step("completed"), step("in_progress"), step("pending")])).toBe(1);
	});

	it("falls back to the next step to start, then the last step", () => {
		expect(activePlanStep([step("completed"), step("pending"), step("pending")])).toBe(1);
		expect(activePlanStep([step("completed"), step("completed")])).toBe(1);
	});
});

describe("planFollowOffset", () => {
	it("centres the followed row in the window", () => {
		expect(planFollowOffset({ y: 300, height: 30 }, 200, 600)).toBe(215);
	});

	it("rests the first and last rows against their edge", () => {
		expect(planFollowOffset({ y: 0, height: 30 }, 200, 600)).toBe(0);
		expect(planFollowOffset({ y: 570, height: 30 }, 200, 600)).toBe(400);
	});

	it("does not scroll a plan that fits", () => {
		expect(planFollowOffset({ y: 90, height: 30 }, 200, 150)).toBe(0);
	});
});

describe("planEdgeFades", () => {
	it("fades only the edges with steps behind them", () => {
		expect(planEdgeFades(0, 200, 600)).toEqual({ top: false, bottom: true });
		expect(planEdgeFades(200, 200, 600)).toEqual({ top: true, bottom: true });
		expect(planEdgeFades(400, 200, 600)).toEqual({ top: true, bottom: false });
		expect(planEdgeFades(0, 200, 150)).toEqual({ top: false, bottom: false });
	});
});

describe("planAccessibilityLabel", () => {
	it("speaks progress and the current step", () => {
		expect(planAccessibilityLabel([step("completed", "Map model"), step("in_progress", "Build screen")])).toBe(
			"Plan, 1 of 2 steps done. Now: Build screen",
		);
		expect(planAccessibilityLabel([step("completed"), step("completed")])).toBe("Plan, all 2 steps done");
	});
});

describe("plan panel", () => {
	const source = readFileSync(new URL("./TurnPlan.tsx", import.meta.url), "utf8");

	it("reads steps at subheadline size rather than caption size", () => {
		expect(source).toContain("stepText: { ...type.subheadline");
		expect(source).not.toContain("caption");
	});

	it("carries a step's state in its mark, not an uppercase label on every row", () => {
		expect(source).not.toContain("textTransform");
		expect(source).not.toContain("STILL CHANGING");
		expect(source).toContain("<Spinning enabled={live}>");
	});

	it("turns the in-progress mark only while the turn runs", () => {
		// A stopped or failed turn, or a plan kept across turns, leaves a step in
		// progress after the agent has stopped; it must not keep spinning.
		expect(source).toContain("<StepMark status={step.status} live={Boolean(live)} />");
		expect(source).not.toContain("<Spinning enabled>");
	});

	it("scrolls a long plan inside a fixed window", () => {
		expect(source).toContain("maxHeight: PLAN_WINDOW_HEIGHT");
		expect(source).toContain("nestedScrollEnabled");
	});
});
