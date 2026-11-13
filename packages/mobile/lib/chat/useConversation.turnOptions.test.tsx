import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";

// Each test uses its own session: the page, catalog and last-catalog caches are module-wide.
const fixture = vi.hoisted(() => ({
	request: vi.fn(),
	controller: "ready",
	capabilities: ["config_options"] as string[],
	options: [] as unknown[] | Error,
	models: [] as unknown[],
	harness: "claude-code",
	patch: undefined as Promise<unknown[]> | undefined,
}));
vi.mock("../api", () => ({ apiRequest: fixture.request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ machineIdentity: () => "qa-host", authHeaders: () => ({}), httpBase: () => "http://qa-host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: async () => null, setItem: async () => {}, removeItem: async () => {} } }));
vi.mock("./conversationEvents", () => ({ subscribeConversationEvents: () => () => {} }));
vi.mock("./conversationPoll", () => ({ conversationPollIntervalFor: () => null }));
import { useMobileConversation, type MobileConversation } from "./useConversation";

const config = {} as ServerConfig;
const modelOption = {
	id: "model", name: "Model", category: "model", type: "select", currentValue: "opus",
	choices: [{ value: "opus", name: "Opus 5.5" }],
};
let renderer: ReactTestRenderer | undefined;
let conversation: MobileConversation;
function Harness({ id }: { id: string }) { conversation = useMobileConversation(config, "qa-host", id); return null; }
function paths() { return fixture.request.mock.calls.map(([, path]) => String(path).replace(/\?.*$/, "").replace(/^.*\/conversation/, "")); }
async function open(id: string) {
	await act(async () => { renderer = create(<Harness id={id} />); });
	fixture.request.mockClear();
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.controller = "ready"; fixture.capabilities = ["config_options"]; fixture.options = []; fixture.models = [];
	fixture.harness = "claude-code"; fixture.patch = undefined;
	fixture.request.mockReset().mockImplementation(async (_cfg: ServerConfig, path: string) => {
		if (path.includes("/conversation/config-options/")) return { json: async () => ({ options: await fixture.patch }) };
		if (path.endsWith("/conversation/config-options")) {
			if (fixture.options instanceof Error) throw fixture.options;
			return { json: async () => ({ options: fixture.options }) };
		}
		if (path.endsWith("/conversation/models")) return { json: async () => ({ models: fixture.models }) };
		return { json: async () => ({ controller: fixture.controller, capabilities: fixture.capabilities, harness: fixture.harness, messages: [] }) };
	});
});
afterEach(async () => { if (renderer) await act(async () => renderer!.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

it("asks for provider controls even when the snapshot was read while the chat slept", async () => {
	fixture.controller = "hibernated"; fixture.capabilities = [];
	await open("slept");
	fixture.options = [modelOption];
	await act(async () => { await conversation.loadTurnOptions(); });
	expect(paths()).toEqual(["/config-options"]);
	expect(conversation.configOptions.map((option) => option.id)).toEqual(["model"]);
});

it("does not keep an empty catalog for 30 seconds, so the next load asks again", async () => {
	await open("empty-then-ready");
	await act(async () => { await conversation.loadTurnOptions(); });
	expect(paths()).toEqual(["/config-options", "/models"]);
	fixture.options = [modelOption];
	await act(async () => { await conversation.loadTurnOptions(); });
	expect(paths()).toEqual(["/config-options", "/models", "/config-options"]);
	expect(conversation.configOptions.map((option) => option.id)).toEqual(["model"]);
});

it("still serves a catalog with choices from the cache", async () => {
	fixture.options = [modelOption];
	await open("cached");
	await act(async () => { await conversation.loadTurnOptions(); });
	await act(async () => { await conversation.loadTurnOptions(); });
	expect(paths()).toEqual(["/config-options"]);
});

it("keeps the last catalog on a reopened chat whose provider is not up yet", async () => {
	fixture.options = [modelOption];
	await open("reopened");
	await act(async () => { await conversation.loadTurnOptions(); });
	await act(async () => renderer!.unmount());
	renderer = undefined;

	fixture.controller = "hibernated"; fixture.capabilities = [];
	fixture.options = new Error("CHAT_CONTROLLER_NOT_READY");
	await open("reopened");
	expect(conversation.configOptions.map((option) => option.id)).toEqual(["model"]);
	await act(async () => { await expect(conversation.loadTurnOptions({ refresh: true })).rejects.toThrow("CHAT_CONTROLLER_NOT_READY"); });
	expect(conversation.configOptions.map((option) => option.id)).toEqual(["model"]);

	fixture.options = [];
	let catalog: Awaited<ReturnType<MobileConversation["loadTurnOptions"]>> | undefined;
	await act(async () => { catalog = await conversation.loadTurnOptions({ refresh: true }); });
	expect(catalog?.configOptions.map((option) => option.id)).toEqual(["model"]);
	expect(conversation.configOptions.map((option) => option.id)).toEqual(["model"]);
});

it("retries a catalog that failed while the chat woke, and only then", async () => {
	fixture.options = new Error("CHAT_CONTROLLER_NOT_READY");
	await open("stale-ready");
	await act(async () => { await expect(conversation.loadTurnOptions()).rejects.toThrow("CHAT_CONTROLLER_NOT_READY"); });
	fixture.options = [modelOption];
	await act(async () => { await conversation.loadTurnOptions({ refresh: true, ifMissing: true }); });
	expect(conversation.configOptions.map((option) => option.id)).toEqual(["model"]);
	fixture.request.mockClear();
	await act(async () => { await conversation.loadTurnOptions({ refresh: true, ifMissing: true }); });
	expect(paths()).toEqual([]);
});

it("shows the remembered catalog on a screen that mounted before it was remembered", async () => {
	const screens: Record<string, MobileConversation> = {};
	function Pair() {
		screens.first = useMobileConversation(config, "qa-host", "pair");
		screens.second = useMobileConversation(config, "qa-host", "pair");
		return null;
	}
	await act(async () => { renderer = create(<Pair />); });
	expect(screens.first.configOptions).toEqual([]);
	fixture.options = [modelOption];
	await act(async () => { await screens.second.loadTurnOptions(); });
	fixture.options = [];
	await act(async () => { await screens.first.loadTurnOptions({ refresh: true }); });
	expect(screens.first.configOptions.map((option) => option.id)).toEqual(["model"]);
});

/** Holds the next catalog request until the test answers or fails it. */
function holdNextCatalog() {
	const held = { answer: (_options: unknown[]) => {}, fail: (_cause: Error) => {} };
	fixture.request.mockImplementationOnce(() => new Promise((resolve, reject) => {
		held.answer = (options) => resolve({ json: async () => ({ options }) });
		held.fail = reject;
	}));
	return held;
}

it("waits for a load in flight instead of asking twice when the lease registers", async () => {
	await open("overlap");
	const held = holdNextCatalog();
	let first!: Promise<unknown>;
	let second!: Promise<unknown>;
	await act(async () => {
		first = conversation.loadTurnOptions();
		second = conversation.loadTurnOptions({ refresh: true, ifMissing: true });
	});
	await act(async () => { held.answer([modelOption]); await first; await second; });
	expect(paths()).toEqual(["/config-options"]);
	expect(conversation.configOptions.map((option) => option.id)).toEqual(["model"]);
});

it("does not let an older load undo a newer one, whether it fails or lands late", async () => {
	await open("older-fails");
	const failing = holdNextCatalog();
	let older!: Promise<unknown>;
	await act(async () => { older = conversation.loadTurnOptions(); });
	fixture.options = [modelOption];
	await act(async () => { await conversation.loadTurnOptions({ refresh: true }); });
	await act(async () => { failing.fail(new Error("CHAT_CONTROLLER_NOT_READY")); await expect(older).rejects.toThrow(); });
	fixture.request.mockClear();
	await act(async () => { await conversation.loadTurnOptions({ refresh: true, ifMissing: true }); });
	expect(paths()).toEqual([]);

	const late = holdNextCatalog();
	await act(async () => { older = conversation.loadTurnOptions({ refresh: true }); });
	await act(async () => { await conversation.loadTurnOptions({ refresh: true }); });
	await act(async () => { late.answer([{ ...modelOption, currentValue: "sonnet", choices: [{ value: "sonnet", name: "Sonnet" }] }]); await older; });
	expect(conversation.configOptions[0]?.currentValue).toBe("opus");
});

// A setting changed just before an agent switch answers for the previous agent;
// that answer must not show, nor be remembered for the new agent.
it("drops a config change's answer that lands after the chat switched agents", async () => {
	fixture.options = [modelOption];
	await open("switch-during-config");
	await act(async () => { await conversation.loadTurnOptions(); });
	expect(conversation.configOptions.map((option) => option.id)).toEqual(["model"]);

	let answer!: (options: unknown[]) => void;
	fixture.patch = new Promise((resolve) => { answer = resolve; });
	let change: Promise<unknown> | undefined;
	await act(async () => { change = conversation.setConfigOption("fast", { enabled: true }); });
	fixture.harness = "codex";
	await act(async () => { await conversation.refresh(); });
	await act(async () => { answer([{ id: "fast", name: "Fast mode", type: "boolean", currentBoolean: true, choices: [] }]); await change; });
	expect(conversation.configOptions.map((option) => option.id)).not.toContain("fast");

	fixture.options = [];
	await act(async () => { await conversation.loadTurnOptions({ refresh: true }); });
	expect(conversation.configOptions).toEqual([]);
});

// After a switch the outgoing agent's catalog never stands in for the new
// one's, even when the new agent's first catalog request fails.
it("clears the outgoing agent's catalog at a switch, and keeps it clear when the new agent's load fails", async () => {
	fixture.options = [modelOption];
	await open("switch-then-fail");
	await act(async () => { await conversation.loadTurnOptions(); });
	expect(conversation.configOptions.map((option) => option.id)).toEqual(["model"]);

	fixture.harness = "codex";
	await act(async () => { await conversation.refresh(); });
	expect(conversation.configOptions).toEqual([]);
	expect(conversation.models).toEqual([]);

	fixture.options = new Error("CHAT_CONTROLLER_NOT_READY");
	await act(async () => { await expect(conversation.loadTurnOptions({ refresh: true })).rejects.toThrow("CHAT_CONTROLLER_NOT_READY"); });
	expect(conversation.configOptions).toEqual([]);
});

// A load started for the outgoing agent answers for it, not for the new one.
it("drops a catalog load that was started for the outgoing agent", async () => {
	await open("switch-during-load");
	let answer!: (options: unknown[]) => void;
	fixture.request.mockImplementationOnce(async () => ({ json: async () => ({ options: await new Promise<unknown[]>((resolve) => { answer = resolve; }) }) }));
	let load: Promise<unknown> | undefined;
	await act(async () => { load = conversation.loadTurnOptions({ refresh: true }); });
	fixture.harness = "codex";
	await act(async () => { await conversation.refresh(); });
	await act(async () => { answer([modelOption]); await load; });
	expect(conversation.configOptions).toEqual([]);
});

// Reopening a switched chat: the outgoing agent's remembered catalog stays
// remembered for it, but must not come back for the new agent.
it("does not bring the outgoing agent's remembered catalog back when a switched chat is reopened", async () => {
	fixture.options = [modelOption];
	await open("switch-then-reopen");
	await act(async () => { await conversation.loadTurnOptions(); });
	fixture.harness = "codex";
	await act(async () => { await conversation.refresh(); });
	await act(async () => renderer!.unmount());
	renderer = undefined;

	fixture.options = new Error("CHAT_CONTROLLER_NOT_READY");
	await open("switch-then-reopen");
	expect(conversation.snapshot?.harness).toBe("codex");
	expect(conversation.configOptions).toEqual([]);
	await act(async () => { await expect(conversation.loadTurnOptions({ refresh: true })).rejects.toThrow("CHAT_CONTROLLER_NOT_READY"); });
	expect(conversation.configOptions).toEqual([]);
});
