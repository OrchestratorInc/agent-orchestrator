import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";

const fixture = vi.hoisted(() => ({
	getAgentModels: vi.fn(),
	signals: [] as AbortSignal[],
}));
vi.mock("../api", () => ({
	getAgents: async () => {
		const agent = (id: string, label: string) => ({ id, label, authStatus: "authorized" });
		const all = [agent("claude-code", "Claude Code"), agent("codex", "Codex"), agent("cursor", "Cursor")];
		return { supported: all, installed: all, authorized: all };
	},
	getSettings: async () => ({ defaultSessionMode: "chat", chatHarnesses: ["claude-code", "codex", "cursor"] }),
	getAgentModels: fixture.getAgentModels,
}));
import { useAgentSwitchMenu, type AgentSwitchMenu } from "./useAgentSwitchMenu";

const config = {} as ServerConfig;
let renderer: ReactTestRenderer | undefined;
let menu: AgentSwitchMenu | undefined;
function Harness() { menu = useAgentSwitchMenu({ cfg: config, current: "claude-code", enabled: true }); return null; }

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.signals = [];
	// Codex answers at once; Cursor is still discovering and only stops when told to.
	fixture.getAgentModels.mockReset().mockImplementation((_cfg: ServerConfig, id: string, _project: unknown, signal: AbortSignal) => {
		fixture.signals.push(signal);
		if (id === "codex") return Promise.resolve({ models: [{ id: "gpt-6-sol", label: "GPT-6-Sol" }] });
		return new Promise(() => {});
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

it("shows each agent's models as they arrive, without waiting on one still discovering", async () => {
	await act(async () => { renderer = create(<Harness />); });
	expect(menu?.agents.map((agent) => agent.id)).toEqual(["codex", "cursor"]);
	expect(menu?.models.codex?.models.map((model) => model.label)).toEqual(["GPT-6-Sol"]);
	expect(menu?.models.cursor).toBeUndefined();
});

it("stops discovery when the chat closes", async () => {
	await act(async () => { renderer = create(<Harness />); });
	expect(fixture.signals).toHaveLength(2);
	expect(fixture.signals.every((signal) => !signal.aborted)).toBe(true);
	act(() => renderer?.unmount());
	renderer = undefined;
	expect(fixture.signals.every((signal) => signal.aborted)).toBe(true);
});

// The app drops its verified connection on every app switch. The menu cleared
// its agents and their models then, and loaded them again on return.
it("keeps the agents and their models while the connection is away, and lists them again once it is back", async () => {
	let cfg: ServerConfig | null = config;
	function Switching() { menu = useAgentSwitchMenu({ cfg, current: "claude-code", enabled: true }); return null; }
	await act(async () => { renderer = create(<Switching />); });
	const listed = menu;
	expect(listed?.models.codex?.models.map((model) => model.label)).toEqual(["GPT-6-Sol"]);
	cfg = null;
	await act(async () => { renderer!.update(<Switching />); });
	expect(menu?.agents.map((agent) => agent.id)).toEqual(["codex", "cursor"]);
	expect(menu?.models.codex?.models.map((model) => model.label)).toEqual(["GPT-6-Sol"]);
	// Verified again: read afresh, with nothing cleared in between.
	cfg = { ...config };
	fixture.getAgentModels.mockClear();
	await act(async () => { renderer!.update(<Switching />); });
	expect(fixture.getAgentModels).toHaveBeenCalled();
	expect(menu?.agents.map((agent) => agent.id)).toEqual(["codex", "cursor"]);
	expect(menu?.models.codex?.models.map((model) => model.label)).toEqual(["GPT-6-Sol"]);
});

// The chat moved to another agent while the connection was away: the list kept
// from before must not offer the agent the chat now runs.
it("never offers the running agent from a list kept while the connection is away", async () => {
	let cfg: ServerConfig | null = config;
	let current = "claude-code";
	function Switching() { menu = useAgentSwitchMenu({ cfg, current, enabled: true }); return null; }
	await act(async () => { renderer = create(<Switching />); });
	expect(menu?.agents.map((agent) => agent.id)).toEqual(["codex", "cursor"]);
	cfg = null;
	current = "codex";
	await act(async () => { renderer!.update(<Switching />); });
	expect(menu?.agents.map((agent) => agent.id)).toEqual(["cursor"]);
});

// A switch can start in Fast mode only on a model with a fast tier: the menu
// reads which from each agent's catalog, and whether its default has one (the
// pick that names no model).
it("reads each agent's models with a Fast mode, and whether its default has one", async () => {
	fixture.getAgentModels.mockImplementation((_cfg: ServerConfig, id: string) => Promise.resolve(id === "codex"
		? { models: [{ id: "gpt-5.5", label: "GPT-5.5", isDefault: true, supportsFast: true }, { id: "gpt-5.5-codex", label: "GPT-5.5 Codex", supportsFast: true }, { id: "gpt-5.1-mini", label: "mini" }] }
		: { models: [{ id: "auto", label: "Auto", isDefault: true }, { id: "fast-one", label: "Fast one", supportsFast: true }] }));
	await act(async () => { renderer = create(<Harness />); });
	expect(menu?.fast).toEqual({
		codex: { models: ["gpt-5.5", "gpt-5.5-codex"], byDefault: true },
		cursor: { models: ["fast-one"], byDefault: false },
	});
});
