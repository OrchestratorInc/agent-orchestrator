import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import claudeCodeLogo from "../../assets/agents/claude-code.svg";
import codexLogo from "../../assets/agents/codex.svg";
import { toggleModelFavorite } from "../../lib/model-favorites";
import { useUiStore } from "../../stores/ui-store";
import { agentReadiness, type AgentReadinessSnapshot } from "../../test/agent-readiness-fixtures";
import { AgentModelPicker, type AccountChoice, type PendingAgentSwitch, type PickerSource } from "./AgentSwitchPicker";

const unavailableGet = vi.hoisted(() => vi.fn());
const readiness = vi.hoisted(() => ({ agents: [] as unknown[] }));
vi.mock("../../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: unavailableGet }) }));
vi.mock("../../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: readiness.agents } }) }));
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

function open(pending?: PendingAgentSwitch, terminal = false, harness = "claude-code", extra: Partial<PickerSource> = {}, loading = false, loadingModels = false, savedModel?: string, account?: AccountChoice) {
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
				agentSwitch={{ projectId: "p", pending, onPendingChange, account }}
			/>
		</QueryClientProvider>,
	);
	return { current, onPendingChange };
}

describe("AgentModelPicker", () => {
	beforeEach(() => {
		useUiStore.setState({ developerMode: false });
		unavailableGet.mockReset().mockResolvedValue({ data: { agents: [] }, error: undefined });
		readiness.agents = [];
		window.localStorage.clear();
	});

	it("lists the running agent's models under the agent rail, with its effort", async () => {
		const user = userEvent.setup();
		const { current } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const rail = screen.getByRole("radiogroup", { name: "Agents" });
		expect(within(rail).getByRole("radio", { name: "Claude Code" })).toHaveAttribute("aria-checked", "true");
		expect(within(rail).getByRole("radio", { name: "Codex" })).toHaveAttribute("aria-checked", "false");
		expect(screen.getByRole("menuitemradio", { name: /^Opus/ })).toHaveAttribute("aria-checked", "true");
		expect(screen.getByRole("menuitemradio", { name: /^Sonnet/ })).toHaveAttribute("aria-checked", "false");
		// The agent is the rail's; there is no Agent or Model row to open.
		expect(screen.queryByRole("menuitem", { name: /^Agent/ })).not.toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: /^Model/ })).not.toBeInTheDocument();
		await user.click(screen.getByRole("menuitem", { name: /Effort.*High/ }));
		fireEvent.click(await screen.findByRole("menuitemradio", { name: "Max" }));
		expect(current.onPickEffort).toHaveBeenCalledWith("max");
		expect(screen.queryAllByRole("menu")).toHaveLength(1);
		expect(screen.queryByRole("menuitemradio", { name: "Max" })).not.toBeInTheDocument();
	});

	it("lists Claude's newest models first and keeps the older releases below them", async () => {
		const user = userEvent.setup();
		open(undefined, false, "claude-code", {
			models: [
				{ id: "claude-opus-4-5-20251101", label: "Opus 4.5" },
				{ id: "claude-opus-5-5", label: "Opus 5.5" },
				{ id: "claude-sonnet-4-5", label: "Sonnet 4.5" },
			],
			modelId: "claude-opus-5-5",
		});
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		// The newest of each family leads, then the release it superseded: the older one costs no
		// height under the row cap, and a scroll or a search still reaches it.
		const labels = screen.getAllByRole("menuitemradio").map((row) => row.textContent);
		expect(labels).toEqual([expect.stringContaining("Opus 5.5"), expect.stringContaining("Sonnet 4.5"), expect.stringContaining("Opus 4.5")]);
	});

	it.each(["Opus", "Sonnet"])("keeps the menu open, effort under it, after picking model %s", async (model) => {
		const user = userEvent.setup();
		const { current } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		fireEvent.click(screen.getByRole("menuitemradio", { name: new RegExp(`^${model}`) }));
		if (model === "Sonnet") expect(current.onPickModel).toHaveBeenCalledWith("sonnet");
		else expect(current.onPickModel).not.toHaveBeenCalled();
		expect(screen.queryAllByRole("menu")).toHaveLength(1);
		expect(screen.getByRole("menuitem", { name: /Effort/ })).toBeInTheDocument();
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
			// One star for the favourite row at the top, one on the model row it folded into.
			expect(await screen.findAllByRole("button", { name: "Remove from favourites" })).toHaveLength(2);
		} finally {
			toggleModelFavorite(saved);
		}
	});

	it("lists only the chosen agent's favourites, without agent logos", async () => {
		const user = userEvent.setup();
		const favorites = [
			{ harness: "claude-code", row: "opus", model: "opus", label: "Opus" },
			{ harness: "cursor", row: "claude-opus-4-8", model: "claude-opus-4-8-high", label: "Opus 4.8" },
		];
		favorites.forEach(toggleModelFavorite);
		try {
			open();
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			// The favourite at the top and its model row below it.
			const rows = screen.getAllByRole("menuitemradio", { name: /^Opus/ });
			expect(rows).toHaveLength(2);
			expect(rows[0].querySelector("img")).not.toBeInTheDocument();
			expect(screen.queryByRole("menuitemradio", { name: /^Opus 4\.8/ })).not.toBeInTheDocument();
		} finally {
			act(() => favorites.forEach(toggleModelFavorite));
		}
	});

	it("shows the pending agent's favourites with their settings", async () => {
		const user = userEvent.setup();
		const saved = { harness: "codex", row: "gpt-x", model: "gpt-x", label: "GPT X", effort: "high", effortLabel: "High", fast: true };
		toggleModelFavorite(saved);
		try {
			open({ harness: "codex" });
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			expect((await screen.findAllByRole("menuitemradio", { name: /^GPT X.*Fast mode.*High/ }))).toHaveLength(1);
		} finally {
			act(() => toggleModelFavorite(saved));
		}
	});

	it("greys out a favourite whose model the agent no longer lists", async () => {
		const user = userEvent.setup();
		const gone = { harness: "codex", row: "gpt-gone", model: "gpt-gone", label: "GPT Gone" };
		toggleModelFavorite(gone);
		try {
			open({ harness: "codex" });
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			const row = await screen.findByRole("menuitemradio", { name: /GPT Gone/ });
			// Codex's list loads with only gpt-x.
			await vi.waitFor(() => expect(row).toHaveAttribute("aria-disabled", "true"));
		} finally {
			toggleModelFavorite(gone);
		}
	});

	it("holds another agent as pending when its logo is picked, keeping the menu open", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.click(screen.getByRole("radio", { name: "Codex" }));
		expect(onPendingChange).toHaveBeenCalledWith({ harness: "codex" });
		expect(screen.queryAllByRole("menu")).toHaveLength(1);
	});

	it("keeps the rail in one order whichever agent runs, so a pick never moves a logo", async () => {
		const user = userEvent.setup();
		open(undefined, false, "codex", { modelLabel: "Codex" });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const names = within(screen.getByRole("radiogroup", { name: "Agents" })).getAllByRole("radio").map((radio) => radio.getAttribute("aria-label"));
		expect(names.slice(0, 2)).toEqual(["Claude Code", "Codex"]);
		expect(screen.getByRole("radio", { name: "Codex" })).toHaveAttribute("aria-checked", "true");
	});

	it("goes on from the rail to the settings rows when the agent lists no models", async () => {
		const user = userEvent.setup();
		open(undefined, false, "claude-code", { models: [], modelId: undefined });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		act(() => screen.getByRole("radio", { name: "Claude Code" }).focus());
		await user.keyboard("{ArrowDown}");
		expect(screen.getByRole("menuitem", { name: /Effort/ })).toHaveFocus();
		await user.keyboard("{ArrowUp}");
		expect(screen.getByRole("radio", { name: "Claude Code" })).toHaveFocus();
		await user.keyboard("{ArrowDown}");
		await user.keyboard("{Shift>}{Tab}{/Shift}");
		expect(screen.getByRole("radio", { name: "Claude Code" })).toHaveFocus();
	});

	it("skips an unavailable first model when going from the rail into the list", async () => {
		const user = userEvent.setup();
		// Codex keeps its list in the order given, so the unavailable model stays first.
		open(undefined, false, "codex", {
			models: [{ id: "old", label: "Old", unavailableReason: "Needs a newer Codex" }, { id: "sonnet", label: "Sonnet" }],
			modelId: undefined,
			modelLabel: "Codex",
		});
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		act(() => screen.getByRole("radio", { name: "Codex" }).focus());
		await user.keyboard("{ArrowDown}");
		expect(screen.getByRole("menuitemradio", { name: /^Sonnet/ })).toHaveFocus();
	});

	it("goes back to the running agent when its logo is picked", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open({ harness: "codex" });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByRole("radio", { name: "Codex" })).toHaveAttribute("aria-checked", "true");
		await user.click(screen.getByRole("radio", { name: "Claude Code" }));
		expect(onPendingChange).toHaveBeenCalledWith(undefined);
	});

	it("shows the agent's logo on a chat picker and on the rail (#8129)", async () => {
		const user = userEvent.setup();
		open();
		const trigger = screen.getByRole("button", { name: /Agent, model and reasoning effort/ });
		expect(trigger.querySelector('img[aria-hidden="true"]')).toBeInTheDocument();
		await user.click(trigger);
		const codex = screen.getByRole("radio", { name: "Codex" });
		expect(codex.querySelector("img")).toHaveAttribute("src", codexLogo);
		// A logo only; its name is the tooltip and the accessible name.
		expect(codex.textContent).toBe("");
	});

	it("moves along the rail with the arrow keys, choosing as it goes, then into and out of the models", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const claude = screen.getByRole("radio", { name: "Claude Code" });
		expect(claude).toHaveAttribute("tabindex", "0");
		expect(screen.getByRole("radio", { name: "Codex" })).toHaveAttribute("tabindex", "-1");
		act(() => claude.focus());
		await user.keyboard("{ArrowRight}");
		expect(screen.getByRole("radio", { name: "Codex" })).toHaveFocus();
		expect(onPendingChange).toHaveBeenCalledWith({ harness: "codex" });
		await user.keyboard("{ArrowDown}");
		expect(screen.getByRole("menuitemradio", { name: /^Opus/ })).toHaveFocus();
		await user.keyboard("{ArrowUp}");
		expect(claude).toHaveFocus();
		await user.keyboard("{ArrowUp}");
		expect(screen.getByPlaceholderText("Search agents and models")).toHaveFocus();
		await user.keyboard("{ArrowDown}");
		expect(claude).toHaveFocus();
		await user.keyboard("{Tab}");
		expect(screen.getByRole("menuitemradio", { name: /^Opus/ })).toHaveFocus();
	});

	it("shows the pending agent's logo on the chat picker", () => {
		open({ harness: "codex" });
		const trigger = screen.getByRole("button", { name: /Agent, model and reasoning effort/ });
		expect(trigger.querySelector("img")).toHaveAttribute("src", codexLogo);
		expect(trigger.querySelector("img")).not.toHaveAttribute("src", claudeCodeLogo);
	});

	it("groups search matches under each agent's logo and name, the chosen agent first", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.type(screen.getByPlaceholderText("Search agents and models"), "o");
		await screen.findByRole("menuitemradio", { name: /^GPT X.*Codex/ });
		const row = screen.getByRole("menuitemradio", { name: /^Opus.*Claude Code/ });
		expect(row.querySelector("img")).not.toBeInTheDocument();
		const headings = screen.getAllByText(/^(Claude Code|Codex)$/);
		expect(headings.map((heading) => heading.textContent)).toEqual(["Claude Code", "Codex"]);
		expect(headings[1].querySelector("img")).toHaveAttribute("src", codexLogo);
		// Typing never changes the agent.
		expect(onPendingChange).not.toHaveBeenCalled();
	});

	it("hides the agent rail while searching, and brings it back once the search is cleared", async () => {
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const search = screen.getByPlaceholderText("Search agents and models");
		await user.type(search, "gpt");
		expect(screen.queryByRole("radiogroup", { name: "Agents" })).not.toBeInTheDocument();
		await user.clear(search);
		expect(screen.getByRole("radio", { name: "Claude Code" })).toHaveAttribute("aria-checked", "true");
	});

	it("hides a chat agent whose chat adapter is missing", async () => {
		unavailableGet.mockResolvedValue({ data: { agents: [{ agent: "pi", reason: "pi-acp is not installed" }] }, error: undefined });
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(unavailableGet).toHaveBeenCalledWith("/api/v1/agents/chat/unavailable", expect.anything());
		await vi.waitFor(() => expect(screen.queryByRole("radio", { name: /^Pi/ })).not.toBeInTheDocument());
	});

	it("offers Antigravity as a chat switch target, checking its chat adapter (#7640)", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(unavailableGet).toHaveBeenCalledWith("/api/v1/agents/chat/unavailable", {
			params: { query: { agents: expect.stringMatching(/(^|,)agy(,|$)/) } },
		});
		await user.click(within(screen.getByRole("radiogroup", { name: "Agents" })).getByRole("radio", { name: "AGY" }));
		expect(onPendingChange).toHaveBeenCalledWith({ harness: "agy" });
	});

	it("still offers Antigravity on a terminal switch", async () => {
		const user = userEvent.setup();
		open(undefined, true);
		await user.click(screen.getByRole("button", { name: "Agent running in this terminal" }));
		const names = within(screen.getByRole("radiogroup", { name: "Agents" })).getAllByRole("radio").map((radio) => radio.getAttribute("aria-label"));
		expect(names.filter((name) => name === "AGY")).toHaveLength(1);
	});

	it("hides agents that are not installed or signed in", async () => {
		readiness.agents = [
			agentReadiness("codex", "Codex", { authentication: "unauthorized" }),
			agentReadiness("cursor", "Cursor", { installation: "not_installed" }),
		] satisfies AgentReadinessSnapshot[];
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const rail = within(screen.getByRole("radiogroup", { name: "Agents" }));
		// Not there at all, under any name, rather than there without a reason.
		expect(rail.queryByRole("radio", { name: /^Codex/ })).not.toBeInTheDocument();
		expect(rail.queryByRole("radio", { name: /^Cursor/ })).not.toBeInTheDocument();
		expect(rail.getAllByRole("radio")[0]).toHaveAccessibleName("Claude Code");
		// Usable agents stay: only the unusable ones are left out.
		expect(rail.getByRole("radio", { name: "OpenCode" })).not.toHaveAttribute("aria-disabled");
	});

	it("keeps a pending agent that stops being usable on the rail, dimmed, with none of its models pickable", async () => {
		readiness.agents = [agentReadiness("codex", "Codex", { authentication: "unauthorized" })] satisfies AgentReadinessSnapshot[];
		const user = userEvent.setup();
		open({ harness: "codex" });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const codex = screen.getByRole("radio", { name: "Codex · Not signed in" });
		expect(codex).toHaveAttribute("aria-checked", "true");
		expect(codex).toHaveAttribute("aria-disabled", "true");
		expect(await screen.findByRole("menuitemradio", { name: /^GPT X/ })).toHaveAttribute("data-disabled");
	});

	it("searches every agent's models and picks one with its agent", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.type(screen.getByPlaceholderText("Search agents and models"), "gpt");
		await user.click(await screen.findByRole("menuitemradio", { name: /GPT X.*Codex/ }));
		expect(onPendingChange).toHaveBeenCalledWith(expect.objectContaining({ harness: "codex", model: "gpt-x" }));
	});

	it("picks the running agent's searched model without a switch", async () => {
		const user = userEvent.setup();
		const { current, onPendingChange } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.type(screen.getByPlaceholderText("Search agents and models"), "sonn");
		await user.click(await screen.findByRole("menuitemradio", { name: /^Sonnet/ }));
		expect(current.onPickModel).toHaveBeenCalledWith("sonnet");
		expect(onPendingChange).not.toHaveBeenCalled();
	});

	it("lists Cloud's models for the account for a Cloud task, not this computer's", async () => {
		cloudModels.mockResolvedValue({ source: "worker", cliVersion: "0.165.0", models: [{ id: "gpt-cloud", displayName: "GPT Cloud", default: true }] });
		const onPendingChange = vi.fn();
		render(
			<QueryClientProvider client={new QueryClient()}>
				<AgentModelPicker
					currentHarness="claude-code"
					current={{ models: [{ id: "opus", label: "Opus" }], modelId: "opus", modelLabel: "Opus", onPickModel: vi.fn(), efforts: [], onPickEffort: vi.fn() }}
					agentSwitch={{ projectId: "", cloudOrgId: "org-1", pending: { harness: "codex" }, onPendingChange }}
				/>
			</QueryClientProvider>,
		);
		const user = userEvent.setup();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const row = await screen.findByRole("menuitemradio", { name: /GPT Cloud/ });
		expect(screen.queryByRole("menuitemradio", { name: /GPT X/ })).not.toBeInTheDocument();
		expect(cloudModels).toHaveBeenCalledWith("org-1", "codex", expect.anything());
		await user.click(row);
		expect(onPendingChange).toHaveBeenCalledWith(expect.objectContaining({ harness: "codex", model: "gpt-cloud" }));
	});

	it("shows the pending agent's own models and effort", async () => {
		const user = userEvent.setup();
		open({ harness: "codex" });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByRole("radio", { name: "Codex" })).toHaveAttribute("aria-checked", "true");
		expect(await screen.findByRole("menuitemradio", { name: /^GPT X/ })).toBeInTheDocument();
		expect(screen.queryByRole("menuitemradio", { name: /^Opus/ })).not.toBeInTheDocument();
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

	it("keeps Home, End and the wrap among the agents, reaching the gear only past the last one", async () => {
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const radios = within(screen.getByRole("radiogroup", { name: "Agents" })).getAllByRole("radio");
		const gear = screen.getByRole("button", { name: "Manage agents…" });
		act(() => radios[0].focus());
		await user.keyboard("{End}");
		expect(radios[radios.length - 1]).toHaveFocus();
		await user.keyboard("{Home}");
		expect(radios[0]).toHaveFocus();
		await user.keyboard("{ArrowLeft}");
		expect(radios[radios.length - 1]).toHaveFocus();
		await user.keyboard("{ArrowRight}");
		expect(gear).toHaveFocus();
		await user.keyboard("{ArrowRight}");
		expect(radios[0]).toHaveFocus();
	});

	it("says why switching is refused while the agent is working", async () => {
		const user = userEvent.setup();
		const onPendingChange = vi.fn();
		render(
			<QueryClientProvider client={new QueryClient()}>
				<AgentModelPicker
					currentHarness="claude-code"
					current={{ models: [{ id: "opus", label: "Opus" }], modelId: "opus", modelLabel: "Opus", onPickModel: vi.fn(), efforts: [], onPickEffort: vi.fn() }}
					agentSwitch={{ projectId: "p", disabled: true, onPendingChange }}
				/>
			</QueryClientProvider>,
		);
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const codex = screen.getByRole("radio", { name: "Codex · The agent is working. Switch when it is idle." });
		expect(codex).toHaveAttribute("aria-disabled", "true");
		await user.click(codex);
		expect(onPendingChange).not.toHaveBeenCalled();
	});

	it("moves Tab from a terminal's rail to the search when nothing is listed below it", async () => {
		const user = userEvent.setup();
		open(undefined, true);
		await user.click(screen.getByRole("button", { name: "Agent running in this terminal" }));
		act(() => screen.getByRole("radio", { name: "Claude Code" }).focus());
		await user.keyboard("{Tab}");
		expect(screen.getByPlaceholderText("Search agents and models")).toHaveFocus();
	});

	it("marks a terminal's running agent on the rail and lists no models until another is picked", async () => {
		const user = userEvent.setup();
		open(undefined, true);
		await user.click(screen.getByRole("button", { name: "Agent running in this terminal" }));
		expect(screen.getByRole("radio", { name: "Claude Code" })).toHaveAttribute("aria-checked", "true");
		expect(screen.getByRole("radio", { name: "Codex" })).toBeInTheDocument();
		expect(screen.queryAllByRole("menuitemradio")).toHaveLength(0);
		expect(screen.getByText("Switch to another agent")).toBeInTheDocument();
	});

	it("greys out DeepSeek for a terminal and says why", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open(undefined, true);
		await user.click(screen.getByRole("button", { name: "Agent running in this terminal" }));
		const deepseek = screen.getByRole("radio", { name: /^DeepSeek · .*exits in a terminal/ });
		expect(deepseek).toHaveAttribute("aria-disabled", "true");
		fireEvent.click(deepseek);
		expect(onPendingChange).not.toHaveBeenCalled();
	});

	it("offers other agents with Developer Mode off", async () => {
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByRole("radio", { name: "Claude Code" })).toHaveAttribute("aria-checked", "true");
		expect(screen.getByRole("radio", { name: "Codex" })).toBeInTheDocument();
		expect(screen.getByRole("menuitemradio", { name: /^Opus/ })).toBeInTheDocument();
	});

	it("offers the account switch only with two or more accounts", async () => {
		const user = userEvent.setup();
		const one = { options: [{ id: "a", label: "me@work" }], value: "a", onPick: vi.fn() };
		open(undefined, false, "claude-code", {}, false, false, undefined, one);
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.queryByRole("menuitem", { name: /^Account/ })).not.toBeInTheDocument();
	});

	it("shows the account switch with two accounts", async () => {
		const user = userEvent.setup();
		const two = { options: [{ id: "a", label: "me@work" }, { id: "b", label: "me@home" }], value: "a", onPick: vi.fn() };
		open(undefined, false, "claude-code", {}, false, false, undefined, two);
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByRole("menuitem", { name: /^Account.*me@work/ })).toBeInTheDocument();
	});
});
