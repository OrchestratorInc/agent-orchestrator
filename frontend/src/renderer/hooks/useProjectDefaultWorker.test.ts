import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useProjectDefaultWorker, useProjectWorkerDefaults } from "./useProjectDefaultWorker";

const get = vi.hoisted(() => vi.fn());
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: get },
	apiErrorMessage: (error: { error?: { message?: string } }) => error?.error?.message ?? "request failed",
}));

function wrapper(client: QueryClient) {
	return ({ children }: { children: ReactNode }) =>
		createElement(QueryClientProvider, { client }, children);
}

describe("useProjectDefaultWorker", () => {
	beforeEach(() => get.mockReset());

	it("resolves the project worker agent ahead of the daemon-wide default", async () => {
		get.mockResolvedValue({
			data: {
				status: "ok",
				project: { id: "demo", agent: "claude-code", config: { worker: { agent: "codex" } } },
			},
		});
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useProjectDefaultWorker("demo"), { wrapper: wrapper(client) });
		await waitFor(() => expect(result.current).toBe("codex"));
		expect(get).toHaveBeenCalledWith("/api/v1/projects/{id}", { params: { path: { id: "demo" } } });
	});

	it("falls back to the project default agent when no worker override exists", async () => {
		get.mockResolvedValue({
			data: { status: "ok", project: { id: "demo", agent: "claude-code", config: {} } },
		});
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useProjectDefaultWorker("demo"), { wrapper: wrapper(client) });
		await waitFor(() => expect(result.current).toBe("claude-code"));
	});

	it("stays empty without a project and does not fetch", () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useProjectDefaultWorker(""), { wrapper: wrapper(client) });
		expect(result.current).toBe("");
		expect(get).not.toHaveBeenCalled();
	});
	it("exposes worker and base tuning from the shared project query", async () => {
		get.mockResolvedValue({ data: { status: "ok", project: { agent: "codex", config: {
			agentConfig: { model: "base-model", effort: "medium" },
			worker: { agent: "codex", agentConfig: { model: "capable", effort: "low", mode: "" } },
		} } } });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useProjectWorkerDefaults("demo"), { wrapper: wrapper(client) });
		expect(result.current.loaded).toBe(false);
		await waitFor(() => expect(result.current.harness).toBe("codex"));
		expect(result.current.loaded).toBe(true);
		expect(result.current.agentConfig).toEqual({ model: "base-model", effort: "medium" });
		expect(result.current.worker?.agentConfig).toEqual({ model: "capable", effort: "low", mode: "" });
	});

	it.each(["Unknown project", "Daemon unavailable"])("settles when project defaults return %s", async (message) => {
		get.mockResolvedValue({ error: { error: { message } } });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useProjectWorkerDefaults("missing"), { wrapper: wrapper(client) });
		expect(result.current.loaded).toBe(false);
		await waitFor(() => expect(result.current.loaded).toBe(true));
		expect(result.current.harness).toBe("");
		expect(result.current.agentConfig).toBeUndefined();
		expect(result.current.worker).toBeUndefined();
	});

});
