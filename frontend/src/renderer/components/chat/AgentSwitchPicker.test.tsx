import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import claudeCodeLogo from "../../assets/agents/claude-code.svg";
import codexLogo from "../../assets/agents/codex.svg";
import { toggleModelFavorite } from "../../lib/model-favorites";
import { useUiStore } from "../../stores/ui-store";
import { AgentModelPicker, type PendingAgentSwitch, type PickerSource } from "./AgentSwitchPicker";

const unavailableGet = vi.hoisted(() => vi.fn());
vi.mock("../../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: unavailableGet }) }));
vi.mock("../../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: [] } }) }));
vi.mock("../../hooks/useSettings", () => ({ useSettings: () => ({ settings: undefined }) }));
const cloudModels = vi.hoisted(() => vi.fn());
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, baseUrl: "https://cp.test", userId: "user-1", client: { listUserAgentModels: cloudModels } }),
}));
vi.mock("../../hooks/useProviderConnections", () => ({ useProviderConnections: () => ({ data: [], isError: false }) }));
vi.mock("../../hooks/useAgentModelsQuery", () => ({
	agentModelsQueryOptions: (agent: string) => ({
		queryKey: ["agent-models", agent],
		queryFn: async () => agent === "droid" ? {
			models: [], refreshState: "error",
			refreshError: "droid failed to start: Cannot find module 'tree-sitter'\n    at Module._resolveFilename",
		} : ({
			models: agent === "codex"
				? [{ id: "gpt-x", label: "GPT X", isDefault: true, efforts: ["low", "high"], defaultEffort: "high" }]
				: agent === "cursor"
					? ["claude-opus-4-8-high", "claude-opus-4-8-thinking-high"].map((id) => ({ id, label: id }))
					: [],
		}),
	}),
}));

function open(pending?: PendingAgentSwitch, terminal = false, harness = "claude-code", extra: Partial<PickerSource> = {}, loading = false, loadingModels = false, savedModel?: string) {
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
				savedModel={savedModel}
				agentSwitch={{ projectId: "p", pending, onPendingChange }}
			/>
		</QueryClientProvider>,
	);
	return { current, onPendingChange };
}

