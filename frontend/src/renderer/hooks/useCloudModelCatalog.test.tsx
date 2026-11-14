import { focusManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MAX_UNVERIFIED_CATALOG_REFRESH_MS, UNVERIFIED_CATALOG_REFRESH_MS, useCloudModelCatalog } from "./useCloudModelCatalog";

const listUserAgentModels = vi.hoisted(() => vi.fn());
vi.mock("./useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, baseUrl: "https://cp.test", userId: "user-1", client: { listUserAgentModels } }),
}));
vi.mock("./useProviderConnections", () => ({
	useProviderConnections: () => ({ data: [{ id: "c1", provider: "codex", label: "default", validationState: "valid", updatedAt: "t" }], isError: false }),
}));

function Probe({ onState }: { onState: (source: string | undefined) => void }) {
	const { catalog } = useCloudModelCatalog({ orgId: "org-1", agent: "codex", enabled: true });
	onState(catalog?.source);
	return null;
}

describe("useCloudModelCatalog", () => {
	beforeEach(() => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		listUserAgentModels.mockReset();
	});
	afterEach(() => vi.useRealTimers());

	it("reads the release list again until the account's own arrives, then stops", async () => {
		listUserAgentModels
			.mockResolvedValueOnce({ source: "release", cliVersion: "0.165.0", models: [{ id: "gpt-6-luna", displayName: "Luna", default: true }] })
			.mockResolvedValue({ source: "worker", cliVersion: "0.165.0", models: [{ id: "gpt-6.1-sol", displayName: "Sol", default: true }] });
		const seen: Array<string | undefined> = [];
		render(<QueryClientProvider client={new QueryClient()}><Probe onState={(source) => seen.push(source)} /></QueryClientProvider>);
		await act(async () => { await vi.advanceTimersByTimeAsync(10); });
		expect(seen.at(-1)).toBe("release");
		expect(listUserAgentModels).toHaveBeenCalledTimes(1);
		await act(async () => { await vi.advanceTimersByTimeAsync(UNVERIFIED_CATALOG_REFRESH_MS + 10); });
		expect(seen.at(-1)).toBe("worker");
		expect(listUserAgentModels).toHaveBeenCalledTimes(2);
		// The account's own list is not polled.
		await act(async () => { await vi.advanceTimersByTimeAsync(3 * UNVERIFIED_CATALOG_REFRESH_MS); });
		expect(listUserAgentModels).toHaveBeenCalledTimes(2);
	});

	const release = { source: "release", cliVersion: "0.165.0", models: [{ id: "gpt-6-luna", displayName: "Luna", default: true }] };
	const after = async (ms: number) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); };

	it("reads the release list less often the longer it stays the release list: 15s, 30s, 60s, 120s, up to 5 minutes", async () => {
		listUserAgentModels.mockResolvedValue(release);
		render(<QueryClientProvider client={new QueryClient()}><Probe onState={() => undefined} /></QueryClientProvider>);
		let now = 0;
		const to = async (at: number) => { await after(at - now); now = at; };
		await to(10);
		expect(listUserAgentModels).toHaveBeenCalledTimes(1);
		// Each gap doubles from 15s, and stops growing at 5 minutes.
		const gaps = [15_000, 30_000, 60_000, 120_000, 240_000, MAX_UNVERIFIED_CATALOG_REFRESH_MS, MAX_UNVERIFIED_CATALOG_REFRESH_MS];
		let readAt = 0;
		for (const [index, gap] of gaps.entries()) {
			readAt += gap;
			await to(readAt - 50);
			expect(listUserAgentModels).toHaveBeenCalledTimes(index + 1);
			await to(readAt + 50);
			expect(listUserAgentModels).toHaveBeenCalledTimes(index + 2);
		}
		expect(MAX_UNVERIFIED_CATALOG_REFRESH_MS).toBe(5 * 60_000);
	});

	it("starts again at 15s when the window regains focus", async () => {
		listUserAgentModels.mockResolvedValue(release);
		render(<QueryClientProvider client={new QueryClient()}><Probe onState={() => undefined} /></QueryClientProvider>);
		await after(10);
		await after(15_000 + 30_000 + 60_000 + 50); // three more reads: the next is 120s away
		expect(listUserAgentModels).toHaveBeenCalledTimes(4);
		await act(async () => { focusManager.setFocused(false); });
		await act(async () => { focusManager.setFocused(true); });
		await after(10);
		// Focus reads it at once, and the next read is 15s later, not 120s.
		expect(listUserAgentModels).toHaveBeenCalledTimes(5);
		await after(UNVERIFIED_CATALOG_REFRESH_MS + 50);
		expect(listUserAgentModels).toHaveBeenCalledTimes(6);
		await act(async () => { focusManager.setFocused(undefined); });
	});

	it("reads once per interval for every picker of one agent, however many are open", async () => {
		listUserAgentModels.mockResolvedValue(release);
		const client = new QueryClient();
		const view = render(<QueryClientProvider client={client}><Probe onState={() => undefined} /></QueryClientProvider>);
		await after(5_000);
		view.rerender(<QueryClientProvider client={client}><Probe onState={() => undefined} /><Probe onState={() => undefined} /></QueryClientProvider>);
		await after(5_000);
		view.rerender(<QueryClientProvider client={client}><Probe onState={() => undefined} /><Probe onState={() => undefined} /><Probe onState={() => undefined} /></QueryClientProvider>);
		// Reads at 0, 15s, 45s and 105s: one per interval, not one per picker.
		await after(105_000 - 10_000 + 50);
		expect(listUserAgentModels).toHaveBeenCalledTimes(4);
	});
});
