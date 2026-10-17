import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// mux.ts imports ./config, which reaches React Native's Flow-typed sources
// through these two. Same preamble as lib/chat/eventCursor.test.ts.
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
}));

import type { ServerConfig } from "./config";
import { base64ToBytes, bytesToBase64, MuxClient } from "./mux";

/**
 * Every literal below was produced by Go's `base64.StdEncoding`, the daemon's
 * only encode site for terminal payloads (backend/internal/terminal/manager.go:449),
 * rather than by bytesToBase64. Pinning the wire contract to the encoder we ship
 * would let both sides drift together without a test noticing.
 */
const WIRE: { name: string; bytes: number[]; b64: string }[] = [
	// Reachable today: mux.ts decodes String(msg.data ?? "").
	{ name: "an empty frame", bytes: [], b64: "" },
	{ name: "a one-byte tail (==)", bytes: [0x41], b64: "QQ==" },
	{ name: "a two-byte tail (=)", bytes: [0x41, 0x42], b64: "QUI=" },
	{ name: "an exact three-byte group", bytes: [0x41, 0x42, 0x43], b64: "QUJD" },
	{
		// A real PTY chunk: NUL, CSI 31m, and bytes above 0x7f that are not valid
		// UTF-8 on their own - the reason the wire is base64 at all.
		name: "an ANSI chunk carrying high bytes",
		bytes: [0x00, 0x1b, 0x5b, 0x33, 0x31, 0x6d, 0xff, 0xfe, 0x7f, 0x80, 0x0a],
		b64: "ABtbMzFt//5/gAo=",
	},
];

describe("base64ToBytes", () => {
	for (const { name, bytes, b64 } of WIRE) {
		it(`decodes ${name} exactly as the daemon encoded it`, () => {
			expect(Array.from(base64ToBytes(b64))).toEqual(bytes);
		});
	}

	it("preserves every byte value 0x00-0xff", () => {
		// Go: base64.StdEncoding.EncodeToString(0..255). A decoder that masked or
		// sign-extended the high half would show up here and nowhere else.
		const all256 =
			"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0+P0BB" +
			"QkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWltcXV5fYGFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6e3x9fn+AgYKD" +
			"hIWGh4iJiouMjY6PkJGSk5SVlpeYmZqbnJ2en6ChoqOkpaanqKmqq6ytrq+wsbKztLW2t7i5uru8vb6/wMHCw8TF" +
			"xsfIycrLzM3Oz9DR0tPU1dbX2Nna29zd3t/g4eLj5OXm5+jp6uvs7e7v8PHy8/T19vf4+fr7/P3+/w==";
		const got = base64ToBytes(all256);
		expect(got).toHaveLength(256);
		expect(Array.from(got)).toEqual(Array.from({ length: 256 }, (_, i) => i));
	});

	it("decodes a full 32 KiB frame", () => {
		// The daemon reads PTY output into a 32 KiB buffer (attachment.go:196), so
		// this is the largest single frame the phone has to decode.
		const bytes = Uint8Array.from({ length: 32 * 1024 }, (_, i) => i & 0xff);
		const b64 = bytesToBase64(bytes);
		expect(b64).toHaveLength(43692);
		expect(b64.slice(0, 32)).toBe("AAECAwQFBgcICQoLDA0ODxAREhMUFRYX");
		expect(b64.slice(-32)).toBe("6err7O3u7/Dx8vP09fb3+Pn6+/z9/v8=");
		expect(base64ToBytes(b64)).toEqual(bytes);
	});

	it("yields an empty frame for a payload it cannot decode, rather than throwing", () => {
		// atob rejects input the previous decoder silently filtered out. `handle` is
		// called outside the JSON.parse guard in ws.onmessage, so a throw would escape
		// the socket callback; drop the frame instead, as that guard already does.
		expect(() => base64ToBytes("not base64!")).not.toThrow();
		expect(base64ToBytes("not base64!")).toEqual(new Uint8Array(0));
		expect(base64ToBytes("QQ=")).toEqual(new Uint8Array(0)); // wrong padding
	});

	it("still decodes a payload carrying whitespace, as the prefilter it replaced did", () => {
		// atob strips ASCII whitespace itself, so dropping the prefilter loses nothing.
		// vitest runs on Node, so every atob case here was re-checked on the
		// simulator's Hermes (250829098.0.17) and behaves identically.
		expect(base64ToBytes("QU\nJD")).toEqual(new Uint8Array([0x41, 0x42, 0x43]));
		expect(base64ToBytes("QU JD")).toEqual(new Uint8Array([0x41, 0x42, 0x43]));
	});
});

