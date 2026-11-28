import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { RECONNECTING_ACTION_COPY } from "./connectionError";
import type { ReviewReplyTarget } from "./ReviewCommentsSection";

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, style, ...props }: { children?: unknown; style?: unknown }) =>
		React.createElement(name, { ...props, style: typeof style === "function" ? style({ pressed: false }) : style }, children as React.ReactNode);
	return { Pressable: host("Pressable"), Text: host("Text"), TextInput: host("TextInput"), View: host("View"), StyleSheet: { create: (styles: object) => styles } };
});
vi.mock("react-native-keyboard-controller", () => ({ KeyboardStickyView: ({ children }: { children: unknown }) => children }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./Spinner", () => ({ Spinner: "Spinner" }));
vi.mock("./haptics", () => ({ haptics: { success: vi.fn() } }));
vi.mock("./pressFeedback", () => ({ usePressFeedback: () => ({ pressed: {}, android_ripple: undefined }) }));
vi.mock("./textSelection", () => ({ textSelectionColors: () => ({}) }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#000000" });
	return { useTheme: () => theme, useThemedStyles: (make: (t: object) => object) => make(theme) };
});

import { ReviewReplyDock } from "./ReviewReplyDock";

const target = {
	comment: { key: "c1", kind: "inline", author: "maya-chen", body: "Cap the name at 80 characters.", threadId: "PRRT_1" },
	block: {},
} as unknown as ReviewReplyTarget;

let renderer: ReactTestRenderer | undefined;
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

const alerts = () => renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.accessibilityRole === "alert").map((node) => node.props.children);
async function typeAndSend() {
	await act(async () => { renderer!.root.find((node) => (node.type as unknown) === "TextInput").props.onChangeText("Done in 4e1d2c0."); });
	await act(async () => { await renderer!.root.find((node) => (node.type as unknown) === "Pressable" && node.props.accessibilityLabel === "Post reply").props.onPress(); });
}

// A reply sent while the machine reconnects keeps its text and says why; the
// note goes once the machine answers rather than claiming a reconnect that is over.
it("says it is reconnecting, and drops that once connected again", async () => {
	const onSend = vi.fn(async () => { throw new Error(RECONNECTING_ACTION_COPY); });
	await act(async () => { renderer = create(<ReviewReplyDock target={target} bottomInset={0} connected={false} onSend={onSend} onClose={vi.fn()} />); });
	await typeAndSend();
	expect(alerts()).toEqual([RECONNECTING_ACTION_COPY]);
	expect(renderer!.root.find((node) => (node.type as unknown) === "TextInput").props.value).toBe("Done in 4e1d2c0.");

	await act(async () => { renderer!.update(<ReviewReplyDock target={target} bottomInset={0} connected onSend={onSend} onClose={vi.fn()} />); });
	expect(alerts()).toEqual([]);
});

// What the machine or the network answered reads as the app's copy, not a raw message.
it("shows a failed post through the app's error copy", async () => {
	const onSend = vi.fn(async () => { throw new TypeError("Network request failed"); });
	await act(async () => { renderer = create(<ReviewReplyDock target={target} bottomInset={0} connected onSend={onSend} onClose={vi.fn()} />); });
	await typeAndSend();
	expect(alerts()).toHaveLength(1);
	expect(alerts()[0]).not.toBe("Network request failed");
});
