import { describe, expect, it } from "vitest";
import { openChannel } from "./channel";

class SilentSocket {
	static last: SilentSocket;
	binaryType = "";
	closed = false;
	onopen: (() => void) | null = null;
	onmessage: (() => void) | null = null;
	onerror: (() => void) | null = null;
	onclose: (() => void) | null = null;
	constructor() {
		SilentSocket.last = this;
	}
	send() {}
	close() {
		this.closed = true;
	}
}

describe("openChannel", () => {
	it("stops the handshake when the caller cancels instead of waiting out the timeout", async () => {
		const controller = new AbortController();
		const pending = openChannel("ws://host/api/v1/e2ee", new Uint8Array(32), {
			WebSocket: SilentSocket as unknown as typeof WebSocket,
			randomBytes: (count) => new Uint8Array(count).fill(1),
		}, controller.signal);
		await Promise.resolve();
		controller.abort();
		await expect(pending).rejects.toMatchObject({ name: "AbortError" });
		expect(SilentSocket.last.closed).toBe(true);
	});
});
