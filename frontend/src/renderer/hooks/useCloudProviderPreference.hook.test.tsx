import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	userId: "alice",
	status: "authenticated",
	get: vi.fn(),
	put: vi.fn(),
}));

vi.mock("../lib/cloud-session", () => ({
	useCloudSession: () => ({ status: state.status, session: state.status === "authenticated" ? { user: { id: state.userId } } : null }),
}));
vi.mock("./useCloudCp", () => ({
	useCloudCp: () => ({ client: { getUserPreferences: state.get, putUserPreferences: state.put }, ready: state.status === "authenticated", baseUrl: "https://cloud.test" }),
}));
vi.mock("./useCloudSandboxProviders", () => ({
	useCloudSandboxProviders: () => ({ available: ["nodeops", "coder"], isSuccess: true }),
}));

import { useCloudProviderPreference } from "./useCloudProviderPreference";

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useCloudProviderPreference", () => {
	beforeEach(() => {
		state.userId = "alice";
		state.status = "authenticated";
		state.get.mockReset().mockImplementation(async () => ({ sandboxProvider: state.userId === "alice" ? "coder" : "nodeops" }));
		state.put.mockReset();
	});

	it("keeps the authoritative value and reports a failed save", async () => {
		state.put.mockRejectedValue(new Error("save failed"));
		const { result } = renderHook(() => useCloudProviderPreference(), { wrapper });
		await waitFor(() => expect(result.current.provider).toBe("coder"));
		await act(async () => { await result.current.setProvider("nodeops"); });
		expect(result.current.provider).toBe("coder");
		expect(result.current.error).toBe("save failed");
	});

	it("does not show the previous account's choice after switching or signing out", async () => {
		const { result, rerender } = renderHook(() => useCloudProviderPreference(), { wrapper });
		await waitFor(() => expect(result.current.provider).toBe("coder"));
		state.userId = "bob";
		rerender();
		await waitFor(() => expect(result.current.provider).toBe("nodeops"));
		state.status = "unauthenticated";
		rerender();
		expect(result.current.provider).toBeNull();
	});
});
