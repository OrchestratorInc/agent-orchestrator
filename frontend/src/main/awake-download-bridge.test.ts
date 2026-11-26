// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BrowserDownloadsState } from "../shared/browser-downloads";
import { createAwakeDownloadBridge } from "./awake-download-bridge";
function downloads(active: boolean): BrowserDownloadsState {
	return {
		downloads: [
			{
				id: "one",
				fileName: "secret.zip",
				status: active ? "progressing" : "completed",
				active,
				receivedBytes: 0,
				totalBytes: 10,
				startedAt: 1,
				updatedAt: 1,
			},
		],
	};
}
afterEach(() => vi.useRealTimers());
describe("awake download ownership", () => {
	it("publishes typed IDs only and flushes a completion arriving during a request", async () => {
		let finish: () => void = () => {};
		const request = vi
			.fn()
			.mockImplementationOnce(
				() =>
					new Promise<void>((resolve) => {
						finish = resolve;
					}),
			)
			.mockResolvedValue(undefined);
		const bridge = createAwakeDownloadBridge({
			baseURL: () => "http://127.0.0.1:3001",
			request,
		});
		bridge.update(downloads(true));
		bridge.update(downloads(false));
		expect(request).toHaveBeenCalledTimes(1);
		expect(JSON.parse(request.mock.calls[0][1].body)).toEqual({
			activeIds: ["one"],
		});
		finish();
		await Promise.resolve();
		await Promise.resolve();
		expect(request).toHaveBeenCalledTimes(2);
		expect(JSON.parse(request.mock.calls[1][1].body)).toEqual({
			activeIds: [],
		});
		bridge.dispose();
	});
	it("retries active heartbeats after daemon startup and stops after disposal", async () => {
		vi.useFakeTimers();
		let base: string | null = null;
		const request = vi.fn().mockResolvedValue(undefined);
		const bridge = createAwakeDownloadBridge({ baseURL: () => base, request });
		bridge.update(downloads(true));
		expect(request).not.toHaveBeenCalled();
		base = "http://127.0.0.1:3001";
		await vi.advanceTimersByTimeAsync(5000);
		expect(request).toHaveBeenCalledTimes(1);
		bridge.dispose();
		await vi.advanceTimersByTimeAsync(10000);
		bridge.update(downloads(false));
		expect(request).toHaveBeenCalledTimes(1);
	});
});
