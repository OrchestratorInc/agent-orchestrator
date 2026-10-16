import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toggleModelFavorite } from "../../lib/model-favorites";
import { AgentModelPicker, type PendingAgentSwitch, type PickerSource } from "./AgentSwitchPicker";

vi.mock("../../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: [] } }) }));
vi.mock("../../hooks/useAgentModelsQuery", () => ({
	agentModelsQueryOptions: (agent: string) => ({
		queryKey: ["agent-models", agent],
		queryFn: async () => ({
			models: agent === "codex"
				? [{ id: "gpt-x", label: "GPT X", isDefault: true, efforts: ["low", "high"], defaultEffort: "high" }]
				: agent === "cursor"
					? ["claude-opus-4-8-high", "claude-opus-4-8-thinking-high"].map((id) => ({ id, label: id }))
					: [],
		}),
	}),
}));

function open(pending?: PendingAgentSwitch, terminal = false, harness = "claude-code", extra: Partial<PickerSource> = {}, loading = false, loadingModels = false) {
	const onPendingChange = vi.fn();
	const current: PickerSource = {
		models: [{ id: "opus", label: "Opus" }, { id: "sonnet", label: "Sonnet" }],
		modelId: "opus",
		modelLabel: "Opus",
		onPickModel: vi.fn(),
		efforts: [{ value: "high", label: "High" }, { value: "max", label: "Max" }],
		effort: "high",
		onPickEffort: vi.fn(),
		...extra,
	};
	render(
		<QueryClientProvider client={new QueryClient()}>
			<AgentModelPicker
				currentHarness={harness}
				current={current}
				terminal={terminal}
				loading={loading}
				loadingModels={loadingModels}
				agentSwitch={{ projectId: "p", pending, onPendingChange }}
			/>
		</QueryClientProvider>,
	);
	return { current, onPendingChange };
}

describe("AgentModelPicker", () => {
	beforeEach(() => window.localStorage.clear());

	it("is one menu of Agent, Model and Effort rows for the running agent", async () => {
		const user = userEvent.setup();
		const { current } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByRole("menuitem", { name: /Agent.*Claude Code/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /Model.*Opus/ })).toBeInTheDocument();
		await user.click(screen.getByRole("menuitem", { name: /Effort.*High/ }));
		fireEvent.click(await screen.findByRole("menuitemradio", { name: "Max" }));
		expect(current.onPickEffort).toHaveBeenCalledWith("max");
	});

	it("keeps a running Cursor chat's own effort and thinking", async () => {
		const user = userEvent.setup();
		open(undefined, false, "cursor", { thinking: true, onToggleThinking: vi.fn() });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByRole("menuitem", { name: /Effort.*High/ })).toBeInTheDocument();
		expect(screen.getByText("Thinking")).toBeInTheDocument();
	});

	it("shows the settings as loading while the agent answers a change", async () => {
		const user = userEvent.setup();
		open(undefined, false, "cursor", { thinking: true, onToggleThinking: vi.fn() }, true);
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByText("Loading settings")).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: /Effort/ })).not.toBeInTheDocument();
		expect(screen.queryByText("Thinking")).not.toBeInTheDocument();
	});

	it("offers Claude Code's auto-compact window on a chat switch to it", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open({ harness: "claude-code", model: "opus" }, false, "codex");
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.click(screen.getByRole("radio", { name: "1M" }));
		expect(onPendingChange).toHaveBeenCalledWith(expect.objectContaining({ harness: "claude-code", autoCompactWindow: "1m" }));
	});

	it("names no model while the agent starts again", () => {
		open(undefined, false, "claude-code", {}, false, true);
		expect(screen.getByRole("button", { name: /Agent, model and reasoning effort/ })).toHaveTextContent("Loading models…");
	});

	it("stars a row when its model was starred under another id", async () => {
		const user = userEvent.setup();
		const saved = { harness: "claude-code", row: "opus", model: "opus", label: "Opus" };
		toggleModelFavorite(saved);
		try {
			open(undefined, false, "claude-code", { models: [{ id: "claude-opus-5-5", label: "Opus", aliases: ["opus"] }], modelId: "claude-opus-5-5" });
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			await user.click(screen.getByRole("menuitem", { name: /Model/ }));
			// One star for the favourite row at the top, one on the model row it folded into.
			expect(await screen.findAllByRole("button", { name: "Remove from favourites" })).toHaveLength(2);
		} finally {
			toggleModelFavorite(saved);
		}
	});

	it("holds another agent as pending when it is picked", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.click(screen.getByRole("menuitem", { name: /Agent/ }));
		fireEvent.click(await screen.findByRole("menuitemradio", { name: "Codex" }));
		expect(onPendingChange).toHaveBeenCalledWith({ harness: "codex" });
	});

	it("searches every agent's models and picks one with its agent", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.type(screen.getByPlaceholderText("Search agents and models"), "gpt");
		await user.click(await screen.findByRole("menuitemradio", { name: /GPT X.*Codex/ }));
		expect(onPendingChange).toHaveBeenCalledWith(expect.objectContaining({ harness: "codex", model: "gpt-x" }));
	});

	it("shows the pending agent's own model and effort rows", async () => {
		const user = userEvent.setup();
		open({ harness: "codex" });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(await screen.findByRole("menuitem", { name: /Model.*GPT X/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /Effort.*High/ })).toBeInTheDocument();
	});

	it("offers Cursor's thinking when a terminal switches to it", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open({ harness: "cursor", model: "claude-opus-4-8-high" }, true);
		await user.click(screen.getByRole("button", { name: "Agent running in this terminal" }));
		await user.click(await screen.findByText("Thinking"));
		expect(onPendingChange).toHaveBeenCalledWith(expect.objectContaining({ harness: "cursor", model: "claude-opus-4-8-thinking-high" }));
	});

	it("offers only the agent row for a terminal's running agent", async () => {
		const user = userEvent.setup();
		open(undefined, true);
		await user.click(screen.getByRole("button", { name: "Agent running in this terminal" }));
		expect(screen.getByRole("menuitem", { name: /Agent/ })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: /Model/ })).not.toBeInTheDocument();
		await user.click(screen.getByRole("menuitem", { name: /Agent/ }));
		expect(await screen.findByRole("menuitemradio", { name: "Codex" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitemradio", { name: "Claude Code" })).not.toBeInTheDocument();
	});
});
