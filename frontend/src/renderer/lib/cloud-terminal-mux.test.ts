import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createCloudTerminalMux } from "./cloud-terminal-mux";
import { subscribeCloudNotificationHints } from "./cloud-notification-hints";
import { useUiStore } from "../stores/ui-store";

// Minimal fake WebSocket: records its URL and every frame it sends, lets the
// test deliver frames, and reports OPEN so sendJSON works.
class FakeWebSocket {
	static OPEN = 1;
	readyState = FakeWebSocket.OPEN;
	url: string;
	sent: string[] = [];
	private listeners = new Map<string, Set<(event: unknown) => void>>();
	constructor(url: string) {
		this.url = url;
		FakeWebSocket.instances.push(this);
	}
	static instances: FakeWebSocket[] = [];
	addEventListener(type: string, fn: (event: unknown) => void) {
		const set = this.listeners.get(type) ?? new Set();
		set.add(fn);
		this.listeners.set(type, set);
	}
	send(data: string) {
		this.sent.push(data);
	}
	close() {}
	emit(type: string, event: unknown) {
		this.listeners.get(type)?.forEach((fn) => fn(event));
	}
	deliver(message: object) {
		this.emit("message", { data: JSON.stringify(message) });
	}
}

const b64 = (s: string) => Buffer.from(s).toString("base64");
const paramOf = (ws: FakeWebSocket, key: string) =>
	new URL(ws.url.replace(/^ws/, "http")).searchParams.get(key);
const afterOf = (ws: FakeWebSocket) => paramOf(ws, "after");
const sentJSON = (ws: FakeWebSocket) => ws.sent.map((frame) => JSON.parse(frame));

function makeMux(cursor?: { value: number }) {
	return createCloudTerminalMux({
		wsBaseUrl: "wss://cp.example.com/api/cloud/v1",
		kind: "agent",
		mintTicket: async () => "ticket-1",
		cursor,
		WebSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
	});
}

// Let the async connect() mint its ticket and dial the socket.
async function settle() {
	await Promise.resolve();
	await Promise.resolve();
}

