import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { agentSwitchTarget, useCloudAgentSwitch } from "./useCloudAgentSwitch";

const mocks = vi.hoisted(() => ({
	agentSwitchHarnesses: [] as string[],
	start: vi.fn(),
	transition: undefined as unknown,
	starting: false,
}));

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
	kind: "worker", mode: "chat", status: "idle", updatedAt: "2026-10-01T00:00:00Z", prs: [], cloud: { orgId: "org-1" },
};

describe("useCloudAgentSwitch", () => {
	beforeEach(() => {
		mocks.agentSwitchHarnesses = ["claude-code", "codex", "cursor", "opencode"];
		mocks.start.mockReset().mockResolvedValue({});
		mocks.transition = undefined;
		mocks.starting = false;
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

	it("starts the switch in the session's interface with the picked model and effort", async () => {
		const { result } = renderHook(() => useCloudAgentSwitch({ ...session, mode: "tui" }));
		await result.current.start({ harness: "claude-code", model: "claude-opus-4-5", shownEffort: "high" });
		expect(mocks.start).toHaveBeenCalledWith({ targetMode: "tui", policy: "drain", targetHarness: "claude-code", model: "claude-opus-4-5", reasoningEffort: "high" });
	});

	it("reports a running agent switch, not an interface switch", () => {
		mocks.transition = { id: "t", phase: "draining", sourceMode: "chat", targetMode: "chat", targetHarness: "claude-code" };
		expect(renderHook(() => useCloudAgentSwitch(session)).result.current).toMatchObject({ switching: true, running: { targetHarness: "claude-code" } });
		mocks.transition = { id: "t", phase: "draining", sourceMode: "chat", targetMode: "tui" };
		expect(renderHook(() => useCloudAgentSwitch(session)).result.current.switching).toBe(false);
		expect(agentSwitchTarget({ targetHarness: "" })).toBeUndefined();
	});
});
