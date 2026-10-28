import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { HarnessModelsPanel } from "./HarnessModelsPanel";

const update = vi.fn(async () => undefined);
vi.mock("../../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { harnessDefaults: {} } }),
	useUpdateHarnessDefault: () => ({ update, error: undefined }),
}));
vi.mock("../../../hooks/useAgentModelsQuery", () => ({
	agentModelsQueryOptions: (agentId: string) => ({
		queryKey: ["agent-models", agentId],
		queryFn: async () => agentId === "strands" ? ({ selectionMode: "text", models: [], allowCustom: true, customModelEntry: "direct" }) : ({
			models: [
				{ id: "claude-opus-4-8-high", label: "Claude Opus 4.8 High", isDefault: true },
				{ id: "claude-opus-4-8-thinking-high", label: "Claude Opus 4.8 High Thinking" },
				{ id: "gpt-5.5-high", label: "GPT-5.5 High" },
			],
		}),
	}),
}));

describe("HarnessModelsPanel", () => {
	it.each([false, true])("explains free-form model selection when login is required: %s", async (needsLogin) => {
		const onOpenAccount = vi.fn();
		render(
			<QueryClientProvider client={new QueryClient()}>
				<HarnessModelsPanel agentId="strands" installed needsLogin={needsLogin} onOpenAccount={onOpenAccount} />
			</QueryClientProvider>,
		);
		expect(await screen.findByText(/Strands accepts model IDs/)).toBeInTheDocument();
		expect(screen.queryByText("This harness reported no models.")).toBeNull();
		expect(screen.queryByRole("list", { name: "Available models" })).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Account" }));
		expect(onOpenAccount).toHaveBeenCalledOnce();
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
