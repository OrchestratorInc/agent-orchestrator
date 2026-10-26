import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
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
	Platform: fixtures.platform, View: "View", Text: "Text", Pressable: "Pressable",
	Animated: { View: "AnimatedView" }, StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
}));
vi.mock("@expo/ui", () => ({ Host: "Host" }));
vi.mock("@expo/ui/swift-ui", async () => (await import("./nativeFormTestDoubles")).swiftUIDouble());
vi.mock("@expo/ui/swift-ui/modifiers", async () => {
	const doubles = await import("./nativeFormTestDoubles");
	return doubles.modifierDouble(doubles.swiftUIModifierNames);
});
vi.mock("@expo/ui/jetpack-compose", async () => (await import("./nativeFormTestDoubles")).composeDouble());
vi.mock("@expo/ui/jetpack-compose/modifiers", async () => (await import("./nativeFormTestDoubles")).composeModifierDouble());
vi.mock("@expo/ui/community/bottom-sheet", () => ({ default: "BottomSheet", BottomSheetView: "BottomSheetView" }));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `${name}.xml` }));
vi.mock("expo-haptics", () => ({
	impactAsync: fixtures.impact, ImpactFeedbackStyle: { Light: "light" },
	selectionAsync: vi.fn(async () => {}), notificationAsync: vi.fn(async () => {}), NotificationFeedbackType: {},
	performAndroidHapticsAsync: fixtures.androidHaptic, AndroidHaptics: { Virtual_Key: "virtual-key", Segment_Tick: "segment-tick" },
}));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemeState: () => ({ scheme: "dark", theme: fixtures.theme }),
	useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme),
}));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./hosts", () => ({ loadHosts: async () => fixtures.hosts }));
vi.mock("./motionHooks", () => ({ useEnterTransition: () => ({}) }));
vi.mock("./RouteErrorBoundary", () => ({ SheetErrorBoundary: () => null }));

type SheetResults = typeof import("./sheetResult");
let renderer: ReactTestRenderer | undefined;
let results: SheetResults;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.platform.OS = "android";
	fixtures.params = { suggested: "Studio Mac", resultKey: "", symbol: "laptop" };
	fixtures.hosts = [];
	fixtures.back.mockClear();
	fixtures.impact.mockClear();
	fixtures.androidHaptic.mockClear();
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

// vitest resolves the base (iOS) files, so an Android render swaps in the
// Android form and fields. A fresh module graph per platform also means the
// sheet-result registry is fresh, so each test parks its callback there.
async function load(platform: "android" | "ios") {
	fixtures.platform.OS = platform;
	vi.resetModules();
	if (platform === "android") {
		vi.doMock("./formFields", async () => await import("./formFields.android"));
		vi.doMock("./MachineNameForm", async () => await import("./MachineNameForm.android"));
	} else {
		vi.doUnmock("./formFields");
		vi.doUnmock("./MachineNameForm");
	}
	results = await import("./sheetResult");
	results.resetSheetResults();
	return (await import("../app/sheets/machine-name")).default;
}

async function open(platform: "android" | "ios" = "android", onSelect?: (choice: unknown) => void) {
	const Sheet = await load(platform);
	if (onSelect) fixtures.params.resultKey = results.parkSheetResult(onSelect);
	await act(async () => { renderer = create(<Sheet />); });
}

const modifiersOf = (node: ReactTestInstance) => (node.props.modifiers ?? []) as { $type: string; args: unknown[] }[];
function nameField(): ReactTestInstance {
	return renderer!.root.find((node) => typeof node.type === "string" && (node.props.testID === "machine-name-input" || modifiersOf(node).some((m) => m.$type === "testID" && m.args[0] === "machine-name-input")));
}
async function typeName(text: string) {
	const field = nameField();
	await act(async () => (field.props.onTextChange ?? field.props.onValueChange)(text));
}
function saveButton() { return renderer!.root.findByProps({ accessibilityLabel: "Save machine" }); }
function symbolChoice(symbol: string) { return renderer!.root.findByProps({ testID: `machine-name-symbol-${symbol}` }); }

