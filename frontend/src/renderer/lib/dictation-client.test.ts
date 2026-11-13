import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { DictationEvent } from "../../shared/dictation";
import { dictationClient } from "./dictation-client";

const mocks = vi.hoisted(() => ({
	get: vi.fn(),
	nativeStart: vi.fn(),
	nativeStop: vi.fn(),
	nativeCancel: vi.fn(),
	nativeListeners: new Set<(event: DictationEvent) => void>(),
}));
vi.mock("./api-client", () => ({ getApiBaseUrl: () => "http://127.0.0.1:43123", apiClient: { GET: mocks.get } }));
vi.mock("./bridge", () => ({
	aoBridge: {
		dictation: {
			start: mocks.nativeStart,
			stop: mocks.nativeStop,
			cancel: mocks.nativeCancel,
			authorize: async () => ({ granted: true }),
			onEvent: (listener: (event: DictationEvent) => void) => {
				mocks.nativeListeners.add(listener);
				return () => mocks.nativeListeners.delete(listener);
			},
		},
	},
}));
const stopTrack = vi.fn();
const closeAudio = vi.fn();
const microphone = vi.fn();
class Socket {
	static OPEN = 1;
	static instances: Socket[] = [];
	readyState = 1;
	bufferedAmount = 0;
	onmessage?: (event: { data: string }) => Promise<void>;
	onclose?: () => void;
	onerror?: () => void;
	send = vi.fn();
	close = vi.fn(() => {
		this.readyState = 3;
		this.onclose?.();
	});
	constructor() {
		Socket.instances.push(this);
	}
	async emit(event: DictationEvent) {
		await this.onmessage?.({ data: JSON.stringify(event) });
	}
}
class Context {
	state = "running";
	sampleRate = 16000;
	destination = {};
	audioWorklet = { addModule: vi.fn(async () => {}) };
	createMediaStreamSource = () => ({ connect: vi.fn() });
	resume = async () => {};
	close = async () => {
		if (this.state === "closed") throw new Error("Already closed");
		this.state = "closed";
		closeAudio();
	};
}
class Worklet {
	static instances: Worklet[] = [];
	constructor() {
		Worklet.instances.push(this);
	}
	port: { onmessage?: (event: { data: string | Float32Array }) => void; postMessage: () => void } = {
		postMessage: () => {
			this.port.onmessage?.({ data: "flushed" });
		},
	};
	connect() {}
	disconnect() {}
}
beforeEach(() => {
	vi.clearAllMocks();
	Socket.instances = [];
	Worklet.instances = [];
	mocks.get.mockResolvedValue({ data: { selected: "phonon-2.5", models: [{ id: "phonon-2.5", installed: true }] } });
	microphone.mockResolvedValue({ getTracks: () => [{ stop: stopTrack }] });
	vi.stubGlobal("WebSocket", Socket);
	vi.stubGlobal("AudioContext", Context);
	vi.stubGlobal("AudioWorkletNode", Worklet);
	Object.defineProperty(navigator, "mediaDevices", { value: { getUserMedia: microphone }, configurable: true });
});
afterEach(async () => {
	await dictationClient.cancel();
	vi.unstubAllGlobals();
	mocks.nativeListeners.clear();
});
it("flushes audio before stop and releases microphone once after final text", async () => {
	const events: DictationEvent[] = [];
	const off = dictationClient.onEvent((event) => events.push(event));
	await dictationClient.start();
	const socket = Socket.instances[0];
	await socket.emit({ type: "listening" });
	await dictationClient.stop();
	expect(socket.send).toHaveBeenCalledWith('{"type":"stop"}');
	await socket.emit({ type: "end", text: "Final correction." });
	await dictationClient.cancel();
	expect(stopTrack).toHaveBeenCalledTimes(1);
	expect(closeAudio).toHaveBeenCalledTimes(1);
	expect(events.at(-1)).toEqual({ type: "end", text: "Final correction." });
	off();
});
it("cancels a pending start without opening a socket or microphone", async () => {
	let ready!: (value: unknown) => void;
	mocks.get.mockReturnValue(
		new Promise((resolve) => {
			ready = resolve;
		}),
	);
	const start = dictationClient.start();
	await dictationClient.cancel();
	ready({ data: { selected: "phonon-2.5", models: [{ id: "phonon-2.5", installed: true }] } });
	await start;
	expect(Socket.instances).toHaveLength(0);
	expect(microphone).not.toHaveBeenCalled();
});
it("records immediately and drains cold-start audio before stop", async () => {
	const events: DictationEvent[] = [];
	const off = dictationClient.onEvent((event) => events.push(event));
	await dictationClient.start();
	await vi.waitFor(() => expect(events).toContainEqual({ type: "listening" }));
	const socket = Socket.instances[0];
	const audio = new Float32Array([0.1, 0.2]);
	Worklet.instances[0].port.onmessage?.({ data: audio });
	expect(socket.send).not.toHaveBeenCalled();
	await dictationClient.stop();
	expect(stopTrack).toHaveBeenCalledTimes(1);
	expect(socket.send).not.toHaveBeenCalled();
	await socket.emit({ type: "listening" });
	expect(socket.send.mock.calls.map((call) => call[0])).toEqual([audio.buffer, '{"type":"stop"}']);
	expect(events.filter((event) => event.type === "listening")).toHaveLength(1);
	off();
});
it("discards queued audio and ignores engine readiness after cancellation", async () => {
	await dictationClient.start();
	await vi.waitFor(() => expect(Worklet.instances).toHaveLength(1));
	const socket = Socket.instances[0];
	Worklet.instances[0].port.onmessage?.({ data: new Float32Array([0.1]) });
	await dictationClient.cancel();
	await socket.emit({ type: "listening" });
	expect(socket.send).not.toHaveBeenCalled();
	expect(stopTrack).toHaveBeenCalledTimes(1);
});
it("stops a microphone whose permission request resolves after cancellation", async () => {
	let granted!: (value: unknown) => void;
	microphone.mockReturnValue(
		new Promise((resolve) => {
			granted = resolve;
		}),
	);
	await dictationClient.start();
	const pending = Socket.instances[0].emit({ type: "listening" });
	await dictationClient.cancel();
	granted({ getTracks: () => [{ stop: stopTrack }] });
	await pending;
	expect(stopTrack).toHaveBeenCalledTimes(1);
});
it("reports permission failure and ignores native callbacks while using Phonon", async () => {
	const events: DictationEvent[] = [];
	const off = dictationClient.onEvent((event) => events.push(event));
	microphone.mockRejectedValue(new Error("Microphone permission denied"));
	await dictationClient.start();
	for (const listener of mocks.nativeListeners) listener({ type: "end", text: "stale" });
	await Socket.instances[0].emit({ type: "listening" });
	expect(events).toEqual([{ type: "error", message: "Microphone permission denied" }]);
	off();
});
it("uses the native helper only when native macOS is selected", async () => {
	mocks.get.mockResolvedValue({ data: { selected: "native-macos", models: [] } });
	await dictationClient.start();
	await dictationClient.stop();
	await dictationClient.cancel();
	expect(mocks.nativeStart).toHaveBeenCalledTimes(1);
	expect(mocks.nativeStop).toHaveBeenCalledTimes(1);
	expect(mocks.nativeCancel).toHaveBeenCalledTimes(1);
	expect(Socket.instances).toHaveLength(0);
	expect(microphone).not.toHaveBeenCalled();
});

