import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { BrowserLiveHandlers, PreviewSource } from "./source";
import type { BrowserLiveCommand } from "../browserLive";

const fixture = vi.hoisted(() => ({ local: vi.fn(), config: null as object | null }));
vi.mock("react-native", () => ({ Platform: { OS: "ios" }, ActivityIndicator: "ActivityIndicator", Image: "Image", Pressable: "Pressable", Text: "Text", TextInput: "TextInput", View: "View", PanResponder: { create: () => ({ panHandlers: {} }) }, StyleSheet: { create: (styles: unknown) => styles } }));
vi.mock("../browserLive", () => ({ BrowserLiveClient: class { constructor(...args: unknown[]) { fixture.local(...args); } connect() {} close() {} } }));
vi.mock("../store", () => ({ useApp: () => ({ config: fixture.config }) }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: (factory: (theme: object) => unknown) => factory({}) }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
import { BrowserLivePane } from "./BrowserLivePane";

let screen: ReactTestRenderer;
let handlers: BrowserLiveHandlers;
let close = vi.fn<() => void>();
let send = vi.fn<(command: BrowserLiveCommand) => void>();
let source: PreviewSource;
beforeEach(() => {
	Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });
	fixture.local.mockClear(); fixture.config = null; close = vi.fn(); send = vi.fn();
	source = { kind: "cloud", scopeKey: "scope", isCurrent: () => true, createLive: (_id, value) => { handlers = value; return { connect: () => handlers.onStatus("open"), close, send }; } };
});
afterEach(() => { act(() => screen?.unmount()); });
const nodes = (type: string) => screen.root.findAll((node) => (node.type as unknown) === type);
const frame = { sequence: 1n, capturedAtMs: 1n, width: 640, height: 360, jpeg: Uint8Array.from([255,216,255]).buffer };

it("uses the supplied Cloud transport without a paired machine and offers safe navigation on a blank page", () => {
	act(() => { screen = create(<BrowserLivePane sessionID="worker" source={source} />); });
	expect(fixture.local).not.toHaveBeenCalled();
	const field = nodes("TextInput")[0];
	act(() => field.props.onChangeText("https://example.com"));
	act(() => nodes("TextInput")[0].props.onSubmitEditing());
	expect(send).toHaveBeenCalledWith({ type: "navigate", payload: { action: "open", url: "https://example.com" } });
});

it("clips the visible top-left crop and clears the prior image on source replacement", () => {
	act(() => { screen = create(<BrowserLivePane sessionID="worker" source={source} />); });
	act(() => { handlers.onFrame({ ...frame, crop: { width: 600, height: 300 } }); });
	expect(nodes("Image")).toHaveLength(1);
	expect(nodes("Image")[0].props.style).toMatchObject({ width: "106.66666666666667%", height: "120%" });
	const old = handlers;
	act(() => { screen.update(<BrowserLivePane sessionID="worker" source={{ ...source, scopeKey: "replacement" }} />); });
	expect(close).toHaveBeenCalled(); expect(nodes("Image")).toHaveLength(0);
	act(() => old.onFrame(frame)); expect(nodes("Image")).toHaveLength(0);
});

it("clears a failed attempt's image while reconnecting", () => {
	act(() => { screen = create(<BrowserLivePane sessionID="worker" source={source} />); });
	act(() => { handlers.onFrame(frame); handlers.onStatus("error", "Disconnected"); });
	act(() => nodes("Pressable").find((node) => node.props.accessibilityLabel === "Reconnect browser")!.props.onPress());
	expect(nodes("Image")).toHaveLength(0);
});

it("keeps the paired Local pane's browser chrome unchanged", () => {
	fixture.config = { host: "desktop.local", hostId: "host", httpPort: "3011", password: "secret" };
	act(() => { screen = create(<BrowserLivePane sessionID="worker" />); });
	expect(nodes("TextInput")).toHaveLength(0); expect(fixture.local).toHaveBeenCalledTimes(1);
});