it.each(["android", "ios"] as const)("shows a text-only Save machine button on %s and saves the trimmed name", async (platform) => {
	const selected = vi.fn();
	await open(platform, selected);
	expect(saveButton().findAllByType("SymbolView" as never)).toHaveLength(0);
	await typeName("  Office Mac  ");
	act(() => saveButton().props.onPress());
	expect(selected).toHaveBeenCalledExactlyOnceWith({ name: "Office Mac", appearance: { symbol: "laptop" } });
	expect(fixtures.back).toHaveBeenCalledTimes(1);
	act(() => renderer!.unmount());
	renderer = undefined;
	expect(selected).toHaveBeenCalledTimes(1);
	expect(results.takeSheetResult(fixtures.params.resultKey)).toBeUndefined();
});

it.each([
	["android", "androidHaptic", "virtual-key"],
	["ios", "impact", "light"],
] as const)("plays one light haptic when opened on %s, not when typing", async (platform, channel, effect) => {
	await open(platform);
	expect(fixtures[channel]).toHaveBeenCalledExactlyOnceWith(effect);
	await typeName("Another name");
	expect(fixtures[channel]).toHaveBeenCalledTimes(1);
	expect(fixtures[channel === "impact" ? "androidHaptic" : "impact"]).not.toHaveBeenCalled();
});

it("uses a content-sized Android sheet with an outlined, focused name field", async () => {
	await open();
	const sheet = renderer!.root.findByType("BottomSheet" as never);
	expect(sheet.props.enableDynamicSizing).toBe(true);
	expect(sheet.props.snapPoints).toBeUndefined();
	expect(renderer!.root.findByType("BottomSheetView" as never).props.style?.flex).not.toBe(1);
	const field = nameField();
	expect(field.type).toBe("OutlinedTextField");
	expect(field.props.autoFocus).toBe(true);
	expect(field.findByType("OutlinedTextField.Label" as never).findByType("Text" as never).props.children).toBe("Name");
	// Sentence case: no tracked uppercase label stands over the field.
	expect(renderer!.root.findAll((node) => node.props.children === "MACHINE NAME")).toHaveLength(0);
});

it("keeps blank names disabled and ignores keyboard submit until a name is entered", async () => {
	const selected = vi.fn();
	await open("android", selected);
	await typeName("  ");
	expect(saveButton().props.accessibilityState.disabled).toBe(true);
	act(() => nameField().props.keyboardActions.onDone());
	expect(fixtures.back).not.toHaveBeenCalled();
	expect(selected).not.toHaveBeenCalled();
});

it("returns cancellation once when the Android sheet is dismissed", async () => {
	const selected = vi.fn();
	await open("android", selected);
	act(() => renderer!.root.findByType("BottomSheet" as never).props.onClose());
	expect(fixtures.back).toHaveBeenCalledTimes(1);
	act(() => renderer!.unmount());
	renderer = undefined;
	expect(selected).toHaveBeenCalledExactlyOnceWith(null);
});

it("starts on the suggested icon and saves the one the user picks", async () => {
	const selected = vi.fn();
	await open("android", selected);
	expect(symbolChoice("laptop").props.accessibilityState.selected).toBe(true);
	act(() => symbolChoice("linux").props.onPress());
	expect(symbolChoice("linux").props.accessibilityState.selected).toBe(true);
	expect(symbolChoice("laptop").props.accessibilityState.selected).toBe(false);
	act(() => saveButton().props.onPress());
	expect(selected).toHaveBeenCalledExactlyOnceWith({ name: "Studio Mac", appearance: { symbol: "linux" } });
});

it("falls back to the server icon when the suggested one is unknown", async () => {
	fixtures.params.symbol = "toaster";
	await open();
	expect(symbolChoice("server").props.accessibilityState.selected).toBe(true);
});

it("moves off an icon another machine already uses and says nothing more", async () => {
	fixtures.params.hostId = "h_new";
	fixtures.hosts = [{ id: "h_old", name: "MacBook Pro", appearance: { symbol: "laptop" } }];
	await open();
	expect(symbolChoice("server").props.accessibilityState.selected).toBe(true);
	expect(renderer!.root.findAllByProps({ testID: "machine-name-symbol-shared" })).toHaveLength(0);
});

