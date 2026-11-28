import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { HarnessModelsPanel } from "./HarnessModelsPanel";

const h = vi.hoisted(() => ({
	catalog: undefined as {
		models: Array<{ id: string; label: string; isDefault?: boolean }>;
		allowCustom?: boolean;
		customModelEntry?: "direct" | "configured" | "none";
	} | undefined,
}));
afterEach(() => { h.catalog = undefined; });

const update = vi.fn(async () => undefined);
vi.mock("../../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { harnessDefaults: {} } }),
	useUpdateHarnessDefault: () => ({ update, error: undefined }),
}));
const endpoint = vi.hoisted(() => ({
	set: vi.fn(async () => ({})),
	refresh: vi.fn(async () => undefined),
}));
vi.mock("../../../hooks/useAgentEndpoint", () => ({
	setAgentEndpoint: endpoint.set,
	useRefreshAfterEndpointChange: () => endpoint.refresh,
}));
vi.mock("../../../hooks/useAgentModelsQuery", () => ({
	agentModelsQueryOptions: () => ({
		queryKey: ["agent-models", "cursor"],
		queryFn: async () => h.catalog ?? ({
			models: [
				{ id: "claude-opus-4-8-high", label: "Claude Opus 4.8 High", isDefault: true },
				{ id: "claude-opus-4-8-thinking-high", label: "Claude Opus 4.8 High Thinking" },
				{ id: "gpt-5.5-high", label: "GPT-5.5 High" },
			],
		}),
	}),
}));

describe("HarnessModelsPanel", () => {
	it("explains Mastra Code model-ID entry when its native catalog is freeform", async () => {
		h.catalog = { models: [], allowCustom: true, customModelEntry: "direct" };
		render(
			<QueryClientProvider client={new QueryClient()}>
				<HarnessModelsPanel agentId="mastra-code" installed needsLogin={false} onOpenAccount={() => {}} />
			</QueryClientProvider>,
		);
		expect(await screen.findByText(/Enter a model ID in New Task/)).toBeInTheDocument();
		expect(screen.queryByText("This harness reported no models.")).not.toBeInTheDocument();
	});

	it.each([
		["mastra-code", "configured"], ["mastra-code", "none"], ["codex", "direct"],
	] as const)("does not advertise model-ID entry for %s with a %s catalog", async (agentId, customModelEntry) => {
		h.catalog = { models: [], allowCustom: true, customModelEntry };
		render(
			<QueryClientProvider client={new QueryClient()}>
				<HarnessModelsPanel agentId={agentId} installed needsLogin={false} onOpenAccount={() => {}} />
			</QueryClientProvider>,
		);
		expect(await screen.findByText("This harness reported no models.")).toBeInTheDocument();
		expect(screen.queryByText(/Enter a model ID in New Task/)).not.toBeInTheDocument();
	});

	it("switches a Cursor model's variants together", async () => {
		render(
			<QueryClientProvider client={new QueryClient()}>
				<HarnessModelsPanel agentId="cursor" installed needsLogin={false} onOpenAccount={() => {}} />
			</QueryClientProvider>,
		);
		const opus = await screen.findByRole("switch", { name: /Claude Opus 4.8/ });
		expect(screen.getAllByRole("switch")).toHaveLength(2);
		await userEvent.click(opus);
		expect(update).toHaveBeenCalledWith({ agentId: "cursor", disabledModels: ["claude-opus-4-8-high", "claude-opus-4-8-thinking-high"] });
	});

	it("makes an endpoint model the default by saving it with the endpoint", async () => {
		h.catalog = { models: [
			{ id: "gateway-large", label: "gateway-large", isDefault: true },
			{ id: "gateway-small", label: "gateway-small" },
		] };
		const customEndpoint = { configured: true, hasApiKey: true, baseUrl: "https://gateway.example.com", authStyles: ["bearer" as const], defaultAuthStyle: "bearer" as const, supportsModel: true };
		render(
			<QueryClientProvider client={new QueryClient()}>
				<HarnessModelsPanel agentId="codex" hostId="host-a" installed needsLogin={false} onOpenAccount={() => {}} customEndpoint={customEndpoint} />
			</QueryClientProvider>,
		);
		const makeSmall = await screen.findByRole("button", { name: "Make gateway-small the default model" });
		// The current default offers no action.
		expect(screen.getAllByRole("button", { name: /the default model$/ })).toHaveLength(1);
		await userEvent.click(makeSmall);
		await waitFor(() => expect(endpoint.refresh).toHaveBeenCalledWith("codex"));
		expect(endpoint.set).toHaveBeenCalledWith("codex", { baseUrl: "https://gateway.example.com", model: "gateway-small" }, "host-a");
	});

	it("offers no Make default without a usable endpoint", async () => {
		h.catalog = { models: [{ id: "gpt-5.5", label: "GPT-5.5", isDefault: true }, { id: "gpt-5.4", label: "GPT-5.4" }] };
		render(
			<QueryClientProvider client={new QueryClient()}>
				<HarnessModelsPanel agentId="codex" installed needsLogin={false} onOpenAccount={() => {}} />
			</QueryClientProvider>,
		);
		expect(await screen.findByRole("switch", { name: /GPT-5.4/ })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /the default model$/ })).not.toBeInTheDocument();
	});
});