describe("createCloudTerminalMux direct open", () => {
	it("opens its socket directly at construction with no agent-ready wait", async () => {
		FakeWebSocket.instances = [];
		const mux = makeMux();
		// The mux dials as soon as the ticket mint resolves — there is no SSE to
		// wait on. Nothing is created synchronously (the mint is awaited).
		expect(FakeWebSocket.instances).toHaveLength(0);
		await settle();
		expect(FakeWebSocket.instances).toHaveLength(1);
		const ws = FakeWebSocket.instances[0];
		expect(paramOf(ws, "kind")).toBe("agent");
		expect(afterOf(ws)).toBe("0");
		mux.dispose();
	});

	it("stays unopened on `starting` and opens the connection on `ready`", async () => {
		FakeWebSocket.instances = [];
		const mux = makeMux();
		await settle();
		const ws = FakeWebSocket.instances[0];
		let opened = 0;
		mux.onOpened("agent", () => {
			opened += 1;
		});
		// `starting` is the CP holding the pane while the terminal comes up: it
		// carries no readiness, so the hook stays in its "connecting" state.
		ws.deliver({ type: "starting", sequence: 0 });
		expect(opened).toBe(0);
		// `ready` is the server ack — the hook transitions to "attached".
		ws.deliver({ type: "ready", sequence: 0 });
		expect(opened).toBe(1);
		mux.dispose();
	});

	it("does not claim a size for a hidden 0×0 open, and sends the real size when visible", async () => {
		FakeWebSocket.instances = [];
		const mux = makeMux();
		await settle();
		const ws = FakeWebSocket.instances[0];
		ws.sent.length = 0;
		// A hidden/parked pane attaches at 0×0. The CP rejects a 0-dimension resize
		// and closes the socket, so the mux must send nothing.
		mux.open("agent", 0, 0);
		expect(ws.sent).toHaveLength(0);
		// Becoming visible (open for a visible pane, or resize on activation) sends
		// the real, non-zero grid so the PTY resizes and the TUI redraws at width.
		vi.useFakeTimers();
		try {
			mux.resize("agent", 100, 30);
			vi.advanceTimersByTime(150);
			expect(sentJSON(ws)).toEqual([{ type: "resize", columns: 100, rows: 30 }]);
		} finally {
			vi.useRealTimers();
		}
		mux.dispose();
	});

	it("ignores an implausibly small grid so a mis-measured fit never reaches the PTY", async () => {
		FakeWebSocket.instances = [];
		const mux = makeMux();
		await settle();
		const ws = FakeWebSocket.instances[0];
		ws.sent.length = 0;
		// A fit measured before font metrics / the pane box settle can propose a
		// couple columns; forwarding that wraps the agent TUI to ~2 chars, and it
		// would be cached in pendingResize and replayed on every reconnect. It must
		// be dropped, never sent and never cached.
		vi.useFakeTimers();
		try {
			mux.resize("agent", 2, 2);
			mux.resize("agent", 8, 30);
			vi.advanceTimersByTime(150);
			expect(ws.sent).toHaveLength(0);
			// A settled, plausible grid sends normally.
			mux.resize("agent", 120, 40);
			vi.advanceTimersByTime(150);
			expect(sentJSON(ws)).toEqual([{ type: "resize", columns: 120, rows: 40 }]);
		} finally {
			vi.useRealTimers();
		}
		mux.dispose();
	});

	it("does not cache an implausibly small pre-open resize for replay on connect", async () => {
		FakeWebSocket.instances = [];
		const mux = makeMux();
		// Resize BEFORE the socket is dialed — the pending-resize caching path that is
		// flushed on (re)connect. A mis-fit here must not be cached, so it is never
		// replayed when the socket opens; a plausible one is.
		mux.resize("agent", 2, 2);
		mux.resize("agent", 110, 44);
		await settle();
		const ws = FakeWebSocket.instances[0];
		ws.emit("open", {}); // drive the on-open replay of the cached resize
		expect(sentJSON(ws)).toEqual([{ type: "resize", columns: 110, rows: 44 }]);
		mux.dispose();
	});
});

