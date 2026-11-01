import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios" },
	params: { sheetKey: "" } as { sheetKey?: string },
	back: vi.fn(),
	theme: new Proxy({}, { get: () => "#000000" }),
}));
vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	const router = { back: fixtures.back, canGoBack: () => true, replace: vi.fn() };
	return {
		Stack: { Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton") }) },
		useRouter: () => router,
		useLocalSearchParams: () => fixtures.params,
	};
});
vi.mock("react-native", () => ({
	Platform: fixtures.platform, TextInput: "TextInput",
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
}));
vi.mock("react-native-keyboard-controller", () => ({ KeyboardAvoidingView: "KeyboardAvoidingView" }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme),
}));
vi.mock("../textSelection", () => ({ textSelectionColors: () => ({}) }));
vi.mock("../RouteErrorBoundary", () => ({ SheetErrorBoundary: () => null }));

const { chatSheetRoute, readChatSheet, resetChatSheets } = await import("./chatSheetRegistry");
const { default: ComposerEditorRoute } = await import("../../app/sheets/composer-editor");

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.platform.OS = "ios";
	resetChatSheets();
});
afterEach(() => {
	if (renderer) act(() => renderer!.unmount());
	renderer = undefined;
	vi.clearAllMocks();
	vi.unstubAllGlobals();
});

function open(text = "Fix the flaky test") {
	const onChange = vi.fn();
	const onSend = vi.fn();
	const route = chatSheetRoute({ kind: "composer-editor", text, placeholder: "Ask Codex", sendLabel: "Send", onChange, onSend });
	fixtures.params = route.params;
	act(() => { renderer = create(<ComposerEditorRoute />); });
	return { onChange, onSend, sheetKey: route.params.sheetKey };
}

const field = () => renderer!.root.findByType("TextInput" as never);
const button = (label: string) => renderer!.root.findByProps({ accessibilityLabel: label });

it("opens on the composer's draft and writes every change back to it", () => {
	const { onChange } = open();
	expect(field().props).toMatchObject({ value: "Fix the flaky test", placeholder: "Ask Codex", multiline: true, autoFocus: true });
	act(() => field().props.onChangeText("Fix the flaky test, then rerun it"));
	expect(onChange).toHaveBeenCalledWith("Fix the flaky test, then rerun it");
	expect(field().props.value).toBe("Fix the flaky test, then rerun it");
});

it("closes before it asks the composer to send", () => {
	const { onSend } = open();
	act(() => button("Send message").props.onPress());
	expect(fixtures.back).toHaveBeenCalledOnce();
	expect(onSend).toHaveBeenCalledOnce();
	expect(fixtures.back.mock.invocationCallOrder[0]).toBeLessThan(onSend.mock.invocationCallOrder[0]);
});

it("can't send an empty draft", () => {
	open("");
	expect(button("Send message").props.disabled).toBe(true);
});

it("collapses from the bar on iOS, and leaves that to the system back on Android", () => {
	open();
	act(() => button("Close full-screen editor").props.onPress());
	expect(fixtures.back).toHaveBeenCalledOnce();
	act(() => renderer!.unmount());
	fixtures.platform.OS = "android";
	open();
	expect(renderer!.root.findAllByProps({ accessibilityLabel: "Close full-screen editor" })).toHaveLength(0);
	// Android's bar draws icons only: the send arrow, no "Send" text.
	expect(button("Send message").props.icon).toBeDefined();
	expect(button("Send message").props.children).toBeUndefined();
});

it("lets go of the hand-off when it closes", () => {
	const { sheetKey } = open();
	act(() => renderer!.unmount());
	renderer = undefined;
	expect(readChatSheet(sheetKey)).toBeUndefined();
});
