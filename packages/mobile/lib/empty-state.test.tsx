import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({ platform: { OS: "ios" } }));

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
vi.mock("./haptics", () => ({ haptics: { tap() {} } }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => true }));
vi.mock("./motionHooks", () => ({ useEnterTransition: () => ({}) }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme, useThemedStyles: (factory: (theme: unknown) => unknown) => factory(darkTheme) };
});

import { darkTheme } from "./theme";
import { iconSize } from "./tokens";
import { Button, EmptyState } from "./ui";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	fixtures.platform.OS = "ios";
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

function flat(style: unknown): Record<string, unknown> {
	return Object.assign({}, ...[style].flat(Infinity).filter(Boolean));
}

function buttonFills(root: ReactTestRenderer["root"]): unknown[] {
	return root.findAllByType("Pressable" as never).map((button) => flat(button.props.style({ pressed: false })).backgroundColor);
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

	it("shows the system's large spinner in place of the glyph while loading", () => {
		const root = render(<EmptyState icon="chat" busy title="Loading conversation…" />).root;
		expect(root.findByType("ActivityIndicator" as never).props.size).toBe("large");
		expect(root.findAllByType("SymbolView" as never)).toHaveLength(0);
	});

	it("stacks several actions under the message", () => {
		const root = render(<EmptyState title="Offline" action={<><ActionStub /><ActionStub /></>} />).root;
		const actions = root.findAllByType(ActionStub);
		expect(actions).toHaveLength(2);
		expect(flat(actions[0].parent!.props.style)).toMatchObject({ alignItems: "center" });
	});

	it.each([
		["ios", darkTheme.accent],
		["android", darkTheme.accentTint],
	])("draws an action that names no variant as %s's empty-state button", (os, fill) => {
		fixtures.platform.OS = os;
		const root = render(<EmptyState title="Machine offline" action={<Button title="Open board" onPress={() => {}} />} />).root;
		expect(buttonFills(root)).toEqual([fill]);
	});

	it("keeps the variant each action names, so a pair stays ranked on Android", () => {
		fixtures.platform.OS = "android";
		const root = render(
			<EmptyState
				title="Couldn't connect"
				action={
					<>
						<Button title="Retry" variant="secondary" onPress={() => {}} />
						<Button title="Scan" variant="primary" onPress={() => {}} />
					</>
				}
			/>,
		).root;
		expect(buttonFills(root)).toEqual([darkTheme.accentTint, darkTheme.accent]);
	});
});

function MascotStub() {
	return null;
}

function ActionStub() {
	return null;
}