describe("createCloudTerminalMux resize coalescing", () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	async function openMux(kind: "agent" | "workspace" = "agent") {
		FakeWebSocket.instances = [];
		const mux = createCloudTerminalMux({
			wsBaseUrl: "wss://cp.example.com/api/cloud/v1",
			kind,
			mintTicket: async () => "ticket-1",
			WebSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
		});
		await settle();
		const ws = FakeWebSocket.instances[0];
		ws.emit("open", {});
		ws.sent.length = 0;
		return { mux, ws };
	}

	const resizes = (ws: FakeWebSocket) => sentJSON(ws).filter((frame) => frame.type === "resize");

	it("collapses a resize burst into one trailing send of the final size", async () => {
		const { mux, ws } = await openMux();
		// A dragged side panel: a fit every few milliseconds.
		for (let cols = 80; cols <= 120; cols++) {
			mux.resize("agent", cols, 40);
			vi.advanceTimersByTime(10);
		}
		expect(resizes(ws)).toEqual([]);
		vi.advanceTimersByTime(150);
		expect(resizes(ws)).toEqual([{ type: "resize", columns: 120, rows: 40 }]);
		mux.dispose();
	});

	it("drops a resize equal to the size already sent", async () => {
		const { mux, ws } = await openMux("workspace");
		mux.resize("workspace", 100, 30);
		vi.advanceTimersByTime(150);
		mux.resize("workspace", 100, 30);
		vi.advanceTimersByTime(150);
		expect(resizes(ws)).toEqual([{ type: "resize", columns: 100, rows: 30 }]);
		mux.dispose();
	});

	it("asks for a repaint when a burst ends at the size already sent but the screen reflowed", async () => {
		const { mux, ws } = await openMux();
		mux.resize("agent", 148, 43);
		vi.advanceTimersByTime(150);
		// A side panel toggled open and closed quickly: the pane fits at the
		// narrower width and back, and the program never sees a size change.
		for (let toggle = 0; toggle < 24; toggle++) {
			mux.resize("agent", toggle % 2 === 0 ? 81 : 148, 43);
			vi.advanceTimersByTime(100);
		}
		mux.resize("agent", 148, 43);
		vi.advanceTimersByTime(150);
		expect(resizes(ws)).toEqual([
			{ type: "resize", columns: 148, rows: 43 },
			{ type: "resize", columns: 148, rows: 43, repaint: true },
		]);
		// Nothing reflowed since, so a later fit at the same size sends nothing.
		mux.resize("agent", 148, 43);
		vi.advanceTimersByTime(150);
		expect(resizes(ws)).toHaveLength(2);
		mux.dispose();
	});

	it("sends the size over the open terminal socket, the live path input takes", async () => {
		const { mux, ws } = await openMux();
		mux.resize("agent", 132, 43);
		mux.sendInput("agent", "x");
		// Input is not debounced; the resize follows on the same socket.
		expect(sentJSON(ws)).toEqual([{ type: "input", data: "x" }]);
		vi.advanceTimersByTime(150);
		expect(sentJSON(ws)).toEqual([
			{ type: "input", data: "x" },
			{ type: "resize", columns: 132, rows: 43 },
		]);
		mux.dispose();
	});

	it("delivers the final size once connected even if the debounce fired while disconnected", async () => {
		const { mux, ws } = await openMux();
		ws.readyState = 3; // CLOSED
		mux.resize("agent", 110, 35);
		mux.resize("agent", 111, 36);
		vi.advanceTimersByTime(150);
		expect(resizes(ws)).toEqual([]);
		ws.readyState = FakeWebSocket.OPEN;
		ws.emit("open", {});
		expect(resizes(ws)).toEqual([{ type: "resize", columns: 111, rows: 36 }]);
		vi.advanceTimersByTime(150);
		expect(resizes(ws)).toHaveLength(1);
		mux.dispose();
	});

	it("resends the current size on a new socket even when it did not change", async () => {
		const { mux, ws } = await openMux();
		mux.resize("agent", 100, 30);
		vi.advanceTimersByTime(150);
		expect(resizes(ws)).toHaveLength(1);
		// The same socket reopening stands in for a reconnect to a new worker PTY.
		ws.emit("open", {});
		expect(resizes(ws)).toEqual([
			{ type: "resize", columns: 100, rows: 30 },
			{ type: "resize", columns: 100, rows: 30 },
		]);
		mux.dispose();
	});
});

describe("createCloudTerminalMux cursor resume", () => {
	it("advances a shared cursor on output and resumes a rebuilt mux from it", async () => {
		FakeWebSocket.instances = [];
		const cursor = { value: 0 };
		const mux1 = makeMux(cursor);
		await settle();
		const ws1 = FakeWebSocket.instances[0];
		expect(afterOf(ws1)).toBe("0"); // first connect starts at 0
		ws1.deliver({ type: "output", sequence: 5, data: b64("hi") });
		expect(cursor.value).toBe(5);
		mux1.dispose();

		// A fresh mux with the same cursor object sends after=5 (the CP decides
		// whether to honor it or reset to 0; the plumbing preserves the position).
		const mux2 = makeMux(cursor);
		await settle();
		const ws2 = FakeWebSocket.instances[1];
		expect(afterOf(ws2)).toBe("5");
		mux2.dispose();
	});

	it("on a reset frame drops the cursor to 0 and clears the pane", async () => {
		FakeWebSocket.instances = [];
		const cursor = { value: 42 };
		const mux = makeMux(cursor);
		await settle();
		const ws = FakeWebSocket.instances[0];
		const chunks: string[] = [];
		mux.onData("agent", (bytes) => chunks.push(new TextDecoder().decode(bytes)));
		ws.deliver({ type: "reset" });
		expect(cursor.value).toBe(0);
		// A clear-screen + scrollback-wipe sequence is emitted to the pane.
		expect(chunks.join("")).toContain("\x1b[2J");
		mux.dispose();
	});

	it("routes a reviewer ticket to its exact terminal", async () => {
		FakeWebSocket.instances = [];
		const mux = createCloudTerminalMux({
			wsBaseUrl: "wss://cp.example.com/api/cloud/v1",
			kind: "agent",
			terminalId: "reviewer-terminal-id",
			mintTicket: async () => "ticket-1",
			WebSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
		});
		await Promise.resolve();
		await Promise.resolve();
		const url = new URL(FakeWebSocket.instances[0].url.replace(/^ws/, "http"));
		expect(url.searchParams.get("terminalId")).toBe("reviewer-terminal-id");
		mux.dispose();
	});

});

