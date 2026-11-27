import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Handlers = { onOutput: (bytes: Uint8Array) => void; onReset: () => void; onStatus: (status: string) => void };

const fixtures = vi.hoisted(() => ({
	handlers: undefined as Handlers | undefined,
	xterm: { write: vi.fn(), resize: vi.fn(), injectJavaScript: vi.fn() },
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Keyboard: { addListener: () => ({ remove: () => {} }), dismiss: vi.fn() },
		LayoutAnimation: { configureNext: vi.fn(), Types: { keyboard: "keyboard" } },
		Platform: { OS: "ios", Version: "26.0", select: (options: Record<string, unknown>) => options.ios ?? options.default },
		StyleSheet: { create: (styles: object) => styles, absoluteFill: {}, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("expo-router", () => ({ Stack: { Toolbar: Object.assign(() => null, { Menu: () => null, MenuAction: () => null, Label: () => null }) } }));
vi.mock("@fressh/react-native-xtermjs-webview", async () => {
	const React = await import("react");
	return { XtermJsWebView: (props: object) => React.createElement("XtermJsWebView", props) };
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, right: 0, bottom: 0, left: 0 }) }));
vi.mock("../ThemeProvider", () => {
	const theme = new Proxy({}, { get: () => "#000" });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme), useThemeState: () => ({ scheme: "dark", style: "default" }) };
});
vi.mock("../theme", () => ({ terminalTheme: () => ({}) }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn(), error: vi.fn(), select: vi.fn() } }));
vi.mock("../AppSymbol", () => ({ AppSymbol: () => null }));
vi.mock("../Spinner", () => ({ Spinner: () => null }));
vi.mock("../ui", () => ({ Button: () => null, EmptyState: () => null }));
vi.mock("../session/Composer", () => ({ Composer: () => null }));
vi.mock("../session/KeyRow", () => ({ KeyRow: () => null }));
vi.mock("../session/RouteToggle", () => ({ RouteToggle: () => null }));
vi.mock("../session/ScrollTopButton", () => ({ ScrollTopButton: () => null }));
vi.mock("../session/TerminalNotice", () => ({ TerminalNotice: () => null, TerminalNoticeHost: () => null }));
vi.mock("../voice/useVoiceInput", () => ({ useVoiceInput: () => ({ state: "idle", partial: "", mode: "hold", error: undefined }) }));
vi.mock("./terminal", () => ({
	connectCloudTerminal: ({ handlers }: { handlers: Handlers }) => {
		fixtures.handlers = handlers;
		return { close: vi.fn(), reconnect: vi.fn(), resize: vi.fn(), send: vi.fn() };
	},
}));

import { CloudTerminalView } from "./CloudTerminalView";

let renderer: ReactTestRenderer | undefined;
const onOutputShown = vi.fn();
const encode = (text: string) => new TextEncoder().encode(text);

beforeEach(async () => {
	onOutputShown.mockClear();
	fixtures.xterm.write.mockClear();
	await act(async () => {
		renderer = create(
			<CloudTerminalView client={{} as never} orgId="org" sessionId="s1" kind="agent" onOutputShown={onOutputShown} />,
			{ createNodeMock: (element) => (element.type === "XtermJsWebView" ? fixtures.xterm : null) },
		);
	});
});

afterEach(async () => {
	await act(async () => renderer?.unmount());
	renderer = undefined;
});

const xtermReady = async () => {
	await act(async () => (renderer!.root.findByType("XtermJsWebView" as never).props as { onInitialized: () => void }).onInitialized());
};
const output = async (text: string) => {
	await act(async () => fixtures.handlers!.onOutput(encode(text)));
};

describe("CloudTerminalView output shown", () => {
	// A reset discards what waited for the xterm; that output was never on screen.
	it("does not report output held for the xterm and then discarded by a reset", async () => {
		await output("> held");
		await act(async () => fixtures.handlers!.onReset());
		await xtermReady();
		expect(onOutputShown).not.toHaveBeenCalled();
		await output("> replayed");
		expect(onOutputShown).toHaveBeenCalledTimes(1);
	});

	it("reports held output once the xterm draws it", async () => {
		await output("> early");
		expect(onOutputShown).not.toHaveBeenCalled();
		await xtermReady();
		expect(onOutputShown).toHaveBeenCalledTimes(1);
	});

	it("does not count control-only bytes, and reports the first visible output once", async () => {
		await xtermReady();
		await output("\x1b[?25l\x1b[2J");
		expect(onOutputShown).not.toHaveBeenCalled();
		await output("> ready");
		await output("more");
		expect(onOutputShown).toHaveBeenCalledTimes(1);
		expect(fixtures.xterm.write).toHaveBeenCalled();
	});
});
