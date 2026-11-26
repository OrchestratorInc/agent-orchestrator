import { afterEach, describe, expect, it } from "vitest";
import {
	base64ToBytes,
	bytesToBase64,
	closeFrame,
	createTerminalMux,
	createTerminalMuxPool,
	dataFrame,
	muxUrlFromApiBase,
	openFrame,
	resizeFrame,
} from "./terminal-mux";

describe("terminal-mux framing", () => {
	it("round-trips arbitrary (non-UTF8) bytes through base64", () => {
		const bytes = new Uint8Array([0x00, 0x1b, 0x5b, 0xff, 0xfe, 0x7f, 0x41]);
		expect(base64ToBytes(bytesToBase64(bytes))).toEqual(bytes);
	});

	it("encodes an open frame the backend expects", () => {
		expect(JSON.parse(openFrame("sess-1", 80, 24))).toEqual({
			ch: "terminal",
			type: "open",
			id: "sess-1",
			cols: 80,
			rows: 24,
		});
	});

	it("base64-encodes input bytes in a data frame", () => {
		const frame = JSON.parse(dataFrame("sess-1", new TextEncoder().encode("ls\n")));
		expect(frame).toMatchObject({ ch: "terminal", type: "data", id: "sess-1" });
		expect(new TextDecoder().decode(base64ToBytes(frame.data))).toBe("ls\n");
	});

	it("carries cols/rows on resize and id on close", () => {
		expect(JSON.parse(resizeFrame("s", 120, 40))).toMatchObject({ type: "resize", cols: 120, rows: 40 });
		expect(JSON.parse(resizeFrame("s", 120, 40, true))).toMatchObject({
			type: "resize",
			cols: 120,
			rows: 40,
			force: true,
		});
		expect(JSON.parse(closeFrame("s"))).toEqual({ ch: "terminal", type: "close", id: "s" });
	});

	it("derives the ws mux url from the http api base (root path, not /api/v1)", () => {
		expect(muxUrlFromApiBase("http://127.0.0.1:4317")).toBe("ws://127.0.0.1:4317/mux");
		expect(muxUrlFromApiBase("https://host:8443/")).toBe("wss://host:8443/mux");
	});

	it("uses the current origin for a relative dev API base", () => {
		expect(muxUrlFromApiBase("")).toBe("ws://localhost:3000/mux");
	});
});

// Minimal fake socket so we can assert client behaviour without a live daemon.
class FakeSocket {
	static OPEN = 1;
	static instances: FakeSocket[] = [];
	readyState = 0;
	sent: string[] = [];
	closed = false;
	private listeners: Record<string, ((ev: unknown) => void)[]> = {};
	constructor(public url: string) {
		FakeSocket.instances.push(this);
	}
	addEventListener(type: string, cb: (ev: unknown) => void) {
		(this.listeners[type] ??= []).push(cb);
	}
	send(frame: string) {
		this.sent.push(frame);
	}
	close() {
		this.closed = true;
	}
	emitOpen() {
		this.readyState = FakeSocket.OPEN;
		this.listeners.open?.forEach((cb) => cb({}));
	}
	emitMessage(data: string) {
		this.listeners.message?.forEach((cb) => cb({ data }));
	}
	emitClose() {
		this.listeners.close?.forEach((cb) => cb({}));
	}
	emitError() {
		this.listeners.error?.forEach((cb) => cb({}));
	}
}

function decodeInputMessages(messages: string[]): Uint8Array {
	const binary = messages.map((message) => atob(JSON.parse(message).data)).join("");
	return Uint8Array.from(binary, (char) => char.charCodeAt(0));
}

