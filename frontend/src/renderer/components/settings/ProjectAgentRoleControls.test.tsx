import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api-client", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../lib/api-client")>()),
	apiClient: { GET: api.get, POST: api.post },
}));

import { AgentModelField } from "./ProjectAgentRoleControls";

const MODELS_ROUTE = "/api/v1/agents/{agent}/models";
const modelRequests = () => api.get.mock.calls.filter(([url]) => url === MODELS_ROUTE);

function renderField(agentId: string, variant: "task" | "settings") {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const noop = () => {};
	return render(
		<QueryClientProvider client={client}>
			<AgentModelField role="worker" variant={variant} agentId={agentId} projectId="" model="" mode="" effort=""
				onModelChange={noop} onModeChange={noop} onEffortChange={noop} onValidityChange={noop} />
		</QueryClientProvider>,
	);
}

describe("AgentModelField", () => {
	beforeEach(() => {
		// The daemon's answer for a models request that names no agent.
		api.get.mockReset().mockImplementation(async (url: string) => url === MODELS_ROUTE
			? { data: undefined, error: { code: "AGENT_REQUIRED", message: "agent is required" } }
			: { data: undefined, error: undefined });
		api.post.mockReset().mockResolvedValue({ data: undefined, error: undefined });
	});

	// A form can open before any agent resolves (no project default and no agent
	// confirmed ready). Asking for that empty agent's models only earns an
	// "agent is required" error under a field the user has not touched.
	it.each(["task", "settings"] as const)("requests no models and shows no error until an agent is chosen (%s)", async (variant) => {
		renderField("", variant);

		// Let the settings read settle so a models request would have been made.
		await waitFor(() => expect(api.get).toHaveBeenCalled());
		await new Promise((resolve) => setTimeout(resolve, 50));

		expect(modelRequests()).toEqual([]);
		expect(screen.queryByText("agent is required")).not.toBeInTheDocument();
	});

	it("requests the chosen agent's models", async () => {
		api.get.mockImplementation(async (url: string) => url === MODELS_ROUTE
			? { data: { agentId: "codex", models: [], selectionMode: "catalog", allowCustom: false, customModelEntry: "none" }, error: undefined }
			: { data: undefined, error: undefined });

		renderField("codex", "task");

		await waitFor(() => expect(modelRequests().length).toBeGreaterThan(0));
		expect(modelRequests()[0]?.[1]?.params?.path).toEqual({ agent: "codex" });
	});
});
