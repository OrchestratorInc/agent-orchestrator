import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const platform = vi.hoisted(() => ({ OS: "ios", Version: "26.0" }));

vi.mock("react-native", () => ({ Platform: platform, StyleSheet: { create: (styles: object) => styles } }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => ({ bgElevated: "#elevated", textPrimary: "#ink" }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("../toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("SwiftHost", props, children) };
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Button: host("SwiftButton"), HStack: host("HStack"), Image: host("SwiftImage"), Text: host("SwiftText") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	accessibilityLabel: (label: string) => ({ accessibilityLabel: label }),
	buttonBorderShape: (shape: string) => ({ buttonBorderShape: shape }),
	buttonStyle: (style: string) => ({ buttonStyle: style }),
	controlSize: (size: string) => ({ controlSize: size }),
	font: (params: object) => ({ font: params }),
	foregroundStyle: (color: string) => ({ foregroundStyle: color }),
	tint: (color: string) => ({ tint: color }),
}));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("ComposeHost"),
		SmallFloatingActionButton: Object.assign(host("SmallFloatingActionButton"), { Icon: host("FabIconSlot") }),
		Icon: host("Icon"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({ paddingAll: (all: number) => ({ paddingAll: all }) }));

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
	platform.Version = "26.0";
	vi.resetModules();
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

async function renderIOS(onPress = vi.fn()) {
	const { JumpToLatest } = await import("./JumpToLatest");
	await act(async () => { renderer = create(<JumpToLatest onPress={onPress} />); });
	return renderer!.root.findByType("SwiftButton" as never);
}

describe("JumpToLatest on iOS", () => {
	it("is a Liquid Glass capsule named for screen readers", async () => {
		const onPress = vi.fn();
		const button = await renderIOS(onPress);
		expect(button.props.modifiers).toEqual(expect.arrayContaining([
			{ buttonStyle: "glass" },
			{ buttonBorderShape: "capsule" },
			{ tint: "#ink" },
			{ accessibilityLabel: "Jump to latest message" },
		]));
		expect(button.findByType("SwiftImage" as never).props.systemName).toBe("arrow.down");
		expect(button.findByType("SwiftText" as never).props.children).toBe("Latest");
		act(() => button.props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
	});

	// Dynamic Type has to reach the label and its arrow, which a bare point size would not.
	it("sizes its label and arrow by text style", async () => {
		const button = await renderIOS();
		const fontOf = (type: string) => button.findByType(type as never).props.modifiers.find((modifier: { font?: object }) => modifier.font).font;
		expect(fontOf("SwiftText")).toMatchObject({ textStyle: "subheadline" });
		expect(fontOf("SwiftImage")).toEqual({ textStyle: "subheadline", weight: "semibold" });
		expect(button.findByType("SwiftImage" as never).props.size).toBeUndefined();
	});

	// SwiftUI finds a custom font by the name inside the font file; expo-font's
	// alias would leave the label in the system face.
	it("names Geist by its PostScript name", async () => {
		const button = await renderIOS();
		const font = button.findByType("SwiftText" as never).props.modifiers.find((modifier: { font?: object }) => modifier.font).font;
		expect(font.family).toBe("Geist-SemiBold");
	});

	// Before iOS 26 the glass styles draw no material at all.
	it("fills the capsule with the elevated surface before Liquid Glass", async () => {
		platform.Version = "18.4";
		const button = await renderIOS();
		expect(button.props.modifiers).toEqual(expect.arrayContaining([{ buttonStyle: "borderedProminent" }, { tint: "#elevated" }]));
		expect(button.findByType("SwiftText" as never).props.modifiers).toContainEqual({ foregroundStyle: "#ink" });
	});
});

describe("JumpToLatest on Android", () => {
	it("is Material's small FAB with the downward arrow, named for screen readers", async () => {
		const { JumpToLatest } = await import("./JumpToLatest.android");
		const onPress = vi.fn();
		await act(async () => { renderer = create(<JumpToLatest onPress={onPress} />); });
		const fab = renderer!.root.findByType("SmallFloatingActionButton" as never);
		expect(fab.props.containerColor).toBe("#elevated");
		const icon = fab.findByType("FabIconSlot" as never).findByType("Icon" as never);
		expect(icon.props).toMatchObject({ source: "drawable:arrow_downward", tint: "#ink", contentDescription: "Jump to latest message" });
		act(() => fab.props.onClick());
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("sits 16dp in from the bottom-end corner", async () => {
		const { JumpToLatest } = await import("./JumpToLatest.android");
		await act(async () => { renderer = create(<JumpToLatest onPress={vi.fn()} />); });
		const host = renderer!.root.findByType("ComposeHost" as never);
		const padding = renderer!.root.findByType("SmallFloatingActionButton" as never).props.modifiers[0].paddingAll;
		expect(host.props.style).toMatchObject({ position: "absolute", end: 16 - padding, bottom: 16 - padding });
	});
});
