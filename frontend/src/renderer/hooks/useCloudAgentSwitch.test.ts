import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { agentSwitchTarget, useCloudAgentSwitch } from "./useCloudAgentSwitch";

const mocks = vi.hoisted(() => ({
	agentSwitchHarnesses: [] as string[],
	start: vi.fn(),
	transition: undefined as unknown,
	starting: false,
	latest: { activityState: "idle", status: "idle" } as { activityState: string; status: string },
	meUserId: "user-1" as string | undefined,
}));

vi.mock("./useCloudCp", () => ({
	useCloudCp: () => ({ client: { getSession: vi.fn(async () => ({ session: mocks.latest })) } }),
}));

vi.mock("./useCloudMeUserId", () => ({ useCloudMeUserId: () => mocks.meUserId }));
vi.mock("./useCloudOffering", () => ({
	useCloudOffering: () => ({ agentSwitchHarnesses: mocks.agentSwitchHarnesses, chatHarnesses: ["claude-code", "codex", "cursor"] }),
}));
vi.mock("./useProviderConnections", () => ({
	useProviderConnections: () => ({ data: [
		{ provider: "claude-code", label: "default", validationState: "valid" },
		{ provider: "opencode", label: "default", validationState: "valid" },
		{ provider: "cursor", label: "default", validationState: "invalid" },
	] }),
}));
vi.mock("./useSessionInterfaceTransition", async (original) => ({
	...await original<typeof import("./useSessionInterfaceTransition")>(),
	useSessionInterfaceTransition: () => ({ transition: mocks.transition, starting: mocks.starting, start: mocks.start }),
}));

const session: WorkspaceSession = {
	id: "session-1", workspaceId: "project-1", workspaceName: "Project", title: "Agent", provider: "codex",
	kind: "worker", mode: "chat", status: "idle", updatedAt: "2026-10-01T00:00:00Z", prs: [], cloud: { orgId: "org-1", createdByUserId: "user-1" },
};

describe("useCloudAgentSwitch", () => {
	beforeEach(() => {
		mocks.agentSwitchHarnesses = ["claude-code", "codex", "cursor", "opencode"];
		mocks.start.mockReset().mockResolvedValue({});
		mocks.transition = undefined;
		mocks.starting = false;
		mocks.latest = { activityState: "idle", status: "idle" };
		mocks.meUserId = "user-1";
	});

	it("offers agents the control plane can switch to and the person is signed in to, with a Chat in Chat", () => {
		const chat = renderHook(() => useCloudAgentSwitch(session)).result.current;
		expect(chat.agents.map((agent) => agent.id)).toEqual(["claude-code", "codex"]);
		expect(chat.available).toBe(true);
		const terminal = renderHook(() => useCloudAgentSwitch({ ...session, mode: "tui" })).result.current;
		expect(terminal.agents.map((agent) => agent.id)).toEqual(["claude-code", "codex", "opencode"]);
	});

	it("offers nothing where the control plane cannot switch, or the session is deleted", () => {
		mocks.agentSwitchHarnesses = [];
		expect(renderHook(() => useCloudAgentSwitch(session)).result.current.available).toBe(false);
		mocks.agentSwitchHarnesses = ["claude-code", "codex"];
		expect(renderHook(() => useCloudAgentSwitch({ ...session, isTerminated: true })).result.current.available).toBe(false);
		expect(renderHook(() => useCloudAgentSwitch(undefined)).result.current.available).toBe(false);
	});

	// The new agent runs on the creator's login for it: the control plane
	// refuses anyone else (403), an org admin included, so nobody else is offered it.
	it("offers the switch only to the session's creator", () => {
		mocks.meUserId = "user-2";
		expect(renderHook(() => useCloudAgentSwitch(session)).result.current.available).toBe(false);
		mocks.meUserId = undefined;
		expect(renderHook(() => useCloudAgentSwitch(session)).result.current.available).toBe(false);
		mocks.meUserId = "user-1";
		expect(renderHook(() => useCloudAgentSwitch({ ...session, cloud: { orgId: "org-1" } })).result.current.available).toBe(false);
		expect(renderHook(() => useCloudAgentSwitch(session)).result.current.available).toBe(true);
	});

	it("starts the switch in the session's interface with the picked model and effort, draining Chat", async () => {
		const { result } = renderHook(() => useCloudAgentSwitch(session));
		await result.current.start({ harness: "claude-code", model: "claude-opus-4-5", shownEffort: "high" });
		expect(mocks.start).toHaveBeenCalledWith({ targetMode: "chat", policy: "drain", targetHarness: "claude-code", model: "claude-opus-4-5", reasoningEffort: "high" });
	});

	it("stops an idle terminal directly, which cannot prove it is idle, and refuses while its agent works", async () => {
		const { result } = renderHook(() => useCloudAgentSwitch({ ...session, mode: "tui" }));
		await result.current.start({ harness: "claude-code" });
		expect(mocks.start).toHaveBeenCalledWith({ targetMode: "tui", policy: "interrupt", targetHarness: "claude-code" });
		mocks.start.mockClear();
		mocks.latest = { activityState: "active", status: "working" };
		await expect(result.current.start({ harness: "claude-code" })).rejects.toThrow("The agent is working");
		expect(mocks.start).not.toHaveBeenCalled();
	});

	it("reports a running agent switch, not an interface switch", () => {
		mocks.transition = { id: "t", phase: "draining", sourceMode: "chat", targetMode: "chat", targetHarness: "claude-code" };
		expect(renderHook(() => useCloudAgentSwitch(session)).result.current).toMatchObject({ switching: true, running: { targetHarness: "claude-code" } });
		mocks.transition = { id: "t", phase: "draining", sourceMode: "chat", targetMode: "tui" };
		expect(renderHook(() => useCloudAgentSwitch(session)).result.current.switching).toBe(false);
		expect(agentSwitchTarget({ targetHarness: "" })).toBeUndefined();
	});
});
