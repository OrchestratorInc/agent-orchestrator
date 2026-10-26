import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "android" },
	params: { suggested: "Studio Mac", resultKey: "", symbol: "laptop" } as { suggested: string; resultKey: string; symbol?: string; hostId?: string },
	back: vi.fn(),
	impact: vi.fn(async () => {}),
	androidHaptic: vi.fn(async () => {}),
	theme: new Proxy({}, { get: () => "#000000" }),
	hosts: [] as { id: string; name: string; appearance?: { symbol: string } }[],
}));
vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	const router = { back: fixtures.back, canGoBack: () => true };
	return {
		Stack: { Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton") }) },
		useRouter: () => router,
		useLocalSearchParams: () => fixtures.params,
	};
});
vi.mock("react-native", () => ({
	Platform: fixtures.platform, View: "View", Text: "Text", TextInput: "TextInput", Pressable: "Pressable",
	Animated: { View: "AnimatedView" }, StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
}));
vi.mock("@expo/ui/community/bottom-sheet", () => ({ default: "BottomSheet", BottomSheetView: "BottomSheetView" }));
vi.mock("expo-haptics", () => ({
	impactAsync: fixtures.impact, ImpactFeedbackStyle: { Light: "light" },
	selectionAsync: vi.fn(async () => {}), notificationAsync: vi.fn(async () => {}), NotificationFeedbackType: {},
	performAndroidHapticsAsync: fixtures.androidHaptic, AndroidHaptics: { Virtual_Key: "virtual-key" },
}));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme) }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./MachineIdentity", () => ({ MachineAvatar: "MachineAvatar" }));
vi.mock("./hosts", () => ({ loadHosts: async () => fixtures.hosts }));
vi.mock("./motionHooks", () => ({ useEnterTransition: () => ({}) }));
vi.mock("./native-header-button", () => ({ NativeHeaderButton: "NativeHeaderButton" }));
vi.mock("./RouteErrorBoundary", () => ({ SheetErrorBoundary: () => null }));

import MachineNameSheet from "../app/sheets/machine-name";
import { parkSheetResult, resetSheetResults, takeSheetResult } from "./sheetResult";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.platform.OS = "android";
	fixtures.params = { suggested: "Studio Mac", resultKey: "", symbol: "laptop" };
	fixtures.hosts = [];
	fixtures.back.mockClear();
	fixtures.impact.mockClear();
	fixtures.androidHaptic.mockClear();
	resetSheetResults();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

function open() { act(() => { renderer = create(<MachineNameSheet />); }); }
function input() { return renderer!.root.findByType("TextInput" as never); }
function saveButton() { return renderer!.root.findByProps({ accessibilityLabel: "Save machine" }); }
function symbolChoice(symbol: string) { return renderer!.root.findByProps({ testID: `machine-name-symbol-${symbol}` }); }

it.each(["android", "ios"])("shows a text-only Save machine button on %s and saves the trimmed name", (platform) => {
	fixtures.platform.OS = platform;
	const selected = vi.fn();
	fixtures.params.resultKey = parkSheetResult(selected);
	open();
	expect(saveButton().findAllByType("Feather" as never)).toHaveLength(0);
	act(() => input().props.onChangeText("  Office Mac  "));
	act(() => saveButton().props.onPress());
	expect(selected).toHaveBeenCalledExactlyOnceWith({ name: "Office Mac", appearance: { symbol: "laptop" } });
	expect(fixtures.back).toHaveBeenCalledTimes(1);
	act(() => renderer!.unmount());
	expect(selected).toHaveBeenCalledTimes(1);
	expect(takeSheetResult(fixtures.params.resultKey)).toBeUndefined();
});

it.each([
	["android", "androidHaptic", "virtual-key"],
	["ios", "impact", "light"],
] as const)("plays one light haptic when opened on %s, not when typing", (platform, channel, effect) => {
	fixtures.platform.OS = platform;
	open();
	expect(fixtures[channel]).toHaveBeenCalledExactlyOnceWith(effect);
	act(() => input().props.onChangeText("Another name"));
	expect(fixtures[channel]).toHaveBeenCalledTimes(1);
	expect(fixtures[channel === "impact" ? "androidHaptic" : "impact"]).not.toHaveBeenCalled();
});

it("uses a content-sized Android sheet without fixed snap points or a stretching form", () => {
	open();
	const sheet = renderer!.root.findByType("BottomSheet" as never);
	expect(sheet.props.enableDynamicSizing).toBe(true);
	expect(sheet.props.snapPoints).toBeUndefined();
	expect(renderer!.root.findByType("BottomSheetView" as never).props.style?.flex).not.toBe(1);
	expect(input().props.autoFocus).toBe(true);
});