it("releases permission granted after stop without switching back to listening", async () => {
	let granted!: (value: unknown) => void;
	microphone.mockReturnValue(
		new Promise((resolve) => {
			granted = resolve;
		}),
	);
	const events: DictationEvent[] = [];
	const off = dictationClient.onEvent((event) => events.push(event));
	await dictationClient.start();
	await dictationClient.stop();
	granted({ getTracks: () => [{ stop: stopTrack }] });
	await vi.waitFor(() => expect(stopTrack).toHaveBeenCalledTimes(1));
	await Socket.instances[0].emit({ type: "listening" });
	expect(events).toEqual([{ type: "finishing" }]);
	expect(Socket.instances[0].send).toHaveBeenCalledExactlyOnceWith('{"type":"stop"}');
	off();
});

it("refuses native dictation when there is no desktop bridge, instead of hanging", async () => {
	mocks.get.mockResolvedValue({
		data: { selected: "native-macos", models: [{ id: "native-macos", installed: true }] },
	});
	const bridge = window.ao;
	window.ao = undefined;
	try {
		await expect(dictationClient.start()).rejects.toThrow(/needs the desktop app/);
		expect(mocks.nativeStart).not.toHaveBeenCalled();
	} finally {
		window.ao = bridge;
	}
});
it("finishes a local recording when the window loses focus, and stops listening for that once it ends", async () => {
	const events: DictationEvent[] = [];
	const off = dictationClient.onEvent((event) => events.push(event));
	await dictationClient.start();
	const socket = Socket.instances[0];
	await socket.emit({ type: "listening" });
	await vi.waitFor(() => expect(events).toContainEqual({ type: "listening" }));
	window.dispatchEvent(new Event("blur"));
	await vi.waitFor(() => expect(socket.send).toHaveBeenCalledWith('{"type":"stop"}'));
	expect(events).toContainEqual({ type: "finishing" });
	await socket.emit({ type: "end", text: "Done." });
	socket.send.mockClear();
	window.dispatchEvent(new Event("blur"));
	expect(socket.send).not.toHaveBeenCalled();
	off();
});
