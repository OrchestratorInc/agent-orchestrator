import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { activePlanStep, planAccessibilityLabel, planEdgeFades, planFollowOffset, planOverflows, samePlanProps } from "./planModel";
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

describe("planOverflows", () => {
	it("scrolls only a list taller than the window", () => {
		expect(planOverflows(200, 600)).toBe(true);
		expect(planOverflows(200, 150)).toBe(false);
		expect(planOverflows(200, 200.5)).toBe(false);
		expect(planOverflows(0, 600)).toBe(false);
	});
});

describe("samePlanProps", () => {
	const plan = { explanation: "Why", steps: [step("completed", "a"), step("in_progress", "b")] };

	it("treats an equal plan in a new object as the same", () => {
		expect(samePlanProps({ plan, live: true }, { plan: { ...plan, steps: plan.steps.map((s) => ({ ...s })) }, live: true })).toBe(true);
	});

	it("redraws when a step, the explanation or the live state changes", () => {
		expect(samePlanProps({ plan, live: true }, { plan, live: false })).toBe(false);
		expect(samePlanProps({ plan }, { plan: { ...plan, explanation: "Other" } })).toBe(false);
		expect(samePlanProps({ plan }, { plan: { ...plan, steps: [step("completed", "a"), step("completed", "b")] } })).toBe(false);
		expect(samePlanProps({ plan }, { plan: { ...plan, steps: [step("completed", "a")] } })).toBe(false);
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
	});

	it("carries a step's state in its mark, not an uppercase label on every row", () => {
		expect(source).not.toContain("textTransform");
		expect(source).not.toContain("STILL CHANGING");
		expect(source).toContain("<Spinning enabled={live}>");
	});

	it("turns the in-progress mark only while the turn runs", () => {
		// A stopped or failed turn, or a plan kept across turns, leaves a step in
		// progress after the agent has stopped; it must not keep spinning.
		expect(source).toContain("<StepMark status={step.status} live={Boolean(live)} slotHeight={markSlotHeight} />");
		expect(source).not.toContain("<Spinning enabled>");
	});

	it("opens a plan only while its turn runs and has steps left", () => {
		// A stopped or failed turn keeps a step in progress, and a list kept across
		// turns is never ticked off; past turns must not each show an open panel.
		expect(source).toContain("const openByDefault = Boolean(live) && !allDone;");
		expect(source).toContain("useState(openByDefault)");
	});

	it("lets the transcript scroll past a plan that fits", () => {
		expect(source).toContain("scrollEnabled={scrollable}");
		expect(source).toContain("alwaysBounceVertical={false}");
	});

	it("says the plan is busy on the header a screen reader focuses", () => {
		expect(source).toContain("accessibilityState={{ expanded, busy: Boolean(live) && !allDone }}");
		expect(source).toContain("<View style={styles.card}>");
	});

	it("forgets the window's measurements when the plan folds", () => {
		const fold = source.slice(source.indexOf("if (!expanded) {"), source.indexOf("follow();\n\t}, [expanded"));
		for (const reset of ["viewportHeight.current = 0;", "contentHeight.current = 0;", "offset.current = 0;", "setFades({ top: false, bottom: false });"]) {
			expect(fold).toContain(reset);
		}
	});

	it("caps step text at the body size and grows the mark slot and window with it", () => {
		expect(source).toContain("maxFontSizeMultiplier={fontScaleCap.body}");
		expect(source).toContain("const windowHeight = PLAN_WINDOW_HEIGHT * textScale;");
		expect(source).toContain("const markSlotHeight = type.subheadline.lineHeight * textScale;");
	});

	it("skips renders the plan did not cause", () => {
		expect(source).toContain("export const TurnPlan = memo(TurnPlanPanel, samePlanProps);");
	});

	it("scrolls a long plan inside a fixed window", () => {
		expect(source).toContain("style={{ maxHeight: windowHeight }}");
		expect(source).toContain("nestedScrollEnabled");
	});
});