describe("AgentModelPicker", () => {
	beforeEach(() => {
		useUiStore.setState({ developerMode: false });
		unavailableGet.mockReset().mockResolvedValue({ data: { agents: [] }, error: undefined });
		window.localStorage.clear();
	});

	it("is one menu of Agent, Model and Effort rows for the running agent", async () => {
		const user = userEvent.setup();
		const { current } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByRole("menuitem", { name: /Agent.*Claude Code/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /Model.*Opus/ })).toBeInTheDocument();
		await user.click(screen.getByRole("menuitem", { name: /Effort.*High/ }));
		fireEvent.click(await screen.findByRole("menuitemradio", { name: "Max" }));
		expect(current.onPickEffort).toHaveBeenCalledWith("max");
		expect(screen.queryAllByRole("menu")).toHaveLength(1);
		expect(screen.queryByRole("menuitemradio", { name: "Max" })).not.toBeInTheDocument();
	});

	it.each(["Opus", "Sonnet"])("closes only the submenu after selecting model %s", async (model) => {
		const user = userEvent.setup();
		const { current } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.click(screen.getByRole("menuitem", { name: /Model.*Opus/ }));
		fireEvent.click(await screen.findByRole("menuitemradio", { name: new RegExp(`^${model}`) }));
		if (model === "Sonnet") expect(current.onPickModel).toHaveBeenCalledWith("sonnet");
		else expect(current.onPickModel).not.toHaveBeenCalled();
		expect(screen.queryAllByRole("menu")).toHaveLength(1);
		expect(screen.queryByRole("menuitemradio", { name: /^Sonnet/ })).not.toBeInTheDocument();
	});

	it.each(["Fast mode", "Thinking"])("keeps the parent menu open after toggling %s", async (label) => {
		const user = userEvent.setup();
		const onToggle = vi.fn();
		open(undefined, false, "cursor", { fast: false, thinking: false, onToggleFast: onToggle, onToggleThinking: onToggle });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.click(screen.getByRole("switch", { name: label }));
		expect(onToggle).toHaveBeenCalledExactlyOnceWith(true);
		expect(screen.queryAllByRole("menu")).toHaveLength(1);
	});

	it("keeps a running Cursor chat's own effort and thinking", async () => {
		const user = userEvent.setup();
		open(undefined, false, "cursor", { thinking: true, onToggleThinking: vi.fn() });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByRole("menuitem", { name: /Effort.*High/ })).toBeInTheDocument();
		expect(screen.getByText("Thinking")).toBeInTheDocument();
	});

	it("keeps the settings in place while the agent answers a change", async () => {
		const user = userEvent.setup();
		open(undefined, false, "cursor", { thinking: true, onToggleThinking: vi.fn() }, true);
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.queryByText("Loading settings")).not.toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /Effort/ })).toBeInTheDocument();
		expect(screen.getByText("Thinking")).toBeInTheDocument();
	});

	it("shows the settings as loading while the agent has none to show yet", async () => {
		const user = userEvent.setup();
		open(undefined, false, "codex", { efforts: [] }, true);
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByText("Loading settings")).toBeInTheDocument();
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

	it("names the saved model instead of loading when the agent's list knows it", async () => {
		open(undefined, false, "codex", { modelLabel: "Codex" }, false, true, "gpt-x");
		const trigger = screen.getByRole("button", { name: /Agent, model and reasoning effort/ });
		expect(await screen.findByText("GPT X")).toBeInTheDocument();
		expect(trigger).not.toHaveTextContent("Loading models…");
	});

	it("names the agent's default model while loading a chat that saved none", async () => {
		open(undefined, false, "codex", { modelLabel: "Codex" }, false, true);
		expect(await screen.findByText("GPT X")).toBeInTheDocument();
	});

	it("lists the agent's models while it starts, checked on the saved one, until it reports its own", async () => {
		const user = userEvent.setup();
		const { current } = open(undefined, false, "codex", { models: [], modelId: undefined, modelLabel: "Codex" }, false, true, "gpt-x");
		await screen.findByText("GPT X");
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.click(screen.getByRole("menuitem", { name: /Model/ }));
		const row = await screen.findByRole("menuitemradio", { name: /GPT X/ });
		expect(row).toHaveAttribute("aria-checked", "true");
		expect(row).toHaveAttribute("aria-disabled", "true");
		expect(current.onPickModel).not.toHaveBeenCalled();
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

	it("names each favourite's agent when the same model is saved for different agents", async () => {
		const user = userEvent.setup();
		const favorites = [
			{ harness: "claude-code", row: "opus", model: "opus", label: "Opus" },
			{ harness: "cursor", row: "claude-opus-4-8", model: "claude-opus-4-8-high", label: "Opus" },
		];
		favorites.forEach(toggleModelFavorite);
		try {
			open();
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			expect(screen.getByRole("menuitemradio", { name: "Opus, Claude Code" })).toBeInTheDocument();
			expect(screen.getByRole("menuitemradio", { name: "Opus, Cursor" })).toBeInTheDocument();
		} finally {
			act(() => favorites.forEach(toggleModelFavorite));
		}
	});

	it("shows a favourite's logo without its agent's name, as before #7822", async () => {
		const user = userEvent.setup();
		const saved = { harness: "codex", row: "gpt-x", model: "gpt-x", label: "GPT X" };
		toggleModelFavorite(saved);
		try {
			open();
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			const row = await screen.findByRole("menuitemradio", { name: "GPT X, Codex" });
			expect(row.querySelector("img")).toBeInTheDocument();
			expect(within(row).queryByText("Codex")).not.toBeInTheDocument();
		} finally {
			act(() => toggleModelFavorite(saved));
		}
	});

	it("draws the agent's name on a lone favourite whose logo another agent shares", async () => {
		const user = userEvent.setup();
		const saved = { harness: "opencode-v2", row: "gpt-x", model: "gpt-x", label: "GPT X" };
		toggleModelFavorite(saved);
		try {
			open();
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			const row = await screen.findByRole("menuitemradio", { name: "GPT X, OpenCode 2" });
			expect(row.querySelector("img")).toBeInTheDocument();
			expect(within(row).getByText("OpenCode 2")).toHaveAttribute("aria-hidden", "true");
		} finally {
			act(() => toggleModelFavorite(saved));
		}
	});

	it("draws the agent's name where two agents' favourites share a logo and a model name", async () => {
		const user = userEvent.setup();
		const favorites = [
			{ harness: "opencode", row: "gpt-x", model: "gpt-x", label: "GPT X" },
			{ harness: "opencode-v2", row: "gpt-x", model: "gpt-x", label: "GPT X" },
		];
		favorites.forEach(toggleModelFavorite);
		try {
			open();
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			const first = await screen.findByRole("menuitemradio", { name: "GPT X, OpenCode" });
			const second = screen.getByRole("menuitemradio", { name: "GPT X, OpenCode 2" });
			expect(within(first).getByText("OpenCode")).toHaveAttribute("aria-hidden", "true");
			expect(within(second).getByText("OpenCode 2")).toHaveAttribute("aria-hidden", "true");
		} finally {
			act(() => favorites.forEach(toggleModelFavorite));
		}
	});

	it("reads a favourite's agent after its model's settings", async () => {
		const user = userEvent.setup();
		const saved = { harness: "codex", row: "gpt-x", model: "gpt-x", label: "GPT X", effort: "high", effortLabel: "High", fast: true };
		toggleModelFavorite(saved);
		try {
			open();
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			expect(await screen.findByRole("menuitemradio", { name: /^GPT X.*Fast mode.*High, Codex$/ })).toBeInTheDocument();
		} finally {
			act(() => toggleModelFavorite(saved));
		}
	});

	it("greys out a favourite whose model the agent no longer lists", async () => {
		const user = userEvent.setup();
		const gone = { harness: "codex", row: "gpt-gone", model: "gpt-gone", label: "GPT Gone" };
		toggleModelFavorite(gone);
		try {
			open();
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			const row = await screen.findByRole("menuitemradio", { name: /GPT Gone/ });
			// Codex's list loads with only gpt-x.
			await vi.waitFor(() => expect(row).toHaveAttribute("aria-disabled", "true"));
		} finally {
			toggleModelFavorite(gone);
		}
	});

	it("holds another agent as pending when it is picked", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.click(screen.getByRole("menuitem", { name: /Agent/ }));
		fireEvent.click(await screen.findByRole("menuitemradio", { name: "Codex" }));
		expect(onPendingChange).toHaveBeenCalledWith({ harness: "codex" });
		expect(screen.queryAllByRole("menu")).toHaveLength(1);
	});

	it("shows the agent's logo on a chat picker and its agent rows (#8129)", async () => {
		const user = userEvent.setup();
		open();
		const trigger = screen.getByRole("button", { name: /Agent, model and reasoning effort/ });
		expect(trigger.querySelector('img[aria-hidden="true"]')).toBeInTheDocument();
		await user.click(trigger);
		await user.click(screen.getByRole("menuitem", { name: /Agent/ }));
		const codex = await screen.findByRole("menuitemradio", { name: "Codex" });
		expect(codex.querySelector('img[aria-hidden="true"]')).toBeInTheDocument();
	});

	it("shows the pending agent's logo on the chat picker", () => {
		open({ harness: "codex" });
		const trigger = screen.getByRole("button", { name: /Agent, model and reasoning effort/ });
		expect(trigger.querySelector("img")).toHaveAttribute("src", codexLogo);
		expect(trigger.querySelector("img")).not.toHaveAttribute("src", claudeCodeLogo);
	});

	it("shows each agent's logo on chat search rows", async () => {
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.type(screen.getByPlaceholderText("Search agents and models"), "gpt");
		const row = await screen.findByRole("menuitemradio", { name: /GPT X.*Codex/ });
		expect(row.querySelector("img")).toHaveAttribute("src", codexLogo);
	});

	it("leaves out a chat agent whose chat adapter is missing", async () => {
		unavailableGet.mockResolvedValue({ data: { agents: [{ agent: "pi", reason: "pi-acp is not installed" }] }, error: undefined });
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.click(screen.getByRole("menuitem", { name: /Agent/ }));
		expect(await screen.findByRole("menuitemradio", { name: "Codex" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitemradio", { name: "Pi" })).not.toBeInTheDocument();
		expect(unavailableGet).toHaveBeenCalledWith("/api/v1/agents/chat/unavailable", expect.anything());
	});

	it("searches every agent's models and picks one with its agent", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.type(screen.getByPlaceholderText("Search agents and models"), "gpt");
		await user.click(await screen.findByRole("menuitemradio", { name: /GPT X.*Codex/ }));
		expect(onPendingChange).toHaveBeenCalledWith(expect.objectContaining({ harness: "codex", model: "gpt-x" }));
	});

	it("lists Cloud's models for the account for a Cloud task, not this computer's", async () => {
		cloudModels.mockResolvedValue({ source: "worker", cliVersion: "0.165.0", models: [{ id: "gpt-cloud", displayName: "GPT Cloud", default: true }] });
		const onPendingChange = vi.fn();
		render(
			<QueryClientProvider client={new QueryClient()}>
				<AgentModelPicker
					currentHarness="claude-code"
					current={{ models: [{ id: "opus", label: "Opus" }], modelId: "opus", modelLabel: "Opus", onPickModel: vi.fn(), efforts: [], onPickEffort: vi.fn() }}
					agentSwitch={{ projectId: "", cloudOrgId: "org-1", onPendingChange }}
				/>
			</QueryClientProvider>,
		);
		const user = userEvent.setup();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.type(screen.getByPlaceholderText("Search agents and models"), "gpt");
		await user.click(await screen.findByRole("menuitemradio", { name: /GPT Cloud.*Codex/ }));
		expect(screen.queryByRole("menuitemradio", { name: /GPT X/ })).not.toBeInTheDocument();
		expect(cloudModels).toHaveBeenCalledWith("org-1", "codex", expect.anything());
		expect(onPendingChange).toHaveBeenCalledWith(expect.objectContaining({ harness: "codex", model: "gpt-cloud" }));
	});

	it("shows the pending agent's own model and effort rows", async () => {
		const user = userEvent.setup();
		open({ harness: "codex" });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(await screen.findByRole("menuitem", { name: /Model.*GPT X/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /Effort.*High/ })).toBeInTheDocument();
	});

	it("says why the pending agent has no models", async () => {
		const user = userEvent.setup();
		open({ harness: "droid" });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(await screen.findByText("Couldn't load Droid's models: droid failed to start: Cannot find module 'tree-sitter'")).toBeInTheDocument();
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

	it("greys out DeepSeek for a terminal and says why on hover", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open(undefined, true);
		await user.click(screen.getByRole("button", { name: "Agent running in this terminal" }));
		await user.click(screen.getByRole("menuitem", { name: /Agent/ }));
		const deepseek = await screen.findByRole("menuitemradio", { name: "DeepSeek" });
		expect(deepseek).toHaveAttribute("aria-disabled", "true");
		expect(deepseek).toHaveAttribute("title", expect.stringContaining("exits in a terminal"));
		fireEvent.click(deepseek);
		expect(onPendingChange).not.toHaveBeenCalled();
	});

	it("offers other agents with Developer Mode off", async () => {
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByRole("menuitem", { name: /Agent.*Claude Code/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /Model.*Opus/ })).toBeInTheDocument();
	});
});
