import { EventEmitter } from "node:events";
import type net from "node:net";
import { describe, expect, it, vi } from "vitest";
import { connectBrowserStream, type BrowserEncodedFrame, type BrowserStreamControl } from "./browser-stream-link";

class FakeSocket extends EventEmitter {
	destroyed = false;
	readonly writes: Buffer[] = [];
	readonly writeResults: boolean[] = [];

	write(packet: Uint8Array): boolean {
		this.writes.push(Buffer.from(packet));
		return this.writeResults.shift() ?? true;
	}

	destroy(): this {
		this.destroyed = true;
		return this;
	}
}

function frame(streamId: number, sequence: number): BrowserEncodedFrame {
	return {
		streamId,
		sequence: BigInt(sequence),
		capturedAtMs: 1n,
		width: 100,
		height: 100,
		jpeg: Buffer.from([sequence]),
	};
}

function decodeFrame(packet: Buffer): { streamId: number; sequence: bigint } | null {
	if (packet[4] !== 2) return null;
	return {
		streamId: packet.readUInt32BE(5),
		sequence: packet.readBigUInt64BE(9),
	};
}

function controlPacket(control: BrowserStreamControl): Buffer {
	const body = Buffer.from(JSON.stringify(control));
	const packet = Buffer.alloc(body.length + 5);
	packet.writeUInt32BE(body.length + 1);
	packet[4] = 1;
	body.copy(packet, 5);
	return packet;
}

function controls(socket: FakeSocket): BrowserStreamControl[] {
	return socket.writes.filter((packet) => packet[4] === 1).map((packet) => JSON.parse(packet.subarray(5).toString("utf8")));
}

