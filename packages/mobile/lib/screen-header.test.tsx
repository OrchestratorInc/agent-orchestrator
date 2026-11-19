import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

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
		Platform: { OS: "ios" },
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
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => false }));
vi.mock("./motionHooks", () => ({ useEnterTransition: () => ({}) }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({ fontMono: "GeistMono" }, { get: (target, property) => property in target ? target[property as keyof typeof target] : "#888" });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});

import { ListSectionHeader, NumberedStep, SheetHeader } from "./ui";

describe("SheetHeader", () => {
	it("marks its title as a heading", () => {
		const renderer = render(<SheetHeader title="Choose a model" subtitle="Used for new turns" />);
		expect(textNode(renderer, "Choose a model").props.accessibilityRole).toBe("header");
		expect(textNode(renderer, "Used for new turns").props.accessibilityRole).toBeUndefined();
	});

	// Under a native bar that already carries the title, only the subtitle stays.
	it("draws no title row when the bar carries the title", () => {
		const renderer = render(<SheetHeader subtitle="Choose a name you'll recognize." right="Refresh" />);
		expect(renderer.root.findAll((node) => (node.type as unknown) === "Text" && node.props.accessibilityRole === "header")).toHaveLength(0);
		expect(textNode(renderer, "Choose a name you'll recognize.")).toBeDefined();
		expect(renderer.root.findAll((node) => node.children.includes("Refresh" as never))).toHaveLength(0);
	});
});

describe("ListSectionHeader", () => {
	it("reads a fixed section as a heading with its count", () => {
		const view = render(<ListSectionHeader label="Pinned" count={3} />).root.findAllByType("View" as never)[0];
		expect(view.props).toMatchObject({ accessible: true, accessibilityRole: "header", accessibilityLabel: "Pinned, 3" });
	});

	it("reports whether a foldable header is expanded", () => {
		const pressable = render(<ListSectionHeader label="Recent" open onToggle={() => {}} />).root.findByType("Pressable" as never);
		expect(pressable.props.accessibilityState).toEqual({ expanded: true });
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
