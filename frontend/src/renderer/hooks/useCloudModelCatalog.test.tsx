import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { UNVERIFIED_CATALOG_REFRESH_MS, useCloudModelCatalog } from "./useCloudModelCatalog";

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
});