describe("browser stream link", () => {
	it("coalesces high-frequency read-only input rejections while a frame write is blocked", async () => {
		vi.useFakeTimers();
		const socket = new FakeSocket();
		const onControl = vi.fn(() => { throw Object.assign(new Error("Agent owns the browser"), { code: "BROWSER_REMOTE_READ_ONLY" }); });
		const handle = connectBrowserStream("ignored", { token: "secret", onControl, createConnection: () => socket as unknown as net.Socket });
		try {
			socket.emit("connect");
			socket.writeResults.push(false);
			handle.sendFrame(frame(1, 1));
			const wheel = controlPacket({ type: "input", sessionId: "session-1", streamId: 1, payload: { kind: "wheel", deltaY: 10 } });
			for (let i = 0; i < 120; i++) socket.emit("data", wheel);
			await vi.advanceTimersByTimeAsync(0);
			expect(onControl).toHaveBeenCalledTimes(120);
			expect(controls(socket).filter((control) => control.type === "error")).toHaveLength(1);

			handle.sendControl({ type: "state", streamId: 1, payload: { url: "https://example.test" } });
			expect(controls(socket).at(-1)?.type).toBe("state");
			await vi.advanceTimersByTimeAsync(499);
			socket.emit("data", wheel);
			await vi.advanceTimersByTimeAsync(0);
			expect(controls(socket).filter((control) => control.type === "error")).toHaveLength(1);
			await vi.advanceTimersByTimeAsync(1);
			socket.emit("data", wheel);
			await vi.advanceTimersByTimeAsync(0);
			expect(controls(socket).filter((control) => control.type === "error")).toHaveLength(2);
		} finally {
			handle.dispose();
			vi.useRealTimers();
		}
	});

	it("limits each stream independently and never coalesces fatal errors", async () => {
		const socket = new FakeSocket();
		const onControl = vi.fn((control: BrowserStreamControl) => {
			throw Object.assign(new Error("Rejected"), { code: control.type === "input" ? "BROWSER_REMOTE_READ_ONLY" : "BROWSER_TARGET_UNAVAILABLE" });
		});
		const handle = connectBrowserStream("ignored", { token: "secret", onControl, createConnection: () => socket as unknown as net.Socket });
		try {
			socket.emit("connect");
			for (const streamId of [1, 2]) {
				socket.emit("data", controlPacket({ type: "input", sessionId: `session-${streamId}`, streamId }));
			}
			for (let i = 0; i < 2; i++) socket.emit("data", controlPacket({ type: "navigate", sessionId: "session-1", streamId: 1 }));
			await vi.waitFor(() => expect(controls(socket).filter((control) => control.type === "error")).toHaveLength(4));
			expect(controls(socket).filter((control) => control.code === "BROWSER_REMOTE_READ_ONLY").map((control) => control.streamId).sort()).toEqual([1, 2]);
			expect(controls(socket).filter((control) => control.code === "BROWSER_TARGET_UNAVAILABLE")).toHaveLength(2);
		} finally { handle.dispose(); }
	});

	it("reports the first rejection again after a stream restart or host reconnect", async () => {
		vi.useFakeTimers();
		const sockets: FakeSocket[] = [];
		const handle = connectBrowserStream("ignored", {
			token: "secret",
			onControl: (control) => { if (control.type === "input") throw Object.assign(new Error("Read-only"), { code: "BROWSER_REMOTE_READ_ONLY" }); },
			createConnection: () => { const socket = new FakeSocket(); sockets.push(socket); return socket as unknown as net.Socket; },
		});
		const wheel = controlPacket({ type: "input", sessionId: "session-1", streamId: 1 });
		try {
			const first = sockets[0]!;
			first.emit("connect");
			first.emit("data", wheel);
			await vi.advanceTimersByTimeAsync(0);
			first.emit("data", controlPacket({ type: "stop", sessionId: "session-1", streamId: 1 }));
			first.emit("data", controlPacket({ type: "start", sessionId: "session-1", streamId: 1 }));
			first.emit("data", wheel);
			await vi.advanceTimersByTimeAsync(0);
			expect(controls(first).filter((control) => control.type === "error")).toHaveLength(2);
			first.emit("close");
			await vi.advanceTimersByTimeAsync(200);
			const second = sockets[1]!;
			second.emit("connect");
			second.emit("data", wheel);
			await vi.advanceTimersByTimeAsync(0);
			expect(controls(second).filter((control) => control.type === "error")).toHaveLength(1);
		} finally {
			handle.dispose();
			vi.useRealTimers();
		}
	});

	it("runs disconnect cleanup before reconnecting after an authenticated link drops", async () => {
		vi.useFakeTimers();
		try {
			const sockets: FakeSocket[] = [];
			let releaseCleanup!: () => void;
			const cleanup = new Promise<void>((resolve) => { releaseCleanup = resolve; });
			const onDisconnect = vi.fn(() => cleanup);
			const handle = connectBrowserStream("ignored", {
				token: "secret",
				onControl: vi.fn(),
				onDisconnect,
				createConnection: () => {
					const socket = new FakeSocket();
					sockets.push(socket);
					return socket as unknown as net.Socket;
				},
			});
			sockets[0]!.emit("connect");

			sockets[0]!.emit("close");
			expect(onDisconnect).toHaveBeenCalledOnce();
			await vi.advanceTimersByTimeAsync(2_000);
			expect(sockets).toHaveLength(1);

			releaseCleanup();
			await Promise.resolve();
			await vi.advanceTimersByTimeAsync(200);
			expect(sockets).toHaveLength(2);
			sockets[1]!.emit("connect");
			handle.dispose();
			expect(onDisconnect).toHaveBeenCalledTimes(2);
		} finally {
			vi.useRealTimers();
		}
	});

	it("keeps only the newest frame per stream while socket writes are blocked", () => {
		const socket = new FakeSocket();
		// The hello and first frame are accepted; the first frame also signals
		// backpressure and must be the final write until drain.
		socket.writeResults.push(true, false, true, true);
		const handle = connectBrowserStream("ignored", {
			token: "secret",
			onControl: vi.fn(),
			createConnection: () => socket as unknown as net.Socket,
		});
		socket.emit("connect");

		handle.sendFrame(frame(1, 1));
		handle.sendFrame(frame(1, 2));
		handle.sendFrame(frame(2, 1));
		handle.sendFrame(frame(1, 3));

		expect(socket.writes.map(decodeFrame).filter((value) => value !== null)).toEqual([
			{ streamId: 1, sequence: 1n },
		]);

		socket.emit("drain");

		expect(socket.writes.map(decodeFrame).filter((value) => value !== null)).toEqual([
			{ streamId: 1, sequence: 1n },
			{ streamId: 1, sequence: 3n },
			{ streamId: 2, sequence: 1n },
		]);
		handle.dispose();
	});
});
