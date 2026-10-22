import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	};
});
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_target, property) => String(property) });
	return { useTheme: () => theme };
});

import { NativeHeaderButton, type NativeHeaderButtonIcon } from "./native-header-button";

const ALL_ICONS: NativeHeaderButtonIcon[] = ["menu", "bell", "close", "check", "back", "more", "add"];

const EXPECTED_GLYPHS: Record<NativeHeaderButtonIcon, string> = {
	menu: "menu",
	bell: "bell",
	close: "x",
	check: "check",
	back: "chevron-left",
	more: "more-horizontal",
	add: "plus",
};

function glyphName(renderer: ReactTestRenderer): unknown {
	const feathers = renderer.root.findAll((node) => (node.type as unknown) === "Feather");
	expect(feathers).toHaveLength(1);
	return feathers[0].props.name;
}

describe("NativeHeaderButton (web fallback)", () => {
	it("renders the Feather bell glyph for the bell icon", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<NativeHeaderButton icon="bell" label="Notifications" onPress={() => {}} />);
		});

		expect(glyphName(renderer)).toBe("bell");
	});

	it.each(ALL_ICONS)("maps %s to a real glyph and no unicode dot fallback remains", (icon) => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<NativeHeaderButton icon={icon} label={`Label ${icon}`} onPress={() => {}} />);
		});

		expect(glyphName(renderer)).toBe(EXPECTED_GLYPHS[icon]);
		const rendered = JSON.stringify(renderer.toJSON());
		expect(rendered).not.toContain("◉");
		expect(rendered).not.toContain("☰");
	});

	it("keeps the pill button contract: testID, accessibility label, and press", () => {
		let pressed = 0;
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<NativeHeaderButton icon="bell" label="Notifications" onPress={() => { pressed += 1; }} />);
		});

		const button = renderer.root.findByProps({ testID: "header-bell" });
		expect(button.props.accessibilityRole).toBe("button");
		expect(button.props.accessibilityLabel).toBe("Notifications");
		expect(button.props.style({ pressed: false })[0].width).toBe(44);
		expect(button.props.style({ pressed: false })[0].height).toBe(44);
		expect(button.props.style({ pressed: false })[0].borderRadius).toBe(999);
		act(() => {
			button.props.onPress();
		});
		expect(pressed).toBe(1);
	});
});