describe("createTerminalMux client", () => {
	afterEach(() => {
		FakeSocket.instances = [];
	});

	it("sends an 800 KiB bracketed paste below the daemon message limit and preserves following input", () => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		const encoder = new TextEncoder();
		const paste = `\x1b[200~${"x".repeat(800 * 1024)}\x1b[201~`;
		socket.emitOpen();
		try {
			mux.sendInput("s", paste);
			mux.sendInput("s", "\r");
			for (const message of socket.sent) {
				expect(encoder.encode(message).byteLength).toBeLessThan(1024 * 1024);
				expect(JSON.parse(message)).toMatchObject({ ch: "terminal", type: "data", id: "s" });
			}
			expect(decodeInputMessages(socket.sent)).toEqual(Uint8Array.from(encoder.encode(`${paste}\r`)));
		} finally {
			mux.dispose();
		}
	});

	it.each(["é", "漢", "😀"])("preserves %s when its UTF-8 bytes cross a message boundary", (character) => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		const input = `${"x".repeat(32 * 1024 - 1)}${character}\r`;
		socket.emitOpen();
		try {
			mux.sendInput("s", input);
			expect(decodeInputMessages(socket.sent)).toEqual(Uint8Array.from(new TextEncoder().encode(input)));
		} finally {
			mux.dispose();
		}
	});

	it("preserves queued paste and following input between open and resize", () => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		const paste = `\x1b[200~${"queued\r".repeat(10_000)}\x1b[201~`;
		try {
			mux.open("s", 80, 24);
			mux.sendInput("s", paste);
			mux.sendInput("s", "\r");
			mux.resize("s", 120, 40);
			expect(socket.sent).toEqual([]);
			socket.emitOpen();
			expect(JSON.parse(socket.sent[0])).toEqual({ ch: "terminal", type: "open", id: "s", cols: 80, rows: 24 });
			expect(decodeInputMessages(socket.sent.slice(1, -1))).toEqual(
				Uint8Array.from(new TextEncoder().encode(`${paste}\r`)),
			);
			expect(JSON.parse(socket.sent.at(-1)!)).toEqual({
				ch: "terminal",
				type: "resize",
				id: "s",
				cols: 120,
				rows: 40,
			});
		} finally {
			mux.dispose();
		}
	});

	it("preserves small and empty input frames and ignores input after disposal", () => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		socket.emitOpen();
		mux.sendInput("s", "ls\r");
		mux.sendInput("s", "");
		mux.dispose();
		mux.sendInput("s", "x".repeat(800 * 1024));
		expect(socket.sent.map((message) => JSON.parse(message))).toEqual([
			{ ch: "terminal", type: "data", id: "s", data: "bHMN" },
			{ ch: "terminal", type: "data", id: "s", data: "" },
		]);
	});

	it("queues frames until open, then flushes them in order", () => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		mux.open("s", 80, 24);
		expect(socket.sent).toHaveLength(0); // not open yet → queued
		socket.emitOpen();
		expect(JSON.parse(socket.sent[0])).toMatchObject({ type: "open", id: "s" });
	});

	it("routes server data frames to the matching id listener as bytes", () => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		socket.emitOpen();
		const chunks: string[] = [];
		mux.onData("s", (bytes) => chunks.push(new TextDecoder().decode(bytes)));
		socket.emitMessage(
			JSON.stringify({ ch: "terminal", id: "s", type: "data", data: bytesToBase64(new TextEncoder().encode("hi")) }),
		);
		socket.emitMessage(
			JSON.stringify({
				ch: "terminal",
				id: "other",
				type: "data",
				data: bytesToBase64(new TextEncoder().encode("nope")),
			}),
		);
		expect(chunks).toEqual(["hi"]);
	});

	it("fires the exit listener on an exited frame", () => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		socket.emitOpen();
		let exited = false;
		mux.onExit("s", () => {
			exited = true;
		});
		socket.emitMessage(JSON.stringify({ ch: "terminal", id: "s", type: "exited" }));
		expect(exited).toBe(true);
	});

	it("fires the opened listener on the server's open ack", () => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		socket.emitOpen();
		let opened = false;
		mux.onOpened("s", () => {
			opened = true;
		});
		socket.emitMessage(JSON.stringify({ ch: "terminal", id: "s", type: "opened" }));
		expect(opened).toBe(true);
	});

	it("routes a pane error frame to that pane's error listener only", () => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		socket.emitOpen();
		const errors: string[] = [];
		const otherErrors: string[] = [];
		mux.onError("s", (message) => errors.push(message));
		mux.onError("other", (message) => otherErrors.push(message));
		socket.emitMessage(JSON.stringify({ ch: "terminal", id: "s", type: "error", error: "no such pane" }));
		expect(errors).toEqual(["no such pane"]);
		expect(otherErrors).toEqual([]);
	});

	it("broadcasts an id-less error frame to every error listener", () => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		socket.emitOpen();
		const seen: string[] = [];
		mux.onError("a", (message) => seen.push(`a:${message}`));
		mux.onError("b", (message) => seen.push(`b:${message}`));
		socket.emitMessage(JSON.stringify({ ch: "terminal", type: "error", error: "missing terminal id" }));
		expect(seen.sort()).toEqual(["a:missing terminal id", "b:missing terminal id"]);
	});

	it("reports connection transitions, deduping error+close into one closed", () => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		const states: string[] = [];
		mux.onConnectionChange((state) => states.push(state));
		socket.emitOpen();
		socket.emitError();
		socket.emitClose();
		expect(states).toEqual(["open", "closed"]);
	});

	it("does not report a connection change for its own dispose", () => {
		const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
		const socket = FakeSocket.instances.at(-1)!;
		socket.emitOpen();
		const states: string[] = [];
		mux.onConnectionChange((state) => states.push(state));
		mux.dispose();
		socket.emitClose(); // browser fires close after socket.close()
		expect(states).toEqual([]);
	});
});

