import { beforeEach, describe, expect, it } from "vitest";
import type { ChatSkill } from "../types/conversation";
import { readChatSkills, rememberChatSkills, REMEMBERED_SKILL_PROJECTS } from "./chat-settings-template";

const skill = (name: string): ChatSkill => ({ name, displayName: name, source: "agent" });

describe("remembered chat skills", () => {
	beforeEach(() => window.localStorage.clear());

	it("prefers the project's own list over the agent-wide one", () => {
		rememberChatSkills("claude-code", undefined, [skill("repo-a")], "project-a");
		rememberChatSkills("claude-code", undefined, [skill("repo-b")], "project-b");

		expect(readChatSkills("claude-code", undefined, "project-a")).toEqual([skill("repo-a")]);
		expect(readChatSkills("claude-code", undefined, "project-b")).toEqual([skill("repo-b")]);
		// A new chat in a project no session has run in yet gets the latest list.
		expect(readChatSkills("claude-code", undefined, "project-c")).toEqual([skill("repo-b")]);
		expect(readChatSkills("claude-code")).toEqual([skill("repo-b")]);
	});

	it("keeps each agent and host apart", () => {
		rememberChatSkills("claude-code", undefined, [skill("claude")], "project-a");
		rememberChatSkills("codex", undefined, [skill("codex")], "project-a");
		rememberChatSkills("codex", "host-1", [skill("remote")], "project-a");

		expect(readChatSkills("claude-code", undefined, "project-a")).toEqual([skill("claude")]);
		expect(readChatSkills("codex", undefined, "project-a")).toEqual([skill("codex")]);
		expect(readChatSkills("codex", "host-1", "project-a")).toEqual([skill("remote")]);
	});

	it("keeps only the most recently used projects' lists", () => {
		for (let index = 0; index <= REMEMBERED_SKILL_PROJECTS; index += 1) {
			rememberChatSkills("claude-code", undefined, [skill(`repo-${index}`)], `project-${index}`);
		}
		// The oldest project's list was dropped; it falls back to the agent-wide one.
		expect(readChatSkills("claude-code", undefined, "project-0")).toEqual([skill(`repo-${REMEMBERED_SKILL_PROJECTS}`)]);
		expect(readChatSkills("claude-code", undefined, "project-1")).toEqual([skill("repo-1")]);
	});
});
