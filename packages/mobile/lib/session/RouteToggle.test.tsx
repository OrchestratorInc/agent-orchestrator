import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: { accent: "#accent", accentTint: "#tint", onAccent: "#onAccent", textSecondary: "#secondary" },
}));

vi.mock("react-native", () => ({ StyleSheet: { create: (styles: object) => styles } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("../toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children) };
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Image: host("Image"), Toggle: host("Toggle") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	accessibilityLabel: (label: string) => ({ label }),
	background: (color: string, shape: object) => ({ background: color, shape }),
	buttonStyle: (style: string) => ({ buttonStyle: style }),
	contentShape: (shape: object) => ({ contentShape: shape }),
	frame: (size: object) => ({ frame: size }),
	shapes: { circle: () => "circle" },
	toggleStyle: (style: string) => ({ toggleStyle: style }),
}));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { FilledIconToggleButton: host("FilledIconToggleButton"), Host: host("ComposeHost"), Icon: host("Icon") };
});

import { RouteToggle } from "./RouteToggle";
import { RouteToggle as AndroidRouteToggle } from "./RouteToggle.android";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("the iOS route toggle", () => {
	it("is a button-style SwiftUI toggle that always shows the terminal glyph", async () => {
		const onChange = vi.fn();
		await act(async () => { renderer = create(<RouteToggle on={false} onChange={onChange} />); });
		const toggle = renderer!.root.findByType("Toggle" as never);
		expect(toggle.props.isOn).toBe(false);
		expect(toggle.props.modifiers).toEqual(expect.arrayContaining([{ toggleStyle: "button" }, { label: "Send to terminal" }]));
		const glyph = renderer!.root.findByType("Image" as never);
		expect(glyph.props.systemName).toBe("terminal");
		expect(glyph.props.modifiers).toContainEqual({ frame: { width: 44, height: 44 } });
		expect(glyph.props.modifiers).not.toContainEqual(expect.objectContaining({ background: expect.anything() }));
		act(() => toggle.props.onIsOnChange(true));
		expect(onChange).toHaveBeenCalledWith(true);
	});

	it("fills the circle while it is on, and keeps the same glyph", async () => {
		await act(async () => { renderer = create(<RouteToggle on onChange={vi.fn()} />); });
		expect(renderer!.root.findByType("Toggle" as never).props.isOn).toBe(true);
		const glyph = renderer!.root.findByType("Image" as never);
		expect(glyph.props.systemName).toBe("terminal");
		expect(glyph.props.color).toBe("#onAccent");
		expect(glyph.props.modifiers).toContainEqual({ background: "#accent", shape: "circle" });
	});
});

describe("the Android route toggle", () => {
	it("is Material's filled icon toggle button with the terminal symbol", async () => {
		const onChange = vi.fn();
		await act(async () => { renderer = create(<AndroidRouteToggle on onChange={onChange} />); });
		const button = renderer!.root.findByType("FilledIconToggleButton" as never);
		expect(button.props.checked).toBe(true);
		expect(button.props.colors).toMatchObject({ checkedContainerColor: "#accent", checkedContentColor: "#onAccent" });
		expect(button.findByType("Icon" as never).props).toMatchObject({ source: "drawable:terminal", contentDescription: "Send to terminal" });
		act(() => button.props.onCheckedChange(false));
		expect(onChange).toHaveBeenCalledWith(false);
	});
});
