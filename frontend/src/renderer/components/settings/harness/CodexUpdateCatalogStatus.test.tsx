import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CodexUpdateCatalogStatus } from "./CodexUpdateCatalogStatus";

let response: Record<string, unknown>;
vi.mock("../../../hooks/useAgentModelsQuery", async (importOriginal) => ({
	...await importOriginal<typeof import("../../../hooks/useAgentModelsQuery")>(),
	agentModelsQueryOptions: () => ({ queryKey: ["codex-catalog-status"], queryFn: async () => response }),
}));

describe("Codex update catalog result", () => {
	it.each([
		{ data: { refreshState: "queued" }, expected: /Refreshing available models/ },
		{ data: { refreshState: "idle", validatedAt: "2026-10-26T00:00:00Z" }, expected: /Refreshing available models/ },
		{ data: { refreshState: "error", stale: true, refreshError: "offline" }, expected: /model discovery could not be verified/ },
		{ data: { refreshState: "idle", validatedAt: "2026-10-27T12:01:00Z" }, expected: /models refreshed.*account/ },
	])("separates CLI success from catalog evidence: $data.refreshState", async ({ data, expected }) => {
		response = { models: [], ...data };
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><CodexUpdateCatalogStatus startedAt="2026-10-27T12:00:00Z" /></QueryClientProvider>);
		expect(await screen.findByText(expected)).toBeInTheDocument();
	});
});
