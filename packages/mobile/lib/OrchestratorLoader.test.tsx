import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const fixture = vi.hoisted(() => ({ reduced: false, repeats: 0, cancels: 0 }));

vi.mock("react-native", () => ({
	StyleSheet: { create: <T,>(styles: T) => styles, absoluteFill: {} },
	Text: "Text",
	View: "View",
}));
vi.mock("react-native-svg", () => ({ SvgXml: "SvgXml" }));
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	return {
		default: { View: "AnimatedView", Text: "AnimatedText" },
		Easing: { linear: null, cubic: null, quad: null, in: (e: unknown) => e, out: (e: unknown) => e, inOut: (e: unknown) => e },
		cancelAnimation: () => { fixture.cancels += 1; },
		withDelay: (_ms: number, value: unknown) => value,
		withSequence: (...values: unknown[]) => values[0],
		interpolate: () => 0,
		useSharedValue: (value: unknown) => React.useRef({ value }).current,
		useAnimatedStyle: (style: () => unknown) => style(),
		withRepeat: (value: unknown) => { fixture.repeats += 1; return value; },
		withTiming: (value: unknown) => value,
	};
});
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => fixture.reduced }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme, useThemedStyles: (make: (t: unknown) => unknown) => make(darkTheme) };
});

import { ConversationLoading, OrchestratorLoader } from "./OrchestratorLoader";

let renderer: ReactTestRenderer | undefined;

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	fixture.reduced = false;
	fixture.repeats = 0;
	fixture.cancels = 0;
});

function render(element: React.ReactElement): ReactTestRenderer {
	act(() => {
		renderer = create(element);
	});
	return renderer!;
}

describe("OrchestratorLoader", () => {
	it("stages three small Rutos from the brand art, each with its own note", () => {
		const root = render(<OrchestratorLoader />).root;
		const art = root.findAllByType("SvgXml" as never).map((node) => node.props.xml as string);
		expect(art).toHaveLength(3);
		for (const xml of art) expect(xml).toContain('id="ruto-outline"');
		expect(root.findAllByType("AnimatedText" as never).map((node) => node.props.children)).toEqual(["♪", "♫", "♩"]);
		// The sway, and each player's hop, note and blink.
		expect(fixture.repeats).toBe(10);
	});

	it("stands still under Reduce Motion", () => {
		fixture.reduced = true;
		render(<OrchestratorLoader />);
		expect(fixture.repeats).toBe(0);
	});
});

describe("ConversationLoading", () => {
	it("announces itself as busy and names what is loading", () => {
		const root = render(<ConversationLoading title="Orchestrating…" message="Fix the login flow" />).root;
		const box = root.findByProps({ accessibilityRole: "progressbar" });
		expect(box.props).toMatchObject({ accessibilityLabel: "Orchestrating…", accessibilityState: { busy: true } });
		expect(root.findAllByType("Text" as never).map((node) => node.props.children)).toEqual(["Orchestrating…", "Fix the login flow"]);
	});
});
