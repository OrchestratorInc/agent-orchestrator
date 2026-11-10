import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { HarnessModelsPanel } from "./HarnessModelsPanel";

const update = vi.fn(async () => undefined);
const refresh = vi.fn(async (..._args: [string, string, string | undefined, boolean | undefined]) => ({ models: [{ id: "gpt-5.6", label: "GPT-5.6" }] }));
vi.mock("../../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { harnessDefaults: {} } }),
	useUpdateHarnessDefault: () => ({ update, error: undefined }),
}));
vi.mock("../../../hooks/useAgentModelsQuery", () => ({
	agentModelsQueryOptions: () => ({
		queryKey: ["agent-models", "cursor"],
		queryFn: async () => ({
			models: [
				{ id: "claude-opus-4-8-high", label: "Claude Opus 4.8 High", isDefault: true },
				{ id: "claude-opus-4-8-thinking-high", label: "Claude Opus 4.8 High Thinking" },
				{ id: "gpt-5.5-high", label: "GPT-5.5 High" },
			],
		}),
	}),
	refreshAgentModels: (agentId: string, scope: string, hostId?: string, catalogOnly?: boolean) => refresh(agentId, scope, hostId, catalogOnly),
}));

function renderPanel() {
	return render(
		<QueryClientProvider client={new QueryClient()}>
			<HarnessModelsPanel agentId="cursor" installed needsLogin={false} onOpenAccount={() => {}} />
		</QueryClientProvider>,
	);
}

describe("HarnessModelsPanel", () => {
	beforeEach(() => refresh.mockClear());

	it("switches a Cursor model's variants together", async () => {
		renderPanel();
		const opus = await screen.findByRole("switch", { name: /Claude Opus 4.8/ });
		expect(screen.getAllByRole("switch")).toHaveLength(2);
		await userEvent.click(opus);
		expect(update).toHaveBeenCalledWith({ agentId: "cursor", disabledModels: ["claude-opus-4-8-high", "claude-opus-4-8-thinking-high"] });
	});

	it("refreshes the catalog on demand and lists the new models", async () => {
		const pending = Promise.withResolvers<{ models: { id: string; label: string }[] }>();
		refresh.mockImplementationOnce(() => pending.promise);
		renderPanel();
		await screen.findByRole("switch", { name: /Claude Opus 4.8/ });
		await userEvent.click(screen.getByRole("button", { name: "Refresh models" }));
		expect(refresh).toHaveBeenCalledWith("cursor", "", undefined, true);
		// The button reports the running refresh and refuses a second one.
		const running = screen.getByRole("button", { name: "Refreshing models…" });
		expect(running).toBeDisabled();
		expect(running).toHaveAttribute("aria-busy", "true");
		pending.resolve({ models: [{ id: "gpt-5.6", label: "GPT-5.6" }] });
		expect(await screen.findByText("GPT-5.6")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Refresh models" })).toBeEnabled();
	});

	it("reports a failed refresh without dropping the listed models", async () => {
		refresh.mockRejectedValueOnce(new Error("Codex login expired"));
		renderPanel();
		await screen.findByRole("switch", { name: /Claude Opus 4.8/ });
		await userEvent.click(screen.getByRole("button", { name: "Refresh models" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Codex login expired");
		expect(screen.getAllByRole("switch")).toHaveLength(2);
		expect(screen.getByRole("button", { name: "Refresh models" })).toBeEnabled();
	});
});
