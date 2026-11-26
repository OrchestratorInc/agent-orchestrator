import { afterEach, describe, expect, it } from "vitest";
import { forgetRememberedChatSkills, mergeChatSkills, rememberChatSkills, rememberedChatSkills } from "./rememberedSkills";

const skill = (name: string) => ({ name, displayName: name });

afterEach(() => forgetRememberedChatSkills());

describe("remembered chat skills", () => {
	it("keeps a chat's list per machine and agent, and per project", () => {
		rememberChatSkills("h1", "claude-code", "p1", [skill("compact"), skill("ship")]);
		rememberChatSkills("h1", "claude-code", undefined, [skill("compact")]);
		expect(rememberedChatSkills("h1", "claude-code", "p1").map((item) => item.name)).toEqual(["compact", "ship"]);
		// Another project falls back to the agent's latest list on this machine.
		expect(rememberedChatSkills("h1", "claude-code", "p2").map((item) => item.name)).toEqual(["compact"]);
		expect(rememberedChatSkills("h1", "codex", "p1")).toEqual([]);
		expect(rememberedChatSkills("h2", "claude-code", "p1")).toEqual([]);
	});

	it("ignores an empty list or a chat whose machine or agent is not known", () => {
		rememberChatSkills("h1", "claude-code", "p1", [skill("compact")]);
		rememberChatSkills("h1", "claude-code", "p1", []);
		rememberChatSkills(undefined, "claude-code", "p1", [skill("x")]);
		rememberChatSkills("h1", undefined, "p1", [skill("x")]);
		expect(rememberedChatSkills("h1", "claude-code", "p1").map((item) => item.name)).toEqual(["compact"]);
	});

	it("forgets every list when a machine is forgotten", () => {
		rememberChatSkills("h1", "claude-code", "p1", [skill("compact")]);
		forgetRememberedChatSkills();
		expect(rememberedChatSkills("h1", "claude-code", "p1")).toEqual([]);
	});

	it("drops the least recently used list past its limit", () => {
		for (let index = 0; index < 40; index += 1) rememberChatSkills(`h${index}`, "claude-code", undefined, [skill("compact")]);
		expect(rememberedChatSkills("h0", "claude-code")).toEqual([]);
		expect(rememberedChatSkills("h39", "claude-code")).toHaveLength(1);
	});
});

describe("mergeChatSkills", () => {
	it("keeps the reported order and adds skills only found on disk after it", () => {
		expect(mergeChatSkills([skill("compact"), skill("review")], [skill("review"), skill("ship")]).map((item) => item.name)).toEqual(["compact", "review", "ship"]);
		expect(mergeChatSkills([], [skill("ship")]).map((item) => item.name)).toEqual(["ship"]);
		const reported = [skill("compact")];
		expect(mergeChatSkills(reported, [])).toBe(reported);
		expect(mergeChatSkills(reported, [skill("compact")])).toBe(reported);
	});
});
