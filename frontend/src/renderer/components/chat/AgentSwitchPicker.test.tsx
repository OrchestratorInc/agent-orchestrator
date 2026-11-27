import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import claudeCodeLogo from "../../assets/agents/claude-code.svg";
import codexLogo from "../../assets/agents/codex.svg";
import { forgetLastModels, rememberLastModel } from "../../lib/last-models";
import { useUiStore } from "../../stores/ui-store";
import { agentReadiness, type AgentReadinessSnapshot } from "../../test/agent-readiness-fixtures";
import { AgentModelPicker, autoCompactKind, type AccountChoice, type PendingAgentSwitch, type PickerSource } from "./AgentSwitchPicker";

const unavailableGet = vi.hoisted(() => vi.fn());
const readiness = vi.hoisted(() => ({ agents: [] as unknown[] }));
vi.mock("../../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: unavailableGet }) }));
vi.mock("../../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: readiness.agents } }) }));
const hostSettings = vi.hoisted(() => ({ value: undefined as { chatHarnesses: string[] } | undefined }));
vi.mock("../../hooks/useSettings", () => ({ useSettings: () => ({ settings: hostSettings.value }) }));
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
				? [{ id: "gpt-x", label: "GPT X", isDefault: true, efforts: ["low", "high"], defaultEffort: "high" }, { id: "gpt-y", label: "GPT Y", efforts: ["low", "high"], defaultEffort: "high" }]
				: agent === "cursor"
					? ["claude-opus-4-8-high", "claude-opus-4-8-thinking-high"].map((id) => ({ id, label: id }))
					: agent === "opencode"
						? [
							{ id: "fireworks/accounts/fireworks/models/kimi", label: "kimi" },
							{ id: "fireworks/accounts/team/models/kimi", label: "team/kimi" },
						]
						: agent === "claude-code"
						? [{ id: "opus", label: "Opus", isDefault: true, efforts: ["high", "xhigh", "max"], defaultEffort: "high" }]
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
		hostSettings.value = undefined;
		window.localStorage.clear();
		forgetLastModels();
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
		// The effort is a row that opens its levels to the side; picking one closes only that side menu.
		await user.click(screen.getByRole("menuitem", { name: /Effort.*High/ }));
		fireEvent.click(await screen.findByRole("menuitemradio", { name: "Max" }));
		expect(current.onPickEffort).toHaveBeenCalledExactlyOnceWith("max");
		expect(screen.queryAllByRole("menu")).toHaveLength(1);
		expect(screen.queryByRole("menuitemradio", { name: "Max" })).not.toBeInTheDocument();
	});

	// A routed catalog offers one model through several providers. The New task
	// composer's menu lists the running agent's models, browsed and searched.
	it("names the provider of the running agent's models that share a name", async () => {
		const user = userEvent.setup();
		open(undefined, false, "crush", {
			models: [
				{ id: "deepseek/deepseek-v4-pro", label: "deepseek-v4-pro" },
				{ id: "fireworks/accounts/fireworks/models/deepseek-v4-pro", label: "deepseek-v4-pro" },
				{ id: "venice/only-here", label: "only-here" },
			],
			modelId: "deepseek/deepseek-v4-pro",
			modelLabel: "deepseek-v4-pro",
		});
		const button = screen.getByRole("button", { name: /Agent, model and reasoning effort/ });
		expect(button).toHaveTextContent("deepseek-v4-pro (deepseek)");
		await user.click(button);
		const names = () => screen.getAllByRole("menuitemradio").map((row) => row.textContent);
		expect(names()).toEqual([
			expect.stringContaining("deepseek-v4-pro (deepseek)"),
			expect.stringContaining("deepseek-v4-pro (fireworks)"),
			expect.stringMatching(/^only-here(?! \()/),
		]);
		await user.type(screen.getByPlaceholderText("Search agents and models"), "deepseek-v4-pro");
		expect(names()).toEqual([
			expect.stringContaining("deepseek-v4-pro (deepseek)"),
			expect.stringContaining("deepseek-v4-pro (fireworks)"),
		]);
	});

	it("shows the full ID where models that share a name share a provider too", async () => {
		const user = userEvent.setup();
		open(undefined, false, "crush", {
			models: [
				{ id: "fireworks/kimi", label: "kimi" },
				{ id: "fireworks/accounts/fireworks/models/kimi", label: "kimi" },
			],
			modelId: "fireworks/kimi",
			modelLabel: "kimi",
		});
		const button = screen.getByRole("button", { name: /Agent, model and reasoning effort/ });
		expect(button).toHaveTextContent("fireworks/kimi");
		await user.click(button);
		expect(screen.getAllByRole("menuitemradio").map((row) => row.textContent)).toEqual([
			expect.stringContaining("fireworks/kimi"),
			expect.stringContaining("fireworks/accounts/fireworks/models/kimi"),
		]);
	});

	// Another agent's names are cut to their last part, so an account's
	// deployment reads the same as the public model it is named after.
	it("tells another agent's deployment apart from the public model", async () => {
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.type(screen.getByPlaceholderText("Search agents and models"), "kimi");
		expect(await screen.findByText("fireworks/accounts/team/models/kimi")).toBeInTheDocument();
		expect(screen.getByText("fireworks/accounts/fireworks/models/kimi")).toBeInTheDocument();
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

	it.each(["Opus", "Sonnet"])("closes the menu after picking model %s", async (model) => {
		const user = userEvent.setup();
		const { current } = open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.click(screen.getByRole("menuitemradio", { name: new RegExp(`^${model}`) }));
		if (model === "Sonnet") expect(current.onPickModel).toHaveBeenCalledWith("sonnet");
		else expect(current.onPickModel).not.toHaveBeenCalled();
		await waitFor(() => expect(screen.queryAllByRole("menu")).toHaveLength(0));
	});

	it("keeps a terminal's menu open after a model is picked for the switch, for its Switch button", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open({ harness: "codex" }, true);
		await user.click(screen.getByRole("button", { name: "Agent running in this terminal" }));
		await user.click(await screen.findByRole("menuitemradio", { name: /^GPT Y/ }));
		expect(onPendingChange).toHaveBeenCalledWith(expect.objectContaining({ harness: "codex", model: "gpt-y" }));
		expect(screen.queryAllByRole("menu")).toHaveLength(1);
	});

	it.each([["menuitemcheckbox", "Fast mode"], ["switch", "Thinking"]] as const)("keeps the parent menu open after toggling a %s named %s", async (role, label) => {
		const user = userEvent.setup();
		const onToggle = vi.fn();
		open(undefined, false, "cursor", { fast: false, thinking: false, onToggleFast: onToggle, onToggleThinking: onToggle });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await user.click(screen.getByRole(role, { name: label }));
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

	it("lists the chat's model first", async () => {
		const user = userEvent.setup();
		open(undefined, false, "claude-code", { modelId: "sonnet", modelLabel: "Sonnet" });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const labels = screen.getAllByRole("menuitemradio").map((row) => row.textContent);
		expect(labels).toEqual([expect.stringContaining("Sonnet"), expect.stringContaining("Opus")]);
	});

	it("has no favourite stars", async () => {
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.queryByRole("button", { name: /favourites/i })).not.toBeInTheDocument();
	});

	it("remembers a picked model for that agent in every chat", async () => {
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		fireEvent.click(screen.getByRole("menuitemradio", { name: /^Sonnet/ }));
		expect(JSON.parse(window.localStorage.getItem("ao.chat.lastModels") ?? "{}")).toEqual({ "claude-code": "sonnet" });
	});

	it("lists the model last picked for another agent first when it is picked on the rail", async () => {
		const user = userEvent.setup();
		rememberLastModel("codex", "gpt-y");
		open({ harness: "codex" });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		await screen.findByRole("menuitemradio", { name: /^GPT Y/ });
		expect(screen.getAllByRole("menuitemradio").map((row) => row.textContent)).toEqual([expect.stringContaining("GPT Y"), expect.stringContaining("GPT X")]);
	});

	it("puts the first model above the group headings and keeps the others' headings", async () => {
		const user = userEvent.setup();
		open(undefined, false, "opencode-v2", {
			models: [{ id: "a/one", label: "One", group: "Alpha" }, { id: "a/two", label: "Two", group: "Alpha" }, { id: "b/three", label: "Three", group: "Beta" }],
			modelId: "b/three",
			modelLabel: "Three",
		});
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const menu = screen.getAllByRole("menu")[0];
		// Model rows and group headings, in the order they are drawn.
		const order = [...menu.querySelectorAll("*")]
			.filter((node) => node.getAttribute("role") === "menuitemradio" || (node.children.length === 0 && ["Alpha", "Beta"].includes(node.textContent ?? "")))
			.map((node) => node.textContent?.trim().split(/\s/)[0]);
		expect(order).toEqual(["Three", "Alpha", "One", "Two"]);
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

	it("offers every agent the host has a Chat driver for, and checks each one's chat adapter", async () => {
		hostSettings.value = { chatHarnesses: ["claude-code", "codex", "grok", "agy", "hermes"] };
		unavailableGet.mockResolvedValue({ data: { agents: [{ agent: "hermes", reason: "hermes-acp is not installed" }] }, error: undefined });
		const user = userEvent.setup();
		open();
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(unavailableGet).toHaveBeenCalledWith("/api/v1/agents/chat/unavailable", { params: { query: { agents: "claude-code,codex,agy,grok,hermes" } } });
		const rail = within(screen.getByRole("radiogroup", { name: "Agents" }));
		await vi.waitFor(() => expect(rail.queryByRole("radio", { name: /^Hermes/ })).not.toBeInTheDocument());
		expect(rail.getAllByRole("radio").map((radio) => radio.getAttribute("aria-label") ?? radio.textContent))
			.toEqual(["Claude Code", "Codex", "AGY", "Grok"]);
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
		fireEvent.click(row);
		expect(onPendingChange).toHaveBeenCalledWith(expect.objectContaining({ harness: "codex", model: "gpt-cloud" }));
	});

	it("shows the pending agent's own models and effort", async () => {
		const user = userEvent.setup();
		open({ harness: "codex" });
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.getByRole("radio", { name: "Codex" })).toHaveAttribute("aria-checked", "true");
		expect(screen.getByRole("menuitem", { name: /Effort.*High/ })).toBeInTheDocument();
		expect(await screen.findByRole("menuitemradio", { name: /^GPT X/ })).toBeInTheDocument();
		expect(screen.queryByRole("menuitemradio", { name: /^Opus/ })).not.toBeInTheDocument();
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
		expect(await screen.findByRole("menuitemradio", { name: /^Opus/ })).toBeInTheDocument();
	});

	describe("Ultracode", () => {
		it("is a switch in the effort's side menu, under the levels, not one of them", async () => {
			const user = userEvent.setup();
			const onToggleUltracode = vi.fn();
			open(undefined, false, "claude-code", { fast: false, onToggleFast: vi.fn(), ultracode: false, onToggleUltracode });
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			// No icon beside Fast mode any more.
			expect(screen.queryByRole("menuitemcheckbox", { name: "Ultracode" })).not.toBeInTheDocument();
			await user.click(screen.getByRole("menuitem", { name: /Effort/ }));
			expect(await screen.findByRole("menuitemradio", { name: "High" })).toBeInTheDocument();
			expect(screen.queryByRole("menuitemradio", { name: /Ultracode/ })).not.toBeInTheDocument();
			expect(screen.getByRole("switch", { name: "Ultracode" })).toHaveAttribute("aria-checked", "false");
			fireEvent.click(screen.getByRole("menuitem", { name: /Ultracode/ }));
			expect(onToggleUltracode).toHaveBeenCalledExactlyOnceWith(true);
			// Toggling it keeps the side menu open, beside the picker.
			expect(screen.queryAllByRole("menu")).toHaveLength(2);
		});

		it("says what it does, as Fast mode does on hover", async () => {
			const user = userEvent.setup();
			open(undefined, false, "claude-code", { fast: false, onToggleFast: vi.fn(), ultracode: false, onToggleUltracode: vi.fn() });
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			expect(screen.getByRole("menuitemcheckbox", { name: "Fast mode" })).toHaveAccessibleDescription(/Faster replies from the same model/);
			await user.hover(screen.getByRole("menuitemcheckbox", { name: "Fast mode" }));
			expect(await screen.findByRole("tooltip")).toHaveTextContent("Faster replies from the same model");
			await user.click(screen.getByRole("menuitem", { name: /Effort/ }));
			expect((await screen.findByRole("menuitem", { name: /Ultracode/ })).getAttribute("title")).toMatch(/Claude Code's own workflows, at any effort level/);
		});

		it("is named on the trigger after the effort", () => {
			open(undefined, false, "claude-code", { ultracode: true, onToggleUltracode: vi.fn() });
			const trigger = screen.getByRole("button", { name: /Agent, model and reasoning effort/ });
			expect(trigger).toHaveTextContent(/High\s*Ultracode/);
		});

		it("is absent for an agent without it", async () => {
			const user = userEvent.setup();
			open(undefined, false, "codex", { fast: false, onToggleFast: vi.fn() });
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			expect(screen.getByRole("menuitemcheckbox", { name: "Fast mode" })).toBeInTheDocument();
			await user.click(screen.getByRole("menuitem", { name: /Effort/ }));
			expect(screen.queryByRole("switch", { name: "Ultracode" })).not.toBeInTheDocument();
		});

		it("turns on for a switch to Claude Code without changing the effort", async () => {
			const user = userEvent.setup();
			const { onPendingChange } = open({ harness: "claude-code", effort: "high" }, false, "codex");
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			await user.click(await screen.findByRole("menuitem", { name: /Effort/ }));
			fireEvent.click(await screen.findByRole("menuitem", { name: /Ultracode/ }));
			expect(onPendingChange).toHaveBeenLastCalledWith(expect.objectContaining({ harness: "claude-code", effort: "high", ultracode: true }));
			expect(screen.getByRole("menuitem", { name: /Effort.*High/ })).toBeInTheDocument();
		});

		it("stays on when a switch to Claude Code picks another level", async () => {
			const user = userEvent.setup();
			const { onPendingChange } = open({ harness: "claude-code", effort: "xhigh", ultracode: true }, false, "codex");
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			await user.click(await screen.findByRole("menuitem", { name: /Effort/ }));
			expect(await screen.findByRole("switch", { name: "Ultracode" })).toHaveAttribute("aria-checked", "true");
			fireEvent.click(await screen.findByRole("menuitemradio", { name: "Max" }));
			expect(onPendingChange).toHaveBeenLastCalledWith(expect.objectContaining({ effort: "max", ultracode: true }));
		});

		it("can still be switched while the agent's settings are locked", async () => {
			// It only changes the chat's next message, never the agent, so a starting agent does not lock it.
			const user = userEvent.setup();
			const onToggleUltracode = vi.fn();
			const onPickModel = vi.fn();
			render(
				<QueryClientProvider client={new QueryClient()}>
					<AgentModelPicker
						currentHarness="claude-code"
						current={{ models: [{ id: "opus", label: "Opus" }, { id: "sonnet", label: "Sonnet" }], modelId: "opus", modelLabel: "Opus", onPickModel, efforts: [], ultracode: false, onToggleUltracode }}
						disabled
						agentSwitch={{ projectId: "p", onPendingChange: vi.fn() }}
					/>
				</QueryClientProvider>,
			);
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			// With no levels to pick, the effort's side menu still opens, for Ultracode alone.
			await user.click(screen.getByRole("menuitem", { name: /Effort/ }));
			const ultracode = await screen.findByRole("menuitem", { name: /Ultracode/ });
			expect(ultracode).not.toHaveAttribute("aria-disabled", "true");
			fireEvent.click(ultracode);
			expect(onToggleUltracode).toHaveBeenCalledExactlyOnceWith(true);
			expect(onPickModel).not.toHaveBeenCalled();
		});

		it("keeps the permission mode picked for a switch", async () => {
			const user = userEvent.setup();
			const { onPendingChange } = open({ harness: "claude-code", effort: "high", approvalMode: "auto" }, false, "codex");
			await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
			await user.click(await screen.findByRole("menuitem", { name: /Effort/ }));
			fireEvent.click(await screen.findByRole("menuitem", { name: /Ultracode/ }));
			expect(onPendingChange).toHaveBeenLastCalledWith(expect.objectContaining({ ultracode: true, approvalMode: "auto" }));
		});
	});

	it("puts Fast mode and auto-compact in one row", async () => {
		const user = userEvent.setup();
		const onChange = vi.fn();
		render(
			<QueryClientProvider client={new QueryClient()}>
				<AgentModelPicker
					currentHarness="claude-code"
					current={{ models: [{ id: "opus", label: "Opus" }], modelId: "opus", modelLabel: "Opus", onPickModel: vi.fn(), efforts: [], fast: false, onToggleFast: vi.fn() }}
					agentSwitch={{ projectId: "p", onPendingChange: vi.fn() }}
					autoCompact={{ onChange, title: "When it compacts" }}
				/>
			</QueryClientProvider>,
		);
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const compact = screen.getByRole("radiogroup", { name: "Auto-compact" });
		expect(compact.parentElement).toContainElement(screen.getByRole("menuitemcheckbox", { name: "Fast mode" }));
		expect(within(compact).getAllByRole("radio").map((radio) => radio.textContent)).toEqual(["Auto", "200k", "500k", "1M"]);
		await user.click(within(compact).getByRole("radio", { name: "500k" }));
		expect(onChange).toHaveBeenLastCalledWith("500k");
		await user.click(within(compact).getByRole("radio", { name: "1M" }));
		expect(onChange).toHaveBeenLastCalledWith("1m");
	});

	it("hides Fast mode and keeps auto-compact in place, disabled, for an agent without them", async () => {
		const user = userEvent.setup();
		render(
			<QueryClientProvider client={new QueryClient()}>
				<AgentModelPicker
					currentHarness="agy"
					current={{ models: [{ id: "flash", label: "Flash" }], modelId: "flash", modelLabel: "Flash", onPickModel: vi.fn(), efforts: [{ value: "low", label: "Low" }, { value: "high", label: "High" }], effort: "high", onPickEffort: vi.fn() }}
					agentSwitch={{ projectId: "p", onPendingChange: vi.fn() }}
				/>
			</QueryClientProvider>,
		);
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.queryByRole("menuitemcheckbox", { name: "Fast mode" })).not.toBeInTheDocument();
		const compact = screen.getByRole("radiogroup", { name: "Auto-compact" });
		expect(compact).toHaveAttribute("aria-disabled", "true");
		expect(within(compact).getAllByRole("radio").map((radio) => radio.textContent)).toEqual(["Auto"]);
	});

	it("offers OpenCode's auto-compact on or off on a chat switch to it", async () => {
		const user = userEvent.setup();
		const { onPendingChange } = open({ harness: "opencode-v2" }, false, "claude-code");
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		const compact = screen.getByRole("radiogroup", { name: "Auto-compact" });
		expect(within(compact).getAllByRole("radio").map((radio) => radio.textContent)).toEqual(["Auto", "Off"]);
		await user.click(within(compact).getByRole("radio", { name: "Off" }));
		expect(onPendingChange).toHaveBeenCalledWith(expect.objectContaining({ harness: "opencode-v2", autoCompactWindow: "off" }));
	});

	it("sets auto-compact only where the agent reads it", () => {
		expect(autoCompactKind("claude-code", "opus")).toBe("window");
		expect(autoCompactKind("claude-code", "claude-haiku-4-5")).toBeUndefined();
		expect(autoCompactKind("opencode-v2")).toBe("toggle");
		expect(autoCompactKind("opencode", "", true)).toBeUndefined();
		expect(autoCompactKind("agy")).toBeUndefined();
	});

	it("has no account switch with only one account", async () => {
		const user = userEvent.setup();
		const one = { options: [{ id: "a", label: "me@work" }], value: "a", onPick: vi.fn() };
		open(undefined, false, "claude-code", {}, false, false, undefined, one);
		await user.click(screen.getByRole("button", { name: /Agent, model and reasoning effort/ }));
		expect(screen.queryByRole("menuitem", { name: /^Account/ })).not.toBeInTheDocument();
		expect(screen.queryByText("me@work")).not.toBeInTheDocument();
	});
	it("marks the chip with the account's badge only with two or more accounts", async () => {
		const one = { options: [{ id: "a", label: "me@work", name: "Work", color: "blue" as const }], value: "a", onPick: vi.fn() };
		open(undefined, false, "claude-code", {}, false, false, undefined, one);
		expect(screen.queryByRole("img", { name: "Work" })).not.toBeInTheDocument();
	});

	it("marks the chip with the account's first letter and switches accounts from the bottom row", async () => {
		const user = userEvent.setup();
		const two = {
			options: [
				{ id: "a", label: "Work · me@work", name: "Work", email: "me@work", color: "blue" as const },
				{ id: "b", label: "me@home", email: "me@home" },
			],
			value: "a",
			onPick: vi.fn(),
		};
		open(undefined, false, "claude-code", {}, false, false, undefined, two);
		const chip = screen.getByRole("button", { name: /Agent, model and reasoning effort/ });
		expect(within(chip).getByRole("img", { name: "Work" })).toHaveTextContent("W");
		await user.click(chip);
		const switcher = screen.getByRole("menuitem", { name: "Account: Work · me@work" });
		expect(switcher).toHaveTextContent("WO");
		await user.click(switcher);
		expect(await screen.findByRole("menuitemradio", { name: /Work/ })).toHaveAttribute("aria-checked", "true");
		const home = screen.getByRole("menuitemradio", { name: /me@home/ });
		expect(home).toHaveTextContent("ME");
		fireEvent.click(home);
		expect(two.onPick).toHaveBeenCalledWith("b");
	});
});
