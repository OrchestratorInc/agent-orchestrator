import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({ platform: { OS: "ios" }, tap: vi.fn(), warning: vi.fn() }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"),
		Animated: { View: host("AnimatedView"), Value: class {} },
		Image: host("Image"),
		Platform: fixtures.platform,
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("./haptics", () => ({ haptics: { tap: fixtures.tap, warning: fixtures.warning } }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => false }));
vi.mock("./motionHooks", () => ({ useEnterTransition: () => ({}) }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme, useThemedStyles: (factory: (theme: unknown) => unknown) => factory(darkTheme) };
});

import { stateOpacity } from "./tokens";
import { Button } from "./ui";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	fixtures.platform.OS = "ios";
	fixtures.tap.mockClear();
	fixtures.warning.mockClear();
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

function render(element: React.ReactElement): ReactTestInstance {
	act(() => {
		renderer = create(element);
	});
	return renderer!.root.findByType("Pressable" as never);
}

function styleOf(pressable: ReactTestInstance, pressed = false): Record<string, unknown> {
	const style = (pressable.props.style as (state: { pressed: boolean }) => unknown[])({ pressed });
	return Object.assign({}, ...style.flat(Infinity).filter(Boolean));
}

describe("Button", () => {
	it("dims on iOS while pressed, and ripples on Android instead", () => {
		const pressable = render(<Button title="Retry" onPress={() => {}} />);
		expect(pressable.props.android_ripple).toBeUndefined();
		expect(styleOf(pressable, true).opacity).toBe(stateOpacity.pressed);
		fixtures.platform.OS = "android";
		const android = render(<Button title="Retry" onPress={() => {}} />);
		expect(android.props.android_ripple).toMatchObject({ borderless: false });
		expect(styleOf(android, true).opacity).toBeUndefined();
	});

	it("dims to the shared disabled opacity and says so", () => {
		const pressable = render(<Button title="Save" disabled onPress={() => {}} />);
		expect(styleOf(pressable).opacity).toBe(stateOpacity.disabled);
		expect(pressable.props.disabled).toBe(true);
		expect(pressable.props.accessibilityState).toEqual({ disabled: true, busy: false });
	});

	it("keeps its name and reports busy while loading", () => {
		const pressable = render(<Button title="Connect" loading onPress={() => {}} />);
		expect(pressable.props.accessibilityRole).toBe("button");
		expect(pressable.props.accessibilityLabel).toBe("Connect");
		expect(pressable.props.accessibilityState).toEqual({ disabled: true, busy: true });
		expect(renderer!.root.findAllByType("ActivityIndicator" as never)).toHaveLength(1);
	});

	it("warns before a destructive action and taps for the rest", () => {
		const onPress = vi.fn();
		const pressable = render(<Button title="Delete" variant="danger" onPress={onPress} />);
		act(() => pressable.props.onPress());
		expect(fixtures.warning).toHaveBeenCalledTimes(1);
		expect(fixtures.tap).not.toHaveBeenCalled();
		expect(onPress).toHaveBeenCalledTimes(1);
	});
});
