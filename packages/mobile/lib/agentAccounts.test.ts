import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));

import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { agentAccountLabel, agentAccountOptions, agentAccountProvider, getAgentAccounts, getSessionAgentAccount, moveSessionAgentAccount, useAgentAccounts } from "./agentAccounts";
import { delegateTask, spawnSession } from "./api";
import type { ServerConfig } from "./config";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

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

	it("tells same-email accounts in different organizations apart", () => {
		const accounts = [
			{ id: "p", provider: "claude" as const, email: "a@x.dev", orgId: "org-personal-1", orgName: "Personal", signedIn: true, default: false },
			{ id: "t", provider: "claude" as const, email: "A@x.dev", orgId: "org-team-22", orgName: "Team", signedIn: true, default: false },
			{ id: "u", provider: "claude" as const, email: "b@x.dev", signedIn: true, default: false },
		];
		expect(agentAccountOptions(accounts).map((option) => option.title)).toEqual(["a@x.dev · Personal", "A@x.dev · Team", "b@x.dev"]);
		const sameName = [
			{ id: "1", provider: "claude" as const, email: "a@x.dev", orgId: "aaaaaaaa-1111", orgName: "Acme" },
			{ id: "2", provider: "claude" as const, email: "a@x.dev", orgId: "bbbbbbbb-2222", orgName: "Acme" },
		];
		expect(sameName.map((account) => agentAccountLabel(account, sameName))).toEqual(["a@x.dev · Acme · aaaaaaaa", "a@x.dev · Acme · bbbbbbbb"]);
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

describe("useAgentAccounts", () => {
	beforeEach(() => { vi.useFakeTimers(); vi.stubGlobal("fetch", vi.fn()); });
	afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

	const mount = () => {
		let state!: ReturnType<typeof useAgentAccounts>;
		function Probe() {
			state = useAgentAccounts(cfg, "claude-code", "ao-1");
			return null;
		}
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(createElement(Probe)); });
		return { get: () => state, unmount: () => act(() => renderer.unmount()) };
	};

	it("loads again after a failed first read, with the same config", async () => {
		let down = true;
		vi.mocked(fetch).mockImplementation(async (url) => {
			if (down) throw new TypeError("Network request failed");
			return String(url).includes("/sessions/")
				? json({ claude: "c1" })
				: json({ accounts: [{ id: "c1", provider: "claude", email: "a@x.dev", signedIn: true, default: true }] });
		});
		const probe = mount();
		await act(async () => { await vi.advanceTimersByTimeAsync(0); });
		expect(probe.get().accounts).toEqual([]);
		down = false;
		await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
		expect(probe.get().accounts.map((account) => account.id)).toEqual(["c1"]);
		expect(probe.get().current).toBe("c1");
		probe.unmount();
	});

	it("reads a move made elsewhere when asked again, while still mounted", async () => {
		let bound = "c1";
		vi.mocked(fetch).mockImplementation(async (url) => String(url).includes("/sessions/")
			? json({ claude: bound })
			: json({ accounts: [
				{ id: "c1", provider: "claude", email: "a@x.dev", signedIn: true, default: true },
				{ id: "c2", provider: "claude", email: "b@x.dev", signedIn: true, default: false },
			] }));
		const probe = mount();
		await act(async () => { await vi.advanceTimersByTimeAsync(0); });
		expect(probe.get().current).toBe("c1");
		bound = "c2";
		await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
		expect(probe.get().current).toBe("c1");
		let fresh: Awaited<ReturnType<ReturnType<typeof useAgentAccounts>["reload"]>>;
		await act(async () => { fresh = await probe.get().reload(); });
		expect(fresh!.current).toBe("c2");
		expect(probe.get().current).toBe("c2");
		probe.unmount();
	});

	it("does not retry a machine that refuses the phone", async () => {
		vi.mocked(fetch).mockImplementation(async () => json({ code: "ORIGIN_FORBIDDEN", message: "Origin is not allowed" }, 403));
		const probe = mount();
		await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
		expect(vi.mocked(fetch)).toHaveBeenCalledTimes(2);
		expect(probe.get().accounts).toEqual([]);
		probe.unmount();
	});
});