it("keeps blank names disabled and ignores keyboard submit until a name is entered", () => {
	const selected = vi.fn();
	fixtures.params.resultKey = parkSheetResult(selected);
	open();
	act(() => input().props.onChangeText("  "));
	expect(saveButton().props.disabled).toBe(true);
	act(() => input().props.onSubmitEditing());
	expect(fixtures.back).not.toHaveBeenCalled();
	expect(selected).not.toHaveBeenCalled();
});

it("returns cancellation once when the Android sheet is dismissed", () => {
	const selected = vi.fn();
	fixtures.params.resultKey = parkSheetResult(selected);
	open();
	act(() => renderer!.root.findByType("BottomSheet" as never).props.onClose());
	expect(fixtures.back).toHaveBeenCalledTimes(1);
	act(() => renderer!.unmount());
	expect(selected).toHaveBeenCalledExactlyOnceWith(null);
});

it("starts on the suggested icon and saves the one the user picks", () => {
	const selected = vi.fn();
	fixtures.params.resultKey = parkSheetResult(selected);
	open();
	expect(symbolChoice("laptop").props.accessibilityState.selected).toBe(true);
	act(() => symbolChoice("linux").props.onPress());
	expect(symbolChoice("linux").props.accessibilityState.selected).toBe(true);
	expect(symbolChoice("laptop").props.accessibilityState.selected).toBe(false);
	act(() => saveButton().props.onPress());
	expect(selected).toHaveBeenCalledExactlyOnceWith({ name: "Studio Mac", appearance: { symbol: "linux" } });
});

it("falls back to the server icon when the suggested one is unknown", () => {
	fixtures.params.symbol = "toaster";
	open();
	expect(symbolChoice("server").props.accessibilityState.selected).toBe(true);
});

async function openAndLoadHosts() {
	await act(async () => { renderer = create(<MachineNameSheet />); });
}

it("moves off an icon another machine already uses and says nothing more", async () => {
	fixtures.params.hostId = "h_new";
	fixtures.hosts = [{ id: "h_old", name: "MacBook Pro", appearance: { symbol: "laptop" } }];
	await openAndLoadHosts();
	expect(symbolChoice("server").props.accessibilityState.selected).toBe(true);
	expect(renderer!.root.findAllByProps({ testID: "machine-name-symbol-shared" })).toHaveLength(0);
});

it("lets the user keep a shared icon and names the machines that share it", async () => {
	const selected = vi.fn();
	fixtures.params.resultKey = parkSheetResult(selected);
	fixtures.params.hostId = "h_new";
	fixtures.hosts = [
		{ id: "h_new", name: "Being re-named", appearance: { symbol: "laptop" } },
		{ id: "h_old", name: "MacBook Pro", appearance: { symbol: "laptop" } },
	];
	await openAndLoadHosts();
	act(() => symbolChoice("laptop").props.onPress());
	const hint = renderer!.root.findByProps({ testID: "machine-name-symbol-shared" });
	expect(hint.props.children.join("")).toBe("Also used by MacBook Pro. The name will tell them apart.");
	act(() => saveButton().props.onPress());
	expect(selected).toHaveBeenCalledExactlyOnceWith({ name: "Studio Mac", appearance: { symbol: "laptop" } });
});

// iOS heads the form sheet with its native bar: the title there, Cancel on the
// left and Save on the right. Android keeps its Material sheet with the title
// and the Save button in the body.
it("puts the title, Cancel and Save in the iOS bar and nowhere in the body", () => {
	fixtures.platform.OS = "ios";
	const selected = vi.fn();
	fixtures.params.resultKey = parkSheetResult(selected);
	open();
	const texts = renderer!.root.findAllByType("Text" as never).map((node) => node.props.children);
	expect(texts).not.toContain("Name this machine");
	expect(renderer!.root.findAllByProps({ title: "Save machine" })).toHaveLength(0);
	const [left, right] = renderer!.root.findAllByType("StackToolbar" as never);
	expect(left.props.placement).toBe("left");
	expect(right.props.placement).toBe("right");
	expect(saveButton().props.children).toBe("Save");
	act(() => input().props.onChangeText("   "));
	expect(saveButton().props.disabled).toBe(true);
	const cancel = left.findByType("StackToolbarButton" as never);
	expect(cancel.props.children).toBe("Cancel");
	act(() => cancel.props.onPress());
	expect(fixtures.back).toHaveBeenCalledTimes(1);
	act(() => renderer!.unmount());
	renderer = undefined;
	expect(selected).toHaveBeenCalledExactlyOnceWith(null);
});

it("keeps the Android title and Save button in the sheet body", () => {
	open();
	expect(renderer!.root.findAllByType("StackToolbar" as never)).toHaveLength(0);
	expect(renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.children === "Name this machine")).toHaveLength(1);
	expect(renderer!.root.findAllByProps({ title: "Save machine" }).length).toBeGreaterThan(0);
});