describe("bytesToBase64", () => {
	for (const { name, bytes, b64 } of WIRE) {
		it(`encodes ${name} the way Go's StdEncoding decodes it`, () => {
			expect(bytesToBase64(Uint8Array.from(bytes))).toBe(b64);
		});
	}

	it("round-trips arbitrary non-UTF8 bytes", () => {
		const bytes = new Uint8Array([0x00, 0x1b, 0x5b, 0xff, 0xfe, 0x7f, 0x41]);
		expect(base64ToBytes(bytesToBase64(bytes))).toEqual(bytes);
	});
});

/**
 * Client frames, as the literal JSON the daemon decodes. The daemon routes on
 * `ch` and then on `type`, and its subscribe handler returns without a word
 * unless `type` is "subscribe" (backend/internal/terminal/manager.go,
 * handleSubscribe), so a subscribe frame without `type` is dropped silently.
 * TestClientMsgDecodesMobileFrames (terminal/protocol_test.go) decodes the same
 * literals with the daemon's own struct.
 */
const SUBSCRIBE = { ch: "subscribe", type: "subscribe", topics: ["sessions", "notifications"] };

/**
 * Frames the daemon pushes on the "sessions" channel: one change_log row per frame,
 * carried as `session` (backend/internal/terminal/protocol.go, sessionUpdate), never
 * as a `sessions` list. The literals are the ones the daemon's own golden tests
 * produce (TestServerMsgSessionFrameWireShape and TestServerMsgProjectEventFrameWireShape
 * in terminal/protocol_test.go). The daemon omits `sessionId` for a project-level change.
 */
const DAEMON_SESSION_FRAME =
	'{"ch":"sessions","type":"snapshot","session":{"seq":7,"projectId":"p1","sessionId":"s1","eventType":"session_updated"}}';
const DAEMON_PROJECT_FRAME =
	'{"ch":"sessions","type":"snapshot","session":{"seq":8,"projectId":"p1","eventType":"pr_updated"}}';

const config: ServerConfig = { host: "127.0.0.1", httpPort: "3011", muxPort: "14801", password: "" };

class FakeSocket {
	static OPEN = 1;
	static instances: FakeSocket[] = [];
	readyState = 0;
	sent: string[] = [];
	onopen: (() => void) | null = null;
	onclose: (() => void) | null = null;
	onmessage: ((ev: { data: unknown }) => void) | null = null;
	constructor(public url: string) {
		FakeSocket.instances.push(this);
	}
	send(frame: string) {
		this.sent.push(frame);
	}
	close() {}
	open() {
		this.readyState = FakeSocket.OPEN;
		this.onopen?.();
	}
	receive(frame: string) {
		this.onmessage?.({ data: frame });
	}
	frames(): unknown[] {
		return this.sent.map((s) => JSON.parse(s));
	}
}

