import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, apiRequest } from "./api";
import { LocalBrowserTransport } from "./browserLive";
import { BrowserStreamSupervisor } from "./browserStreamSupervisor";

vi.mock("./config", () => ({
	authHeaders: (config: { password: string }) => ({ Authorization: `Bearer ${config.password}` }),
}));
vi.mock("./api", () => ({
	ApiError: class extends Error {
		constructor(readonly status: number, message: string, readonly code?: string) { super(message); }
	},
	apiRequest: vi.fn(),
}));

class FailingSocket {
	static made = 0;
	binaryType = "";
	readyState = 0;
	onopen: (() => void) | null = null;
	onerror: (() => void) | null = null;
	onclose: (() => void) | null = null;
	onmessage: ((event: { data: unknown }) => void) | null = null;
	constructor() {
		FailingSocket.made++;
		// A refused handshake: an error, then the close.
		queueMicrotask(() => { this.onerror?.(); this.onclose?.(); });
	}
	send() {}
	close() {}
}

beforeEach(() => {
	vi.useFakeTimers();
	FailingSocket.made = 0;
	vi.stubGlobal("WebSocket", FailingSocket);
});
afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
	vi.mocked(apiRequest).mockReset();
});

describe("supervisor around the real local transport", () => {
	it("a refused handshake whose probe says sharing is off stops for good, even when the probe is slower than the first retry", async () => {
		// The probe answers after 2.5 s: past the first 1 s retry.
		vi.mocked(apiRequest).mockImplementation(() => new Promise((_, reject) => {
			setTimeout(() => reject(new ApiError(403, "Browser viewing from mobile is disabled on the desktop", "BROWSER_REMOTE_DISABLED")), 2_500);
		}));
		const onDisabled = vi.fn();
		const supervisor = new BrowserStreamSupervisor(
			{ create: () => new LocalBrowserTransport({ host: "desktop.local", httpPort: "3011", muxPort: "3011", password: "pw" }, "s1") },
			{ onStatus: vi.fn(), onState: vi.fn(), onFrame: vi.fn(), onStale: vi.fn(), onDisabled },
		);
		supervisor.start();
		await vi.advanceTimersByTimeAsync(3_000);
		expect(onDisabled).toHaveBeenCalledTimes(1);
		const dialed = FailingSocket.made;
		await vi.advanceTimersByTimeAsync(120_000);
		expect(FailingSocket.made).toBe(dialed);
		supervisor.stop();
	});

	it("an old desktop's 404 also stops it, through the same probe", async () => {
		vi.mocked(apiRequest).mockRejectedValue(new ApiError(404, "Not found"));
		const onDisabled = vi.fn();
		const supervisor = new BrowserStreamSupervisor(
			{ create: () => new LocalBrowserTransport({ host: "desktop.local", httpPort: "3011", muxPort: "3011", password: "pw" }, "s1") },
			{ onStatus: vi.fn(), onState: vi.fn(), onFrame: vi.fn(), onStale: vi.fn(), onDisabled },
		);
		supervisor.start();
		await vi.advanceTimersByTimeAsync(100);
		expect(onDisabled).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(60_000);
		expect(FailingSocket.made).toBe(1);
		supervisor.stop();
	});
});
