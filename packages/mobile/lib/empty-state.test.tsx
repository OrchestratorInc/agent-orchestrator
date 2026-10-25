import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	class Value {
		setValue() {}
	}
	return {
		ActivityIndicator: host("ActivityIndicator"),
		Animated: { View: host("AnimatedView"), Value, loop: () => ({ start() {}, stop() {} }), sequence: () => ({}), timing: () => ({}) },
		Image: host("Image"),
		Platform: { OS: "ios" },
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
vi.mock("./haptics", () => ({ haptics: { tap() {} } }));
vi.mock("./native-header-button", () => ({ NativeHeaderButton: "NativeHeaderButton" }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => true }));
vi.mock("./motionHooks", () => ({ useEnterTransition: () => ({}) }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme, useThemedStyles: (factory: (theme: unknown) => unknown) => factory(darkTheme) };
});

import { darkTheme } from "./theme";
import { iconSize } from "./tokens";
import { EmptyState } from "./ui";

let renderer: ReactTestRenderer | undefined;

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

function flat(style: unknown): Record<string, unknown> {
	return Object.assign({}, ...[style].flat(Infinity).filter(Boolean));
}

describe("EmptyState", () => {
	it("leads with a large secondary symbol standing on its own, not a bordered tile", () => {
		const root = render(<EmptyState icon="wifi-off" title="Machine offline" />).root;
		const glyph = root.findByType("SymbolView" as never);
		expect(glyph.props.name).toEqual({ ios: "wifi.slash", android: "wifi_off" });
		expect(glyph.props.size).toBe(iconSize.hero);
		expect(glyph.props.tintColor).toBe(darkTheme.textSecondary);
		for (const view of root.findAllByType("View" as never).concat(root.findAllByType("AnimatedView" as never))) {
			expect(flat(view.props.style).borderWidth).toBeUndefined();
		}
	});

	it("takes the size of its container instead of a fixed height", () => {
		const container = render(<EmptyState title="All caught up" />).root.findAllByType("AnimatedView" as never)[0];
		const style = flat(container.props.style);
		expect(style.flex).toBe(1);
		expect(style.minHeight).toBeUndefined();
	});

	it("marks its title as a heading", () => {
		const title = render(<EmptyState title="No workers yet" message="Start a task." />).root.findAll((node) => (node.type as unknown) === "Text" && node.children.includes("No workers yet"))[0];
		expect(title.props.accessibilityRole).toBe("header");
	});

	it("keeps Lucide for a domain glyph with no system symbol", () => {
		const root = render(<EmptyState icon="git-pull-request" title="No pull requests" />).root;
		expect(root.findByType("Feather" as never).props.name).toBe("git-pull-request");
		expect(root.findAllByType("SymbolView" as never)).toHaveLength(0);
	});

	it("tints the glyph for a state that means something", () => {
		const root = render(<EmptyState icon="alert-triangle" iconColor={darkTheme.red} title="Couldn't connect" />).root;
		expect(root.findByType("SymbolView" as never).props.tintColor).toBe(darkTheme.red);
	});

	it("draws a caller's visual in place of the glyph", () => {
		const root = render(<EmptyState visual={<MascotStub />} title="Say hello" />).root;
		expect(root.findAllByType(MascotStub)).toHaveLength(1);
		expect(root.findAllByType("SymbolView" as never)).toHaveLength(0);
	});

	it("stacks several actions under the message", () => {
		const root = render(<EmptyState title="Offline" action={<><ActionStub /><ActionStub /></>} />).root;
		const actions = root.findAllByType(ActionStub);
		expect(actions).toHaveLength(2);
		expect(flat(actions[0].parent!.props.style)).toMatchObject({ alignItems: "center" });
	});
});

function MascotStub() {
	return null;
}

function ActionStub() {
	return null;
}
