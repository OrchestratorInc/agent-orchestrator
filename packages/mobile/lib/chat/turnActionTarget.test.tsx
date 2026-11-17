import { readFileSync } from "node:fs";
import type { ReactElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({
	Alert: { alert: vi.fn() },
	ActivityIndicator: "ActivityIndicator",
	Platform: { OS: "ios" },
	Pressable: "Pressable",
	StyleSheet: { create: (styles: object) => styles },
	View: "View",
}));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({ textTertiary: "#tertiary" }) }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: { opacity: 0.7 } }) }));
vi.mock("../haptics", () => ({ haptics: { warning: vi.fn(), tap: vi.fn() } }));

import { touchTarget } from "../tokens";
import { TurnForkButton } from "./TurnForkButton";
import { TURN_ACTION_SIZE, TURN_ACTION_SLOP, TurnRollbackButton, turnActionControl } from "./TurnRollbackButton";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

/** The touch area a control gets: its own square plus the slop on each side. */
function reach(style: { width: number; height: number }, slop: number) {
	return { width: style.width + 2 * slop, height: style.height + 2 * slop };
}

function pressable(element: ReactElement, label: string) {
	act(() => { renderer = create(element); });
	const control = renderer!.root.findByProps({ accessibilityLabel: label });
	const [style] = control.props.style({ pressed: false });
	return { style, hitSlop: control.props.hitSlop };
}

// A reply's action row holds copy, fork and roll back. Each is a small square
// with its touch taken out by slop to the platform's minimum target, and all
// three share the one size, so no control in the row is harder to hit.
describe("a turn's action controls", () => {
	it("reach the minimum touch target on each platform", async () => {
		expect(reach(turnActionControl, TURN_ACTION_SLOP).height).toBeGreaterThanOrEqual(touchTarget);
		vi.resetModules();
		vi.doMock("../platformTokens", () => import("../platformTokens.android"));
		const android = await import("./TurnRollbackButton");
		const androidTarget = (await import("../platformTokens.android")).touchTarget;
		expect(androidTarget).toBeGreaterThan(touchTarget);
		expect(reach(android.turnActionControl, android.TURN_ACTION_SLOP).height).toBeGreaterThanOrEqual(androidTarget);
		expect(reach(android.turnActionControl, android.TURN_ACTION_SLOP).width).toBeGreaterThanOrEqual(androidTarget);
		vi.doUnmock("../platformTokens");
	});

	it("gives roll back the row's size and slop", () => {
		const { style, hitSlop } = pressable(<TurnRollbackButton turnId="turn-1" onRollback={async () => 0} compact />, "Roll back to before this turn");
		expect(style).toEqual(turnActionControl);
		expect(style.width).toBe(TURN_ACTION_SIZE);
		expect(hitSlop).toBe(TURN_ACTION_SLOP);
	});

	it("gives fork the row's size and slop", () => {
		const { style, hitSlop } = pressable(<TurnForkButton turnId="turn-1" onFork={async () => {}} compact />, "Fork from this turn");
		expect(style).toEqual(turnActionControl);
		expect(hitSlop).toBe(TURN_ACTION_SLOP);
	});

	// Copy lives in ChatTimeline, whose dependencies are too many to render here.
	it("gives copy the same size and slop", () => {
		const timeline = readFileSync(new URL("./ChatTimeline.tsx", import.meta.url), "utf8");
		const copy = timeline.slice(timeline.indexOf("function CopyResponseButton"), timeline.indexOf("function SystemSignal"));
		expect(copy).toContain("hitSlop={TURN_ACTION_SLOP}");
		expect(copy).toContain("styles.turnAction,");
		expect(timeline).toContain("turnAction: turnActionControl,");
		// The row is a full target tall, so each control's slop is inside it.
		expect(timeline).toContain("turnActions: { height: TURN_ACTION_SIZE + 2 * TURN_ACTION_SLOP,");
	});

	it("keeps a full-size control off the row's slop", () => {
		const { hitSlop } = pressable(<TurnRollbackButton turnId="turn-1" onRollback={async () => 0} />, "Roll back to before this turn");
		expect(hitSlop).toBeUndefined();
	});
});
