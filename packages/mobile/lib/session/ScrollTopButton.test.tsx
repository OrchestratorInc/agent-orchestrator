import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios", Version: "26.0" },
	theme: { textPrimary: "#primary", bgElevated: "#elevated", borderDefault: "#border", textSecondary: "#secondary" },
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Platform: fixtures.platform, Pressable: host("Pressable"), StyleSheet: { create: (styles: object) => styles }, View: host("View") };
});
vi.mock("../ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemeState: () => ({ scheme: "dark" }),
	useThemedStyles: (factory: (t: typeof fixtures.theme) => unknown) => factory(fixtures.theme),
}));
vi.mock("../divider", () => ({ rowDividerWidth: 1 }));
vi.mock("../glass", () => ({ glassCircle: () => ({ glass: "circle" }) }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: { opacity: 0.7 } }) }));
vi.mock("../toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children) };
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Group: host("Group"), Image: host("Image") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	accessibilityAddTraits: (traits: string[]) => ({ traits }),
	accessibilityLabel: (label: string) => ({ label }),
	frame: (size: object) => ({ frame: size }),
	onTapGesture: (handler: () => void) => ({ onTap: handler }),
}));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("ComposeHost"),
		Icon: host("Icon"),
		SmallFloatingActionButton: Object.assign(host("SmallFloatingActionButton"), { Icon: host("FabIconSlot") }),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({ paddingAll: (all: number) => ({ paddingAll: all }) }));

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.resetModules();
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

describe("the iOS scroll-to-top button", () => {
	it("is a Liquid Glass circle the size of the touch target on iOS 26", async () => {
		fixtures.platform.Version = "26.0";
		const { ScrollTopButton } = await import("./ScrollTopButton");
		const onPress = vi.fn();
		await act(async () => { renderer = create(<ScrollTopButton onPress={onPress} />); });
		const group = renderer!.root.findByType("Group" as never);
		expect(group.props.modifiers).toEqual(expect.arrayContaining([{ frame: { width: 44, height: 44 } }, { glass: "circle" }, { traits: ["isButton"] }, { label: "Scroll to top" }]));
		expect(renderer!.root.findByType("Image" as never).props.systemName).toBe("arrow.up.to.line");
		const tap = group.props.modifiers.find((modifier: { onTap?: () => void }) => modifier.onTap);
		act(() => tap.onTap());
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("falls back to a filled circle where there is no glass", async () => {
		fixtures.platform.Version = "18.4";
		const { ScrollTopButton } = await import("./ScrollTopButton");
		const onPress = vi.fn();
		await act(async () => { renderer = create(<ScrollTopButton onPress={onPress} />); });
		expect(renderer!.root.findAllByType("Group" as never)).toHaveLength(0);
		const button = renderer!.root.findByType("Pressable" as never);
		expect(button.props).toMatchObject({ accessibilityRole: "button", accessibilityLabel: "Scroll to top" });
		expect(renderer!.root.findByType("SymbolView" as never).props.name).toEqual({ ios: "arrow.up.to.line", android: "vertical_align_top" });
		act(() => button.props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
	});
});

describe("the Android scroll-to-top button", () => {
	it("is Material's small FAB on the elevated surface, named for TalkBack", async () => {
		const { ScrollTopButton } = await import("./ScrollTopButton.android");
		const onPress = vi.fn();
		await act(async () => { renderer = create(<ScrollTopButton onPress={onPress} />); });
		const fab = renderer!.root.findByType("SmallFloatingActionButton" as never);
		expect(fab.props.containerColor).toBe("#elevated");
		const icon = fab.findByType("FabIconSlot" as never).findByType("Icon" as never);
		expect(icon.props).toMatchObject({ source: "drawable:vertical_align_top", tint: "#primary", contentDescription: "Scroll to top" });
		act(() => fab.props.onClick());
		expect(onPress).toHaveBeenCalledOnce();
	});
});
