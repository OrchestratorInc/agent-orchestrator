import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({
	reduceMotion: false,
	loops: [] as { started: number; stopped: number }[],
	timings: [] as { value: unknown; config: { toValue: number; duration: number; useNativeDriver: boolean } }[],
	resets: [] as unknown[],
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	class Value {
		setValue(value: number) {
			if (value === 1) fixtures.resets.push(this);
		}
	}
	const loop = () => {
		const record = { started: 0, stopped: 0 };
		fixtures.loops.push(record);
		return { start: () => void record.started++, stop: () => void record.stopped++ };
	};
	return {
		ActivityIndicator: host("ActivityIndicator"),
		Animated: {
			View: host("AnimatedView"),
			Text: host("AnimatedText"),
			Value,
			loop,
			sequence: () => ({}),
			timing: (value: unknown, config: (typeof fixtures.timings)[number]["config"]) => {
				fixtures.timings.push({ value, config });
				return {};
			},
		},
		Image: host("Image"),
		Platform: { OS: "ios" },
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./haptics", () => ({ haptics: { tap() {} } }));
vi.mock("./native-header-button", () => ({ NativeHeaderButton: "NativeHeaderButton" }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => fixtures.reduceMotion }));
vi.mock("./motionHooks", () => ({ useEnterTransition: () => ({}) }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme, useThemedStyles: (factory: (theme: unknown) => unknown) => factory(darkTheme) };
});

import { BREATHE_MS } from "./motion";
import { Dot } from "./ui";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	fixtures.reduceMotion = false;
	fixtures.loops.length = 0;
	fixtures.timings.length = 0;
	fixtures.resets.length = 0;
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

function render(element: React.ReactElement): ReactTestRenderer {
	act(() => {
		renderer = create(element);
	});
	return renderer!;
}

function opacities(root: ReactTestRenderer["root"]): unknown[] {
	return root.findAllByType("AnimatedView" as never).map((dot) => dot.props.style.opacity);
}

function List({ breathing }: { breathing: boolean[] }) {
	return (
		<>
			{breathing.map((on, index) => (
				<Dot key={index} color="orange" breathing={on} />
			))}
		</>
	);
}

describe("Dot breathing", () => {
	// Dots that each ran their own loop kept the phase of whenever they mounted,
	// so a board of working rows pulsed out of step.
	it("drives every breathing dot from one shared value and one loop", () => {
		const [first, second] = opacities(render(<List breathing={[true, true]} />).root);
		expect(first).toBe(second);
		expect(typeof first).toBe("object");
		expect(fixtures.loops).toEqual([{ started: 1, stopped: 0 }]);
		expect(fixtures.timings.map(({ value }) => value)).toEqual([first, first]);
		expect(fixtures.timings.map(({ config }) => config)).toEqual([
			{ toValue: 0.35, duration: BREATHE_MS, useNativeDriver: true },
			{ toValue: 1, duration: BREATHE_MS, useNativeDriver: true },
		]);
	});

	it("joins a dot that mounts later into the running loop", () => {
		render(<List breathing={[true]} />);
		act(() => renderer!.update(<List breathing={[true, true]} />));
		const [first, second] = opacities(renderer!.root);
		expect(second).toBe(first);
		expect(fixtures.loops).toEqual([{ started: 1, stopped: 0 }]);
	});

	it("keeps the loop running until the last breathing dot lets go, then rests at full opacity", () => {
		render(<List breathing={[true, true]} />);
		act(() => renderer!.update(<List breathing={[true, false]} />));
		expect(fixtures.loops).toEqual([{ started: 1, stopped: 0 }]);
		expect(opacities(renderer!.root)[1]).toBe(1);

		const shared = opacities(renderer!.root)[0];
		act(() => renderer!.update(<List breathing={[false, false]} />));
		expect(fixtures.loops).toEqual([{ started: 1, stopped: 1 }]);
		expect(fixtures.resets).toEqual([shared]);
		expect(opacities(renderer!.root)).toEqual([1, 1]);

		act(() => renderer!.update(<List breathing={[true]} />));
		expect(fixtures.loops).toEqual([
			{ started: 1, stopped: 1 },
			{ started: 1, stopped: 0 },
		]);
		expect(opacities(renderer!.root)[0]).toBe(shared);
	});

	it("stops the shared loop when the breathing dots unmount", () => {
		render(<List breathing={[true, true]} />);
		act(() => renderer!.unmount());
		renderer = undefined;
		expect(fixtures.loops).toEqual([{ started: 1, stopped: 1 }]);
	});

	it("never starts the loop under Reduce Motion", () => {
		fixtures.reduceMotion = true;
		expect(opacities(render(<List breathing={[true, true]} />).root)).toEqual([1, 1]);
		expect(fixtures.loops).toEqual([]);
	});

	it("keeps a dot's shape and colour", () => {
		const dot = render(<Dot color="orange" size={12} />).root.findByType("AnimatedView" as never);
		expect(dot.props.style).toEqual({ width: 12, height: 12, borderRadius: 6, backgroundColor: "orange", opacity: 1 });
	});
});
