import { afterEach, describe, expect, it, vi } from "vitest";
import { BrowserLiveClient } from "./browserLive";
import { browserJPEGDataURI, browserLiveURL, browserWheelDeltaFromDrag, containBrowserFrame, decodeBrowserFrame } from "./browserLiveProtocol";

vi.mock("./config", () => ({
	authHeaders: (config: { password: string }) => ({ Authorization: `Bearer ${config.password}` }),
}));

afterEach(() => vi.unstubAllGlobals());

function encodedFrame(): ArrayBuffer {
	const bytes = new Uint8Array(25);
	const view = new DataView(bytes.buffer);
	bytes[0] = 1;
	bytes[1] = 1;
	view.setBigUint64(2, 7n);
	view.setBigUint64(10, 123n);
	view.setUint16(18, 640);
	view.setUint16(20, 360);
	bytes.set([0xff, 0xd8, 0xff], 22);
	return bytes.buffer;
}

describe("browser live transport", () => {
	it("builds the authenticated listener URL without putting the password in it", () => {
		const config = { host: "192.168.1.5", httpPort: "3011", password: "secret" };
		const url = browserLiveURL(config, "worker/a");
		expect(url).toBe("ws://192.168.1.5:3011/api/v1/sessions/worker%2Fa/browser/live");
		expect(url).not.toContain("secret");
	});

	it("decodes the versioned binary JPEG envelope", () => {
		const frame = decodeBrowserFrame(encodedFrame());
		expect(frame).toMatchObject({ sequence: 7n, capturedAtMs: 123n, width: 640, height: 360 });
		expect([...new Uint8Array(frame.jpeg)]).toEqual([0xff, 0xd8, 0xff]);
	});

	it("leaves handshake errors without generic copy so the sharing hint is shown", () => {
		class FakeWebSocket {
			static instance: FakeWebSocket;
			binaryType = "";
			onopen: (() => void) | null = null;
			onerror: (() => void) | null = null;
			onclose: (() => void) | null = null;
			onmessage: ((event: { data: unknown }) => void) | null = null;

			constructor() {
				FakeWebSocket.instance = this;
			}

			close() {}
		}
		vi.stubGlobal("WebSocket", FakeWebSocket);
		const statuses: Array<{ status: string; message?: string }> = [];
		const client = new BrowserLiveClient(
			{ host: "192.168.1.5", httpPort: "3011", muxPort: "3012", password: "secret" },
			"worker/a",
			{
				onStatus: (status, message) => statuses.push({ status, message }),
				onState: () => {},
				onFrame: () => {},
			},
		);

		client.connect();
		FakeWebSocket.instance.onerror?.();
		expect(statuses.at(-1)).toEqual({ status: "error", message: undefined });
	});

	it("restores an open stream when a valid frame follows a recoverable host error", async () => {
		class FakeWebSocket {
			static readonly OPEN = 1;
			static instance: FakeWebSocket;
			readonly readyState = FakeWebSocket.OPEN;
			binaryType = "";
			onopen: (() => void) | null = null;
			onerror: (() => void) | null = null;
			onclose: (() => void) | null = null;
			onmessage: ((event: { data: unknown }) => void) | null = null;

			constructor() {
				FakeWebSocket.instance = this;
			}

			send() {}
			close() {}
		}
		vi.stubGlobal("WebSocket", FakeWebSocket);
		const statuses: Array<{ status: string; message?: string }> = [];
		const frames: bigint[] = [];
		const client = new BrowserLiveClient(
			{ host: "192.168.1.5", httpPort: "3011", muxPort: "3012", password: "secret" },
			"worker/a",
			{
				onStatus: (status, message) => statuses.push({ status, message }),
				onState: () => {},
				onFrame: (frame) => frames.push(frame.sequence),
			},
		);

		client.connect();
		FakeWebSocket.instance.onopen?.();
		FakeWebSocket.instance.onmessage?.({ data: JSON.stringify({ type: "error", message: "Desktop input is busy" }) });
		await Promise.resolve();
		expect(statuses.at(-1)).toEqual({ status: "error", message: "Desktop input is busy" });

		FakeWebSocket.instance.onmessage?.({ data: encodedFrame() });
		await Promise.resolve();
		expect(statuses.at(-1)).toEqual({ status: "open", message: undefined });
		expect(frames).toEqual([7n]);
	});

	it("fits the desktop frame without stretching it", () => {
		expect(containBrowserFrame({ width: 390, height: 600 }, { width: 1280, height: 720 })).toEqual({
			left: 0,
			top: 190.3125,
			width: 390,
			height: 219.375,
		});
	});

	it("preserves drag signs for Electron wheel input so content follows the finger", () => {
		expect(browserWheelDeltaFromDrag(12, -30)).toEqual({ deltaX: 12, deltaY: -30 });
		expect(browserWheelDeltaFromDrag(-8, 24)).toEqual({ deltaX: -8, deltaY: 24 });
	});

	it("encodes JPEG bytes as a React Native image data URI", () => {
		expect(browserJPEGDataURI(Uint8Array.from([0xff, 0xd8, 0xff]).buffer)).toBe("data:image/jpeg;base64,/9j/");
	});
});