describe("MuxClient", () => {
	beforeEach(() => {
		FakeSocket.instances = [];
		vi.useFakeTimers();
		vi.stubGlobal("WebSocket", FakeSocket);
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.unstubAllGlobals();
	});

	function connected(handlers: ConstructorParameters<typeof MuxClient>[1] = {}) {
		const mux = new MuxClient(config, handlers);
		mux.connect();
		const socket = FakeSocket.instances[FakeSocket.instances.length - 1];
		socket.open();
		return { mux, socket };
	}

	describe("frames it sends", () => {
		it("subscribes with the typed frame the daemon accepts", () => {
			const { mux, socket } = connected();
			mux.subscribeSessions();
			expect(socket.frames()).toEqual([SUBSCRIBE]);
		});

		it("subscribes again with the same frame after a reconnect", () => {
			const { mux, socket } = connected();
			mux.subscribeSessions();
			socket.onclose?.();
			vi.advanceTimersByTime(1000); // first reconnect backoff
			const next = FakeSocket.instances[FakeSocket.instances.length - 1];
			expect(next).not.toBe(socket);
			next.open();
			expect(next.frames()).toEqual([SUBSCRIBE]);
		});

		it("gives every frame a channel and a type", () => {
			const { mux, socket } = connected();
			mux.subscribeSessions();
			mux.openTerminal("s1", "p1");
			mux.sendInput("s1", "ls\n", "p1");
			mux.resize("s1", 80, 24, "p1");
			vi.advanceTimersByTime(20_000); // the keep-alive ping
			mux.closeTerminal("s1", "p1");
			expect(socket.frames()).toEqual([
				SUBSCRIBE,
				{ ch: "terminal", id: "s1", type: "open", projectId: "p1", role: "secondary" },
				{ ch: "terminal", id: "s1", type: "data", data: "bHMK", projectId: "p1" },
				{ ch: "terminal", id: "s1", type: "resize", cols: 80, rows: 24, projectId: "p1" },
				{ ch: "system", type: "ping" },
				{ ch: "terminal", id: "s1", type: "close", projectId: "p1" },
			]);
		});
	});

	describe("session frames it receives", () => {
		function subscribed() {
			const onSessionUpdate = vi.fn();
			const { mux, socket } = connected({ onSessionUpdate });
			mux.subscribeSessions();
			return { onSessionUpdate, socket };
		}

		it("reports the change the daemon sends for a session", () => {
			const { onSessionUpdate, socket } = subscribed();
			socket.receive(DAEMON_SESSION_FRAME);
			expect(onSessionUpdate.mock.calls).toEqual([
				[{ seq: 7, projectId: "p1", sessionId: "s1", eventType: "session_updated" }],
			]);
		});

		it("reports a project-level change, which has no session id", () => {
			const { onSessionUpdate, socket } = subscribed();
			socket.receive(DAEMON_PROJECT_FRAME);
			expect(onSessionUpdate.mock.calls).toEqual([[{ seq: 8, projectId: "p1", eventType: "pr_updated" }]]);
			expect(onSessionUpdate.mock.calls[0][0].sessionId).toBeUndefined();
		});

		it("reports one update per frame, in order", () => {
			const { onSessionUpdate, socket } = subscribed();
			socket.receive(DAEMON_SESSION_FRAME);
			socket.receive(DAEMON_PROJECT_FRAME);
			expect(onSessionUpdate.mock.calls.map(([u]) => u.seq)).toEqual([7, 8]);
		});

		it("drops a sessions frame it cannot read", () => {
			const { onSessionUpdate, socket } = subscribed();
			socket.receive('{"ch":"sessions","type":"snapshot"}');
			socket.receive('{"ch":"sessions","type":"snapshot","session":{"projectId":"p1","eventType":"session_updated"}}');
			socket.receive('{"ch":"sessions","type":"snapshot","session":{"seq":"7","projectId":"p1","eventType":"session_updated"}}');
			socket.receive('{"ch":"sessions","type":"snapshot","session":{"seq":7,"eventType":"session_updated"}}');
			socket.receive('{"ch":"sessions","type":"snapshot","session":{"seq":7,"projectId":"p1"}}');
			expect(onSessionUpdate).not.toHaveBeenCalled();
		});
	});
});
