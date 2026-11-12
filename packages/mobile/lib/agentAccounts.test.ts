import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));

import { agentAccountOptions, agentAccountProvider, getAgentAccounts, getSessionAgentAccount, moveSessionAgentAccount } from "./agentAccounts";
import { delegateTask, spawnSession } from "./api";
import type { ServerConfig } from "./config";

const cfg: ServerConfig = { host: "ao.test", httpPort: "3011", muxPort: "3011", secure: false, password: "secret12" };
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

describe("agent accounts on mobile", () => {
	beforeEach(() => vi.stubGlobal("fetch", vi.fn()));
	afterEach(() => vi.unstubAllGlobals());

	it("lists one provider's accounts and the one a session runs on", async () => {
		vi.mocked(fetch)
			.mockResolvedValueOnce(json({ accounts: [
				{ id: "c1", provider: "claude", email: "a@x.dev", signedIn: true, default: true },
				{ id: "x1", provider: "codex", signedIn: true, default: true },
				{ id: "c2", provider: "claude", signedIn: false, default: false },
			] }))
			.mockResolvedValueOnce(json({ claude: "c2", codex: "x1" }));
		const accounts = await getAgentAccounts(cfg, agentAccountProvider("claude-code")!);
		expect(agentAccountOptions(accounts)).toEqual([
			{ id: "c1", title: "a@x.dev", detail: "Default", disabled: false },
			{ id: "c2", title: "OAuth token", detail: "Signed out", disabled: true },
		]);
		expect(await getSessionAgentAccount(cfg, "ao 1", "claude")).toBe("c2");
		expect(vi.mocked(fetch).mock.calls[1][0]).toBe("http://ao.test:3011/api/v1/agents/accounts/sessions/ao%201");
	});

	it("moves a session, stopping its turn only when asked", async () => {
		vi.mocked(fetch).mockImplementation(async () => json({ transition: { id: "t1" } }, 202));
		await moveSessionAgentAccount(cfg, "ao-1", "c2", false);
		await moveSessionAgentAccount(cfg, "ao-1", "c2", true);
		const bodies = vi.mocked(fetch).mock.calls.map(([, init]) => [init?.method, JSON.parse(String(init?.body))]);
		expect(bodies).toEqual([["PUT", { accountId: "c2" }], ["PUT", { accountId: "c2", stopTurn: true }]]);
	});

	it("starts a task on the picked account", async () => {
		vi.mocked(fetch)
			.mockResolvedValueOnce(json({ session: { id: "w-1" } }, 201))
			.mockResolvedValueOnce(json({ workerId: "w-2" }, 202))
			.mockResolvedValueOnce(json({ session: { id: "w-2", projectId: "p-1" } }));
		await spawnSession(cfg, { harness: "codex", accountId: "x1" });
		await delegateTask(cfg, { projectId: "p-1", brief: "Fix it", agent: "codex", mode: "chat", accountId: "x2" });
		expect(JSON.parse(String(vi.mocked(fetch).mock.calls[0][1]?.body))).toMatchObject({ accountId: "x1" });
		expect(JSON.parse(String(vi.mocked(fetch).mock.calls[1][1]?.body))).toMatchObject({ accountId: "x2" });
	});
});
