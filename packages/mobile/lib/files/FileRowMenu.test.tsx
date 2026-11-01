import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ show: vi.fn(), press: vi.fn() }));

vi.mock("react-native", async () => {
	const React = await import("react");
	return {
		Platform: { OS: "ios" },
		Pressable: ({ children, style, ...props }: { children?: React.ReactNode; style?: unknown }) =>
			React.createElement("Pressable", { ...props, style: typeof style === "function" ? style({ pressed: false }) : style }, children),
		useWindowDimensions: () => ({ width: 390, height: 844 }),
	};
});
vi.mock("@expo/ui/community/menu", async () => {
	const React = await import("react");
	return {
		MenuView: ({ children, ref, ...props }: { children?: React.ReactNode; ref?: React.Ref<{ show(): void }> }) => {
			React.useImperativeHandle(ref, () => ({ show: fixtures.show }));
			return React.createElement("MenuView", props, children);
		},
	};
});
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: { color: "#ripple" }, pressed: { backgroundColor: "#pressed" } }) }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({ textPrimary: "#textPrimary", textSecondary: "#textSecondary" }), useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("../haptics", () => ({ haptics: { press: fixtures.press } }));
vi.mock("./fileIcons", () => ({ fileIcon: (name: string) => `drawable:${name}` }));

import { FileRowMenu } from "./FileRowMenu";
import { FileRowMenu as AndroidFileRowMenu } from "./FileRowMenu.android";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	fixtures.show.mockClear();
	fixtures.press.mockClear();
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

const props = (onAction = vi.fn(), onPress = vi.fn()) => ({
	actions: ["openDiff", "openFile", "copyPath", "share"] as const,
	onAction,
	onPress,
	accessibilityLabel: "auth.ts in src, modified",
	accessibilityHint: "Opens the diff",
	style: { minHeight: 52 },
	children: null,
});

describe("FileRowMenu on iOS", () => {
	it("opens the system context menu on a long press, with SF Symbols", async () => {
		const onAction = vi.fn();
		await act(async () => { renderer = create(<FileRowMenu {...props(onAction)} />); });
		const menu = renderer!.root.findByType("MenuView" as never);
		expect(menu.props.shouldOpenOnLongPress).toBe(true);
		expect(menu.props.actions.map((action: { title: string; image: string }) => [action.title, action.image])).toEqual([
			["Open diff", "plus.forwardslash.minus"],
			["Open file", "doc.text"],
			["Copy path", "doc.on.doc"],
			["Share", "square.and.arrow.up"],
		]);
		act(() => menu.props.onPressAction({ nativeEvent: { event: "copyPath" } }));
		expect(onAction).toHaveBeenCalledWith("copyPath");
	});

	it("keeps the row a button that spans the list and offers the menu to screen readers", async () => {
		const onAction = vi.fn();
		const onPress = vi.fn();
		await act(async () => { renderer = create(<FileRowMenu {...props(onAction, onPress)} />); });
		const row = renderer!.root.findByType("Pressable" as never);
		expect(row.props.accessibilityRole).toBe("button");
		expect(row.props.style).toContainEqual({ width: 390 });
		expect(row.props.onLongPress).toBeUndefined();
		expect(row.props.accessibilityActions.map((action: { label: string }) => action.label)).toEqual(["Open diff", "Open file", "Copy path", "Share"]);
		act(() => row.props.onAccessibilityAction({ nativeEvent: { actionName: "share" } }));
		expect(onAction).toHaveBeenCalledWith("share");
		act(() => row.props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
	});
});

describe("FileRowMenu on Android", () => {
	it("opens Material's menu from the row's own long press, with a ripple and drawables", async () => {
		await act(async () => { renderer = create(<AndroidFileRowMenu {...props()} />); });
		const menu = renderer!.root.findByType("MenuView" as never);
		expect(menu.props.colorScheme).toBe("dark");
		expect(menu.props.actions.map((action: { image: string }) => action.image)).toEqual(["drawable:openDiff", "drawable:openFile", "drawable:copy", "drawable:share"]);
		const row = renderer!.root.findByType("Pressable" as never);
		expect(row.props.android_ripple).toEqual({ color: "#ripple" });
		act(() => row.props.onLongPress());
		expect(fixtures.show).toHaveBeenCalledOnce();
		expect(fixtures.press).toHaveBeenCalledOnce();
	});
});