describe("createCloudTerminalMux cloud notification hints", () => {
	it("forwards a structured notification hint without writing terminal bytes", async () => {
		FakeWebSocket.instances = [];
		const mux = makeMux();
		await settle();
		const ws = FakeWebSocket.instances[0];
		const hints: string[] = [];
		const unsubscribe = subscribeCloudNotificationHints((hint) => hints.push(hint.eventId));
		ws.deliver({ type: "notification_hint", eventId: "evt-1", eventType: "needs_input", occurredAt: "2026-09-21T00:00:00Z", payload: { message: "Need a choice" } });
		expect(hints).toEqual(["evt-1"]);
		unsubscribe();
		mux.dispose();
	});
});

describe("createCloudTerminalMux shared session input", () => {
	it("tells the person once when the agent refuses keystrokes someone else's turn blocks", async () => {
		FakeWebSocket.instances = [];
		useUiStore.setState({ globalToasts: [] });
		const mux = makeMux();
		await settle();
		const ws = FakeWebSocket.instances[0];
		const output: number[] = [];
		mux.onData("pane", (bytes) => output.push(bytes.length));
		ws.deliver({ type: "input_refused", inputId: "in-1", message: "Someone else is writing to the agent." });
		ws.deliver({ type: "input_refused", inputId: "in-2", message: "Someone else is writing to the agent." });
		const toasts = useUiStore.getState().globalToasts.filter((toast) => toast.dedupeKey === "composer-terminal-refused");
		expect(toasts).toHaveLength(1);
		expect(toasts[0].title).toBe("Someone else is writing to the agent. Request a turn in Chat to type.");
		expect(output).toEqual([]);
		mux.dispose();
	});
});

describe("createCloudTerminalMux ticket refusals", () => {
	function refusingMux(error: object) {
		const mux = createCloudTerminalMux({
			wsBaseUrl: "wss://cp.example.com/api/cloud/v1",
			kind: "agent",
			mintTicket: async () => {
				throw error;
			},
			WebSocketImpl: FakeWebSocket as unknown as typeof WebSocket,
		});
		const errors: string[] = [];
		const states: string[] = [];
		mux.onError("pane", (message) => errors.push(message));
		mux.onConnectionChange((state) => states.push(state));
		return { mux, errors, states };
	}

	it("reports a plan refusal once as a pane error instead of polling", async () => {
		FakeWebSocket.instances = [];
		const { mux, errors, states } = refusingMux({ code: "WEEKLY_LIMIT_REACHED", message: "Weekly usage limit reached." });
		await settle();
		expect(errors).toEqual(["Weekly usage limit reached."]);
		expect(states).toEqual([]);
		expect(FakeWebSocket.instances).toHaveLength(0);
		mux.dispose();
	});

	it("keeps waiting while the worker is still starting", async () => {
		FakeWebSocket.instances = [];
		const { mux, errors, states } = refusingMux({ code: "WORKER_UNAVAILABLE", message: "Worker is starting." });
		await settle();
		expect(errors).toEqual([]);
		expect(states).toEqual(["waiting"]);
		mux.dispose();
	});
});
