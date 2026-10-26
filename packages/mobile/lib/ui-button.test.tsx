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

import { darkTheme } from "./theme";
import { radius, stateOpacity, touchTarget } from "./tokens";
import { Button, type ButtonVariant } from "./ui";

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
	it("is a capsule at least one touch target tall", () => {
		const style = styleOf(render(<Button title="Retry" onPress={() => {}} />));
		expect(style.borderRadius).toBe(radius.pill);
		expect(style.minHeight).toBe(touchTarget);
		// The ripple is drawn inside the view's bounds; the capsule has to clip it.
		expect(style.overflow).toBe("hidden");
	});

	it("dims on iOS while pressed, without a ripple or a scale", () => {
		const pressable = render(<Button title="Retry" onPress={() => {}} />);
		expect(pressable.props.android_ripple).toBeUndefined();
		expect(styleOf(pressable, true).opacity).toBe(stateOpacity.pressed);
		expect(styleOf(pressable, true).transform).toBeUndefined();
		expect(renderer!.root.findAllByType("AnimatedView" as never)).toHaveLength(0);
	});

	it("ripples on Android and leaves the pressed style alone", () => {
		fixtures.platform.OS = "android";
		const pressable = render(<Button title="Retry" onPress={() => {}} />);
		expect(pressable.props.android_ripple).toMatchObject({ borderless: false });
		expect(styleOf(pressable, true).opacity).toBeUndefined();
	});

	it("ripples in the fill's own ink on a filled button, so it shows on the near-white accent", () => {
		fixtures.platform.OS = "android";
		const primary = render(<Button title="Save" onPress={() => {}} />);
		expect(primary.props.android_ripple.color).not.toBe(darkTheme.accentTint);
		act(() => renderer!.update(<Button title="Save" variant="secondary" onPress={() => {}} />));
		expect(renderer!.root.findByType("Pressable" as never).props.android_ripple.color).toBe(darkTheme.accentTint);
	});

	it.each<[ButtonVariant, string]>([
		["primary", darkTheme.accent],
		["secondary", darkTheme.accentTint],
		["danger", darkTheme.tintRed],
		["success", darkTheme.green],
	])("fills a %s button", (variant, fill) => {
		expect(styleOf(render(<Button title="Go" variant={variant} onPress={() => {}} />)).backgroundColor).toBe(fill);
	});

	it("draws a ghost button as text alone, with no fill or border", () => {
		const style = styleOf(render(<Button title="Discard" variant="ghost" onPress={() => {}} />));
		expect(style.backgroundColor).toBe("transparent");
		expect(style.borderWidth).toBeUndefined();
		const label = renderer!.root.findByType("Text" as never);
		expect(label.props.style).toEqual(expect.arrayContaining([{ color: darkTheme.accent }]));
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

	it("draws a chrome icon as a system symbol, mapping the Lucide name it was given", () => {
		render(<Button title="Retry" icon="refresh-cw" onPress={() => {}} />);
		expect(renderer!.root.findByType("SymbolView" as never).props.name).toEqual({ ios: "arrow.clockwise", android: "refresh" });
		expect(renderer!.root.findAllByType("Feather" as never)).toHaveLength(0);
	});

	it("keeps Lucide for a domain glyph with no system symbol", () => {
		render(<Button title="Open pull request" icon="git-pull-request" onPress={() => {}} />);
		expect(renderer!.root.findByType("Feather" as never).props.name).toBe("git-pull-request");
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