describe("createTerminalMuxPool", () => {
	afterEach(() => {
		FakeSocket.instances = [];
	});

	it("leases one shared socket to independent terminal attachments", () => {
		const pool = createTerminalMuxPool(() =>
			createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket),
		);
		const first = pool.acquire();
		const second = pool.acquire();
		expect(FakeSocket.instances).toHaveLength(1);
		const socket = FakeSocket.instances[0];
		socket.emitOpen();

		first.open("a", 80, 24);
		second.open("b", 120, 40);
		expect(socket.sent.map((frame) => JSON.parse(frame).id)).toEqual(["a", "b"]);

		first.dispose();
		expect(socket.closed).toBe(false);
		second.dispose();
		expect(socket.closed).toBe(true);
	});

	it("routes listeners per lease and creates one replacement socket after a shared disconnect", () => {
		const pool = createTerminalMuxPool(() =>
			createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket),
		);
		const first = pool.acquire();
		const second = pool.acquire();
		const firstStates: string[] = [];
		const secondStates: string[] = [];
		first.onConnectionChange((state) => firstStates.push(state));
		second.onConnectionChange((state) => secondStates.push(state));
		FakeSocket.instances[0].emitOpen();
		FakeSocket.instances[0].emitClose();
		expect(firstStates).toEqual(["open", "closed"]);
		expect(secondStates).toEqual(["open", "closed"]);

		first.dispose();
		second.dispose();
		const replacementA = pool.acquire();
		const replacementB = pool.acquire();
		expect(FakeSocket.instances).toHaveLength(2);
		replacementA.dispose();
		replacementB.dispose();
	});

	it("releases every underlying listener on lease cleanup", () => {
		const pool = createTerminalMuxPool(() =>
			createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket),
		);
		const lease = pool.acquire();
		const data: string[] = [];
		lease.onData("a", (bytes) => data.push(new TextDecoder().decode(bytes)));
		const socket = FakeSocket.instances[0];
		socket.emitOpen();
		lease.dispose();
		socket.emitMessage(
			JSON.stringify({
				ch: "terminal",
				data: bytesToBase64(new TextEncoder().encode("late")),
				id: "a",
				type: "data",
			}),
		);
		expect(data).toEqual([]);
	});
});
