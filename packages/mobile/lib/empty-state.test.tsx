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
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => true }));
vi.mock("./motionHooks", () => ({ useEnterTransition: () => ({}) }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme, useThemedStyles: (factory: (theme: unknown) => unknown) => factory(darkTheme) };
});

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

describe("EmptyState", () => {
	it("marks its title as a heading", () => {
		const title = render(<EmptyState title="No workers yet" message="Start a task." />).root.findAll((node) => (node.type as unknown) === "Text" && node.children.includes("No workers yet"))[0];
		expect(title.props.accessibilityRole).toBe("header");
	});

	it("draws a caller's visual in place of the glyph", () => {
		const root = render(<EmptyState visual={<MascotStub />} title="Say hello" />).root;
		expect(root.findAllByType(MascotStub)).toHaveLength(1);
		expect(root.findAllByType("SymbolView" as never)).toHaveLength(0);
	});

	it("shows a spinner in place of the glyph while loading", () => {
		const root = render(<EmptyState icon="chat" busy title="Loading conversation…" />).root;
		expect(root.findAllByType("ActivityIndicator" as never)).toHaveLength(1);
		expect(root.findAllByType("SymbolView" as never)).toHaveLength(0);
	});

	it("renders every action it is given", () => {
		const root = render(<EmptyState title="Offline" action={<><ActionStub /><ActionStub /></>} />).root;
		expect(root.findAllByType(ActionStub)).toHaveLength(2);
	});
});

function MascotStub() {
	return null;
}

function ActionStub() {
	return null;
}
