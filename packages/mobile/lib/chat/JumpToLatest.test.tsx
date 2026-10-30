import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const platform = vi.hoisted(() => ({ OS: "ios", Version: "26.0" }));

vi.mock("react-native", () => ({ Platform: platform, StyleSheet: { create: (styles: object) => styles }, View: "View" }));
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
	return { Button: host("SwiftButton"), Image: host("SwiftImage") };
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
	it("is a small Liquid Glass circle holding only the arrow, named for screen readers", async () => {
		const onPress = vi.fn();
		const button = await renderIOS(onPress);
		expect(button.props.modifiers).toEqual(expect.arrayContaining([
			{ buttonStyle: "glass" },
			{ buttonBorderShape: "circle" },
			{ controlSize: "small" },
			{ tint: "#ink" },
			{ accessibilityLabel: "Jump to latest message" },
		]));
		expect(button.findByType("SwiftImage" as never).props.systemName).toBe("arrow.down");
		expect(button.findAllByType("SwiftText" as never)).toHaveLength(0);
		act(() => button.props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
	});

	// Dynamic Type has to reach the arrow, which a bare point size would not.
	it("sizes its arrow by text style", async () => {
		const button = await renderIOS();
		const arrow = button.findByType("SwiftImage" as never);
		expect(arrow.props.modifiers.find((modifier: { font?: object }) => modifier.font).font).toEqual({ textStyle: "footnote", weight: "semibold" });
		expect(arrow.props.size).toBeUndefined();
	});

	// Centred just above the composer, along the bottom of the list's room.
	it("sits centred at the bottom of the space it is given", async () => {
		const { JumpToLatest } = await import("./JumpToLatest");
		await act(async () => { renderer = create(<JumpToLatest onPress={vi.fn()} />); });
		expect(renderer!.root.findByType("View" as never).props.style).toMatchObject({ position: "absolute", left: 0, right: 0, alignItems: "center" });
	});

	// Before iOS 26 the glass styles draw no material at all.
	it("fills the circle with the elevated surface before Liquid Glass", async () => {
		platform.Version = "18.4";
		const button = await renderIOS();
		expect(button.props.modifiers).toEqual(expect.arrayContaining([{ buttonStyle: "borderedProminent" }, { tint: "#elevated" }]));
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

	it("sits centred just above the composer", async () => {
		const { JumpToLatest } = await import("./JumpToLatest.android");
		await act(async () => { renderer = create(<JumpToLatest onPress={vi.fn()} />); });
		expect(renderer!.root.findByType("View" as never).props.style).toMatchObject({ position: "absolute", left: 0, right: 0, alignItems: "center" });
	});
});
