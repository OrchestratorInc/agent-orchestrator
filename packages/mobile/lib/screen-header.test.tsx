import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({ platform: { OS: "ios" } }));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	class Value {
		interpolate() { return 1; }
		setValue() {}
	}
	return {
		ActivityIndicator: host("ActivityIndicator"),
		Animated: {
			View: host("AnimatedView"),
			Value,
			loop: () => ({ start() {}, stop() {} }),
			sequence: () => ({}),
			timing: () => ({}),
		},
		Easing: { linear: "linear" },
		Image: host("Image"),
		Platform: fixtures.platform,
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
		Switch: host("Switch"),
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("./haptics", () => ({ haptics: { tap() {} } }));
vi.mock("./native-header-button", async () => {
	const React = await import("react");
	return { NativeHeaderButton: (props: object) => React.createElement("NativeHeaderButton", props) };
});
vi.mock("./sidebar-navigation-context", () => ({ useOptionalSidebarNavigation: () => null }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => false }));
vi.mock("./motionHooks", () => ({ useEnterTransition: () => ({}) }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({ fontMono: "GeistMono" }, { get: (target, property) => property in target ? target[property as keyof typeof target] : "#888" });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});

import { fontScaleCap, space, type } from "./tokens";
import { ListSectionHeader, NumberedStep, ScreenHeader, SheetHeader } from "./ui";

describe("ScreenHeader", () => {
	it("keeps a long title on one line when a right-side identity is present", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<ScreenHeader title="A very long project name" right="Machine" />);
		});

		const title = renderer.root.findAll((node) => (node.type as unknown) === "Text" && node.children.includes("A very long project name"))[0];
		expect(title.props.numberOfLines).toBe(1);
		expect(title.props.ellipsizeMode).toBe("tail");
	});

	it("marks its title as a heading", () => {
		expect(textNode(render(<ScreenHeader title="Projects" />), "Projects").props.accessibilityRole).toBe("header");
	});
});

describe("SheetHeader", () => {
	it("marks its title as a heading", () => {
		const renderer = render(<SheetHeader title="Choose a model" subtitle="Used for new turns" />);
		expect(textNode(renderer, "Choose a model").props.accessibilityRole).toBe("header");
		expect(textNode(renderer, "Used for new turns").props.accessibilityRole).toBeUndefined();
	});
});

describe("ListSectionHeader", () => {
	beforeEach(() => {
		fixtures.platform.OS = "ios";
	});

	function flat(style: unknown): Record<string, unknown> {
		return Object.assign({}, ...[style].flat(Infinity).filter(Boolean));
	}

	it("reads a fixed section as a heading with its count", () => {
		const view = render(<ListSectionHeader label="Pinned" count={3} />).root.findAllByType("View" as never)[0];
		expect(view.props).toMatchObject({ accessible: true, accessibilityRole: "header", accessibilityLabel: "Pinned, 3" });
	});

	it("highlights a foldable header's row on iOS instead of fading it", () => {
		const pressable = render(<ListSectionHeader label="Recent" open onToggle={() => {}} />).root.findByType("Pressable" as never);
		expect(pressable.props.android_ripple).toBeUndefined();
		const pressed = flat(pressable.props.style({ pressed: true }));
		expect(pressed.backgroundColor).toBe("#888");
		expect(pressed.opacity).toBeUndefined();
		expect(pressable.props.accessibilityState).toEqual({ expanded: true });
	});

	it("ripples a foldable header on Android", () => {
		fixtures.platform.OS = "android";
		const pressable = render(<ListSectionHeader label="Recent" open={false} onToggle={() => {}} />).root.findByType("Pressable" as never);
		expect(pressable.props.android_ripple).toMatchObject({ borderless: false });
		expect(flat(pressable.props.style({ pressed: true })).backgroundColor).toBeUndefined();
	});

	it("draws its disclosure chevron as a system symbol", () => {
		const renderer = render(<ListSectionHeader label="Recent" open={false} onToggle={() => {}} />);
		expect(renderer.root.findByType("SymbolView" as never).props.name).toEqual({ ios: "chevron.forward", android: "chevron_right" });
		act(() => renderer.update(<ListSectionHeader label="Recent" open onToggle={() => {}} />));
		expect(renderer.root.findByType("SymbolView" as never).props.name).toEqual({ ios: "chevron.down", android: "expand_more" });
	});
});

describe("NumberedStep", () => {
	it("reads as one element that says where the step falls in the list", () => {
		const step = render(<NumberedStep n={2} total={3} title="Scan the code" hint="Or enter the address." />).root.findAllByType("View" as never)[0];
		expect(step.props).toMatchObject({ accessible: true, accessibilityLabel: "Step 2 of 3, Scan the code, Or enter the address." });
	});

	it("still names the step without a total", () => {
		const step = render(<NumberedStep n={1} title="Enable a connection" />).root.findAllByType("View" as never)[0];
		expect(step.props.accessibilityLabel).toBe("Step 1, Enable a connection");
	});

	it("draws one badge shape, compact or not, that grows with its number instead of clipping it", () => {
		const badge = (compact: boolean) => render(<NumberedStep n={1} title="Pair" compact={compact} />).root.findAllByType("View" as never)[1];
		const full = badge(false).props.style;
		expect(badge(true).props.style).toEqual(full);
		expect(full.width).toBeUndefined();
		expect(full.height).toBeUndefined();
		// The capped line plus the padding fits inside the floor, so one digit
		// keeps a round badge at the largest text size it allows.
		expect(type.footnote.lineHeight * fontScaleCap.chrome + 2 * space.hair).toBeLessThanOrEqual(full.minHeight);
		expect(full.minWidth).toBe(full.minHeight);
		const number = render(<NumberedStep n={1} title="Pair" />).root.findAll((node) => (node.type as unknown) === "Text" && node.children.includes("1" as never))[0];
		expect(number.props.maxFontSizeMultiplier).toBe(fontScaleCap.chrome);
	});
});

let current: ReactTestRenderer | undefined;

afterEach(() => {
	act(() => current?.unmount());
	current = undefined;
});

function render(element: React.ReactElement): ReactTestRenderer {
	act(() => {
		current = create(element);
	});
	return current!;
}

function textNode(renderer: ReactTestRenderer, text: string) {
	return renderer.root.findAll((node) => (node.type as unknown) === "Text" && node.children.includes(text))[0];
}
