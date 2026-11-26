import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

// The terminal screen stays mounted while the connection drops on an app switch
// and comes back, possibly on another endpoint. The new socket must attach the
// PTY itself, at the phone's grid: the xterm WebView is still up and will not
// initialize or report its size again. Runs the real MuxClient over a socket
// that, like a real one, takes frames only once open, so what is asserted is
// what reached the wire.
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));

import type { ServerConfig } from "../config";
import { bytesToBase64 } from "../mux";
import { TERMINAL_RESET, useTerminalMux } from "./useTerminalMux";

class FakeWebSocket {
	static CONNECTING = 0;
	static OPEN = 1;
	static CLOSED = 3;
	static all: FakeWebSocket[] = [];
	readyState = FakeWebSocket.CONNECTING;
	sent: Record<string, unknown>[] = [];
	onopen?: () => void;
	onclose?: () => void;
	onmessage?: (event: { data: string }) => void;
	onerror?: () => void;
	constructor(readonly url: string) { FakeWebSocket.all.push(this); }
	send(data: string) {
		if (this.readyState !== FakeWebSocket.OPEN) throw new Error("InvalidStateError: still connecting");
		this.sent.push(JSON.parse(data) as Record<string, unknown>);
	}
	close() { this.readyState = FakeWebSocket.CLOSED; this.onclose?.(); }
	accept() { act(() => { this.readyState = FakeWebSocket.OPEN; this.onopen?.(); }); }
	output(text: string) {
		act(() => this.onmessage?.({ data: JSON.stringify({ ch: "terminal", id: "pty-1", type: "data", data: bytesToBase64(new TextEncoder().encode(text)) }) }));
	}
	opens() { return this.sent.filter((frame) => frame.ch === "terminal" && frame.type === "open"); }
}

const lan: ServerConfig = { hostId: "a", host: "192.168.1.20", httpPort: "3011", muxPort: "3011", password: "pw" };
const tunnel: ServerConfig = { hostId: "a", host: "a.trycloudflare.com", httpPort: "443", muxPort: "443", secure: true, password: "pw" };
const events = { onStatus: vi.fn(), onExited: vi.fn(), onError: vi.fn(), onResize: vi.fn() };
let xterm: { write: ReturnType<typeof vi.fn<(data: Uint8Array) => void>> };
let terminal!: ReturnType<typeof useTerminalMux>;
let renderer: ReactTestRenderer | undefined;
function Harness({ config }: { config: ServerConfig | null }) {
	terminal = useTerminalMux({ config, terminalHandleId: "pty-1", projectId: "proj", xtermRef: { current: xterm }, events });
	return null;
}
// What TerminalSessionScreen does: the FitAddon reports the phone's grid, then
// onInitialized marks the xterm ready and opens the terminal.
const webViewInitialized = (cols = 80, rows = 24) => act(() => {
	terminal.lastDimsRef.current = { cols, rows };
	terminal.markXtermReady();
	terminal.openTerminal();
});
const written = () => xterm.write.mock.calls.map(([data]) => new TextDecoder().decode(data));
const open = (cols: number, rows: number) => ({ ch: "terminal", id: "pty-1", type: "open", projectId: "proj", role: "secondary", cols, rows });

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.stubGlobal("WebSocket", FakeWebSocket);
	FakeWebSocket.all = [];
	xterm = { write: vi.fn<(data: Uint8Array) => void>() };
});
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

it("reattaches at the phone's grid when the connection comes back on another endpoint", async () => {
	await act(async () => { renderer = create(<Harness config={lan} />); });
	const [first] = FakeWebSocket.all;
	first.accept();
	webViewInitialized(80, 24);
	expect(first.opens()).toEqual([open(80, 24)]);
	first.output("before");

	await act(async () => renderer!.update(<Harness config={null} />)); // backgrounded
	expect(first.readyState).toBe(FakeWebSocket.CLOSED);
	await act(async () => renderer!.update(<Harness config={tunnel} />)); // back, on the tunnel

	const second = FakeWebSocket.all[1];
	expect(second.url).toBe("wss://a.trycloudflare.com:443/mux");
	// Still connecting: the open must wait for the socket and keep its size.
	expect(second.readyState).toBe(FakeWebSocket.CONNECTING);
	second.accept();
	expect(second.opens()).toEqual([open(80, 24)]);

	// The daemon repaints a fresh attach in full, scrollback included: the xterm
	// is reset first so nothing appears twice, and the repaint lands, not buffered.
	second.output("repaint");
	expect(written()).toEqual(["before", new TextDecoder().decode(TERMINAL_RESET), "repaint"]);

	// A WebView re-firing onInitialized must not attach the PTY twice on one socket.
	webViewInitialized(80, 24);
	expect(second.opens()).toHaveLength(1);
});

it("keeps a grid the phone reports while the new socket is still connecting", async () => {
	await act(async () => { renderer = create(<Harness config={lan} />); });
	FakeWebSocket.all[0].accept();
	webViewInitialized(80, 24);
	await act(async () => renderer!.update(<Harness config={tunnel} />));
	// What applyDims does when the FitAddon reports a new size after open.
	act(() => terminal.muxRef.current?.resize("pty-1", 100, 30, "proj"));
	const second = FakeWebSocket.all[1];
	second.accept();
	expect(second.opens()).toEqual([open(100, 30)]);
});

it("opens at the phone's grid once a connection exists when the xterm came up first", async () => {
	await act(async () => { renderer = create(<Harness config={null} />); });
	webViewInitialized(90, 28);
	expect(FakeWebSocket.all).toEqual([]);
	await act(async () => renderer!.update(<Harness config={lan} />));
	const [socket] = FakeWebSocket.all;
	socket.accept();
	expect(socket.opens()).toEqual([open(90, 28)]);
});

it("waits for the xterm before opening, and keeps early output in order", async () => {
	await act(async () => { renderer = create(<Harness config={lan} />); });
	const [socket] = FakeWebSocket.all;
	socket.accept();
	expect(socket.opens()).toEqual([]);
	socket.output("early");
	expect(xterm.write).not.toHaveBeenCalled();
	webViewInitialized(80, 24);
	expect(written()).toEqual(["early"]);
	expect(socket.opens()).toEqual([open(80, 24)]);
});
