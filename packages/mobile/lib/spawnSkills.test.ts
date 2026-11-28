import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));

import { getAgentSkills } from "./api";
import type { ServerConfig } from "./config";
import { forgetRememberedChatSkills, rememberChatSkills } from "./chat/rememberedSkills";
import { loadSpawnSkills } from "./spawnSkills";

const cfg: ServerConfig = { host: "ao.test", httpPort: "3011", muxPort: "3011", secure: false, password: "secret12" };
const review = { name: "review", displayName: "review", description: "Review the diff", source: "agent" };

function answer(status: number, body: unknown) {
	return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

beforeEach(() => {
	vi.stubGlobal("fetch", vi.fn().mockImplementation(async () => answer(200, { skills: [review] })));
});
afterEach(() => { vi.unstubAllGlobals(); forgetRememberedChatSkills(); });

describe("getAgentSkills", () => {
	it("asks the machine for an agent's skills in a project", async () => {
		await expect(getAgentSkills(cfg, "claude-code", "proj 1")).resolves.toEqual([review]);
		expect(vi.mocked(fetch).mock.calls[0][0]).toBe("http://ao.test:3011/api/v1/agents/claude-code/skills?projectId=proj%201");
	});

	it("asks for the user's own skills without a project", async () => {
		await getAgentSkills(cfg, "codex");
		expect(vi.mocked(fetch).mock.calls[0][0]).toBe("http://ao.test:3011/api/v1/agents/codex/skills");
	});

	it("lists none on a machine whose version has no such route", async () => {
		vi.mocked(fetch).mockResolvedValueOnce(answer(404, { error: "not_found", code: "NOT_FOUND", message: "not found" }));
		await expect(getAgentSkills(cfg, "claude-code")).resolves.toEqual([]);
	});

	it("still fails on other errors", async () => {
		vi.mocked(fetch).mockResolvedValueOnce(answer(500, { error: "internal", code: "INTERNAL_ERROR", message: "boom" }));
		await expect(getAgentSkills(cfg, "claude-code")).rejects.toMatchObject({ status: 500 });
	});
});

describe("loadSpawnSkills", () => {
	it("reads once per machine, agent and project while the list is fresh", async () => {
		await loadSpawnSkills(cfg, "host-a", "claude-code", "p1");
		await loadSpawnSkills(cfg, "host-a", "claude-code", "p1");
		expect(fetch).toHaveBeenCalledTimes(1);
		await loadSpawnSkills(cfg, "host-a", "claude-code", "p2");
		await loadSpawnSkills(cfg, "host-b", "claude-code", "p1");
		await loadSpawnSkills(cfg, "host-a", "codex", "p1");
		expect(fetch).toHaveBeenCalledTimes(4);
	});

	it("lists what a chat of the agent reported first, then the skills only found on disk", async () => {
		rememberChatSkills("host-d", "claude-code", "p1", [{ name: "compact", displayName: "compact" }, review]);
		const skills = await loadSpawnSkills(cfg, "host-d", "claude-code", "p1");
		expect(skills.map((item) => item.name)).toEqual(["compact", "review"]);
	});

	it("still lists what a chat reported when the machine can't be read", async () => {
		rememberChatSkills("host-e", "claude-code", undefined, [{ name: "compact", displayName: "compact" }]);
		await expect(loadSpawnSkills(null, "host-e", "claude-code")).resolves.toEqual([{ name: "compact", displayName: "compact" }]);
	});

	it("fails without a connection to the machine", async () => {
		await expect(loadSpawnSkills(null, "host-c", "claude-code")).rejects.toMatchObject({ name: "UnreachableError" });
		expect(fetch).not.toHaveBeenCalled();
	});
});
