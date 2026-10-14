import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

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
vi.mock("./motionHooks", () => ({ useEnterTransition: () => ({}), usePressScale: () => ({}) }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({ fontMono: "GeistMono" }, { get: (target, property) => property in target ? target[property as keyof typeof target] : "#888" });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});

import { ScreenHeader } from "./ui";

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
});
