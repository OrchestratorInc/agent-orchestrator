import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { HarnessModelsPanel } from "./HarnessModelsPanel";

const update = vi.fn(async () => undefined);
vi.mock("../../../hooks/useSettings", () => ({
	useSettings: () => ({ settings: { harnessDefaults: {} } }),
	useUpdateHarnessDefault: () => ({ update, error: undefined }),
}));
vi.mock("../../../hooks/useAgentModelsQuery", () => ({
	agentModelsQueryOptions: () => ({
		queryKey: ["agent-models", "omp"],
		queryFn: async () => ({
			models: [
				{ id: "codegpt/DeepSeek-V4.1-Flash", label: "DeepSeek V4.1 Flash", provider: "codegpt" },
				...Array.from({ length: 9 }, (_, i) => ({ id: `codegpt/unique-${i}`, label: `Unique ${i}`, provider: "codegpt" })),
				{ id: "codegpt/cn:deepseek-v4.1-flash", label: "DeepSeek V4.1 Flash", provider: "codegpt" },
			],
		}),
	}),
}));

it("设置页跨分页和搜索保留重名模型 ID，开关仅修改所选 ID", async () => {
	render(<QueryClientProvider client={new QueryClient()}>
		<HarnessModelsPanel agentId="omp" installed needsLogin={false} onOpenAccount={() => {}} />
	</QueryClientProvider>);
	expect(await screen.findByText("codegpt/DeepSeek-V4.1-Flash")).toBeInTheDocument();
	expect(screen.queryByText("codegpt/unique-0")).not.toBeInTheDocument();

	await userEvent.type(screen.getByRole("searchbox"), "cn:deepseek");
	expect(screen.getByText("codegpt/cn:deepseek-v4.1-flash")).toBeInTheDocument();
	await userEvent.click(screen.getByRole("switch", { name: /codegpt\/cn:deepseek-v4\.1-flash/ }));
	expect(update).toHaveBeenCalledExactlyOnceWith({ agentId: "omp", disabledModels: ["codegpt/cn:deepseek-v4.1-flash"] });
});
