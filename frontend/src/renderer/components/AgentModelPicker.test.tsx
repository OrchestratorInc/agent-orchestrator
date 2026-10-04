import { act, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import type { AgentModelCatalog } from "../hooks/useAgentModelsQuery";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: get }, apiErrorMessage: String }));
import { AgentModelPicker } from "./AgentModelPicker";

afterEach(() => vi.clearAllMocks());

it.each(["opencode", "cursor"])("keeps %s's model picker usable during its first catalog fetch", async (agentId) => {
	let resolve!: (response: { data: AgentModelCatalog }) => void;
	get.mockReturnValue(new Promise((done) => { resolve = done; }));
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const view = render(<QueryClientProvider client={client}>
		<AgentModelPicker agentId={agentId} agentLabel={agentId} projectId="project-a" value="" mode=""
			onModelChange={vi.fn()} onModeChange={vi.fn()} onWarningChange={vi.fn()} />
	</QueryClientProvider>);
	const picker = screen.getByRole("button", { name: "Model" });
	expect(picker).toBeEnabled();
	expect(picker).toHaveTextContent("Default");
	expect(picker).toHaveAttribute("aria-busy", "true");
	expect(screen.queryByText("Loading models…")).not.toBeInTheDocument();
	await act(async () => resolve({ data: {
		agentId, customModelEntry: "none", fetchedAt: "2026-10-04T00:00:00Z", source: "native", stale: false, models: [{ id: "native-model", label: "Native model", isDefault: true }],
		selectionMode: "catalog", allowCustom: false,
	} }));
	await waitFor(() => expect(picker).toHaveTextContent("Native model"));
	expect(picker).not.toHaveAttribute("aria-busy", "true");
	view.unmount();
	client.clear();
});