it("lets the user keep a shared icon and names the machines that share it", async () => {
	const selected = vi.fn();
	fixtures.params.hostId = "h_new";
	fixtures.hosts = [
		{ id: "h_new", name: "Being re-named", appearance: { symbol: "laptop" } },
		{ id: "h_old", name: "MacBook Pro", appearance: { symbol: "laptop" } },
	];
	await open("android", selected);
	act(() => symbolChoice("laptop").props.onPress());
	const hint = renderer!.root.findByProps({ testID: "machine-name-symbol-shared" });
	expect(hint.props.children).toBe("Also used by MacBook Pro. The name will tell them apart.");
	act(() => saveButton().props.onPress());
	expect(selected).toHaveBeenCalledExactlyOnceWith({ name: "Studio Mac", appearance: { symbol: "laptop" } });
});

// iOS heads the form sheet with its native bar: the title there, Cancel on the
// left and Save on the right, over a native form with a name row and an icon
// menu. Android keeps its Material sheet with the title and the Save button in
// the body.
it("puts the title, Cancel and Save in the iOS bar over a native form", async () => {
	const selected = vi.fn();
	await open("ios", selected);
	const texts = renderer!.root.findAllByType("Text" as never).map((node) => node.props.children);
	expect(texts).not.toContain("Name this machine");
	expect(renderer!.root.findAllByProps({ title: "Save machine" })).toHaveLength(0);
	const form = renderer!.root.findByType("Form" as never);
	expect(form.findByType("LabeledContent" as never).props.label).toBe("Name");
	expect(nameField().props.autoFocus).toBe(true);
	const [left, right] = renderer!.root.findAllByType("StackToolbar" as never);
	expect(left.props.placement).toBe("left");
	expect(right.props.placement).toBe("right");
	expect(saveButton().props.children).toBe("Save");
	await typeName("   ");
	expect(saveButton().props.disabled).toBe(true);
	const cancel = left.findByType("StackToolbarButton" as never);
	expect(cancel.props.children).toBe("Cancel");
	act(() => cancel.props.onPress());
	expect(fixtures.back).toHaveBeenCalledTimes(1);
	act(() => renderer!.unmount());
	renderer = undefined;
	expect(selected).toHaveBeenCalledExactlyOnceWith(null);
});

it("picks the iOS icon from a menu of system symbols and names any machine sharing it", async () => {
	const selected = vi.fn();
	fixtures.params.hostId = "h_new";
	fixtures.hosts = [{ id: "h_old", name: "MacBook Pro", appearance: { symbol: "cloud" } }];
	await open("ios", selected);
	const picker = renderer!.root.findByType("Picker" as never);
	expect(picker.props.label).toBe("Icon");
	expect(modifiersOf(picker)).toContainEqual({ $type: "pickerStyle", args: ["menu"] });
	expect(picker.findAllByType("Label" as never).map((option) => option.props.systemImage)).toEqual([
		"server.rack", "cloud", "terminal", "desktopcomputer", "laptopcomputer", "macmini", "macstudio",
	]);
	expect(picker.props.selection).toBe("laptop");
	act(() => picker.props.onSelectionChange("cloud"));
	const iconSection = renderer!.root.findAllByType("Section" as never)[1];
	expect(iconSection.props.footer.props.children).toBe("Also used by MacBook Pro. The name will tell them apart.");
	act(() => saveButton().props.onPress());
	expect(selected).toHaveBeenCalledExactlyOnceWith({ name: "Studio Mac", appearance: { symbol: "cloud" } });
});

it("keeps the Android title and Save button in the sheet body", async () => {
	await open();
	expect(renderer!.root.findAllByType("StackToolbar" as never)).toHaveLength(0);
	expect(renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.children === "Name this machine")).toHaveLength(1);
	expect(renderer!.root.findAllByProps({ title: "Save machine" }).length).toBeGreaterThan(0);
});
