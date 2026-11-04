import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

// The terminal screen stays mounted while the connection drops on an app switch
// and comes back, possibly on another endpoint. The new socket must attach the
// PTY itself: the xterm WebView is still up and will not initialize again.
type Handlers = { onTerminalData?: (id: string, bytes: Uint8Array) => void };
const fake = vi.hoisted(() => ({ sockets: [] as Array<{
	host: string; handlers: Handlers;
	connect: ReturnType<typeof vi.fn>; disconnect: ReturnType<typeof vi.fn>;
	openTerminal: ReturnType<typeof vi.fn>; resize: ReturnType<typeof vi.fn>;
}> }));
vi.mock("../mux", () => ({
	MuxClient: class {
		constructor(cfg: { host: string }, handlers: Handlers) {
			const socket = { host: cfg.host, handlers, connect: vi.fn(), disconnect: vi.fn(), openTerminal: vi.fn(), resize: vi.fn() };
			fake.sockets.push(socket);
			return socket;
		}
	},
}));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));

import type { ServerConfig } from "../config";
import { TERMINAL_RESET, useTerminalMux } from "./useTerminalMux";

const lan: ServerConfig = { hostId: "a", host: "192.168.1.20", httpPort: "3011", muxPort: "3011", password: "pw" };
const tunnel: ServerConfig = { hostId: "a", host: "a.trycloudflare.com", httpPort: "443", muxPort: "443", secure: true, password: "pw" };
const bytes = (text: string) => new TextEncoder().encode(text);
const events = { onStatus: vi.fn(), onExited: vi.fn(), onError: vi.fn(), onResize: vi.fn() };
let xterm: { write: ReturnType<typeof vi.fn<(data: Uint8Array) => void>> };
let terminal!: ReturnType<typeof useTerminalMux>;
let renderer: ReactTestRenderer | undefined;
function Harness({ config }: { config: ServerConfig | null }) {
	terminal = useTerminalMux({ config, terminalHandleId: "pty-1", projectId: "proj", xtermRef: { current: xterm }, events });
	return null;
}
// What TerminalSessionScreen's onInitialized does once the WebView is up.
const webViewInitialized = () => act(() => { terminal.markXtermReady(); terminal.openTerminal(); });
const written = () => xterm.write.mock.calls.map(([data]) => new TextDecoder().decode(data));

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fake.sockets.length = 0;
	xterm = { write: vi.fn<(data: Uint8Array) => void>() };
});
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

it("reattaches the terminal on the new socket when the connection comes back on another endpoint", async () => {
	await act(async () => { renderer = create(<Harness config={lan} />); });
	webViewInitialized();
	terminal.lastDimsRef.current = { cols: 80, rows: 24 };
	const [first] = fake.sockets;
	expect(first.openTerminal).toHaveBeenCalledTimes(1);
	act(() => first.handlers.onTerminalData?.("pty-1", bytes("before")));

	await act(async () => renderer!.update(<Harness config={null} />)); // backgrounded
	expect(first.disconnect).toHaveBeenCalled();
	await act(async () => renderer!.update(<Harness config={tunnel} />)); // back, on the tunnel

	const second = fake.sockets[1];
	expect(second.host).toBe("a.trycloudflare.com");
	expect(second.connect).toHaveBeenCalled();
	expect(second.openTerminal).toHaveBeenCalledWith("pty-1", "proj");
	expect(second.resize).toHaveBeenCalledWith("pty-1", 80, 24, "proj");
	// The daemon repaints a fresh attach in full, scrollback included: the xterm
	// is reset first so nothing appears twice, and the repaint lands, not buffered.
	act(() => second.handlers.onTerminalData?.("pty-1", bytes("repaint")));
	expect(written()).toEqual(["before", new TextDecoder().decode(TERMINAL_RESET), "repaint"]);

	// A WebView re-firing onInitialized must not attach the PTY twice on one socket.
	webViewInitialized();
	expect(second.openTerminal).toHaveBeenCalledTimes(1);
});

it("opens the terminal once a connection exists when the xterm came up first", async () => {
	await act(async () => { renderer = create(<Harness config={null} />); });
	webViewInitialized();
	expect(fake.sockets).toEqual([]);
	await act(async () => renderer!.update(<Harness config={lan} />));
	expect(fake.sockets[0].openTerminal).toHaveBeenCalledTimes(1);
});

it("waits for the xterm before opening, and keeps early output in order", async () => {
	await act(async () => { renderer = create(<Harness config={lan} />); });
	const [socket] = fake.sockets;
	expect(socket.openTerminal).not.toHaveBeenCalled();
	act(() => socket.handlers.onTerminalData?.("pty-1", bytes("early")));
	expect(xterm.write).not.toHaveBeenCalled();
	webViewInitialized();
	expect(written()).toEqual(["early"]);
	expect(socket.openTerminal).toHaveBeenCalledTimes(1);
});
