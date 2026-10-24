import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
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
	it.each(["direct", undefined] as const)("explains model-ID entry when the native catalog is freeform (%s)", async (customModelEntry) => {
		h.catalog = { models: [], allowCustom: true, customModelEntry };
		render(
			<QueryClientProvider client={new QueryClient()}>
				<HarnessModelsPanel agentId="codex" installed needsLogin={false} onOpenAccount={() => {}} />
			</QueryClientProvider>,
		);
		expect(await screen.findByText(/Enter a model ID in New Task/)).toBeInTheDocument();
		expect(screen.queryByText("This harness reported no models.")).not.toBeInTheDocument();
	});

	it.each(["configured", "none"] as const)("does not advertise direct entry for a %s catalog", async (customModelEntry) => {
		h.catalog = { models: [], allowCustom: true, customModelEntry };
		render(
			<QueryClientProvider client={new QueryClient()}>
				<HarnessModelsPanel agentId="codex" installed needsLogin={false} onOpenAccount={() => {}} />
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
});
