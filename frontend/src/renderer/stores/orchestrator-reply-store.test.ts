import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { chatFixtureEmpty } from "../lib/chat-fixture";
import type { ConversationSnapshot } from "../types/conversation";
import type { WorkspaceSession } from "../types/workspace";
import { useOrchestratorReplyState, useOrchestratorReplyStore } from "./orchestrator-reply-store";

const fetchRecentConversation = vi.hoisted(() => vi.fn());
vi.mock("../hooks/useConversation", () => ({ fetchRecentConversation }));

function orchestrator(state: "active" | "idle" | "waiting_input" | "exited", at: string, id = "orch-1", mode: "chat" | "terminal" = "chat") {
	return { id, kind: "orchestrator", mode, lastUserMessageAt: "2026-11-08T09:59:00Z", activity: { state, lastActivityAt: at } } as unknown as WorkspaceSession;
}

function workerTurn(reply: string): ConversationSnapshot {
	return {
		...chatFixtureEmpty,
		turns: [{ id: "t1", state: "completed", requestedAt: "2026-11-08T10:05:00Z", completedAt: "2026-11-08T10:05:30Z" }],
		items: [
			{ kind: "message", id: "m1", turnId: "t1", sequence: 1, revision: 0, role: "user", origin: "automation", streaming: false, createdAt: "2026-11-08T10:05:00Z", text: "Reports since your previous turn:\n\n[done] [Menus](ao://sessions/proj/proj-285)\nMenus render." },
			{ kind: "message", id: "m2", turnId: "t1", sequence: 2, revision: 0, role: "assistant", origin: "provider", streaming: false, createdAt: "2026-11-08T10:05:30Z", text: reply },
		],
	};
}

function afterYourReply() {
	act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") } }));
	const hook = setup(orchestrator("active", "2026-11-08T10:05:00Z"));
	expect(hook.result.current).toBeNull();
	return hook;
}

function setup(initial: WorkspaceSession, viewing = false, projectId = "project-a") {
	return renderHook(
		(props: { orchestrator: WorkspaceSession; viewing: boolean; projectId: string }) => useOrchestratorReplyState(props),
		{ initialProps: { orchestrator: initial, viewing, projectId } },
	);
}

describe("useOrchestratorReplyState", () => {
	beforeEach(() => {
		fetchRecentConversation.mockReset();
		window.localStorage.clear();
		act(() => useOrchestratorReplyStore.setState({ replyAt: {}, seenAt: {} }));
	});

	it("marks a reply unread when a working orchestrator settles", () => {
		const hook = setup(orchestrator("active", "2026-11-08T10:00:00Z"));
		expect(hook.result.current).toBe("working");
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:00:05Z"), viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBe("unread");
	});

	it("ignores idle resets that no observed turn preceded", () => {
		const hook = setup(orchestrator("idle", "2026-11-08T10:00:00Z"));
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T11:00:00Z"), viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBeNull();
	});

	it("does not count an orchestrator that exits mid-turn", () => {
		const hook = setup(orchestrator("active", "2026-11-08T10:00:00Z"));
		hook.rerender({ orchestrator: orchestrator("exited", "2026-11-08T10:00:05Z"), viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBeNull();
	});

	it("clears when viewed and stays cleared afterwards", () => {
		const hook = setup(orchestrator("active", "2026-11-08T10:00:00Z"));
		const settled = orchestrator("idle", "2026-11-08T10:00:05Z");
		hook.rerender({ orchestrator: settled, viewing: false, projectId: "project-a" });
		hook.rerender({ orchestrator: settled, viewing: true, projectId: "project-a" });
		hook.rerender({ orchestrator: settled, viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBeNull();
	});

	it("treats a reply that lands while viewing as seen", () => {
		const hook = setup(orchestrator("active", "2026-11-08T10:00:00Z"), true);
		const settled = orchestrator("idle", "2026-11-08T10:00:05Z");
		hook.rerender({ orchestrator: settled, viewing: true, projectId: "project-a" });
		hook.rerender({ orchestrator: settled, viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBeNull();
	});

	it("keeps unread state per project", () => {
		const hook = setup(orchestrator("active", "2026-11-08T10:00:00Z"));
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:00:05Z"), viewing: false, projectId: "project-a" });
		const other = setup(orchestrator("idle", "2026-11-08T10:00:05Z"), false, "project-b");
		expect(hook.result.current).toBe("unread");
		expect(other.result.current).toBeNull();
	});

	it("stays quiet through worker traffic the orchestrator handled on its own", async () => {
		fetchRecentConversation.mockResolvedValue(workerTurn("Noted."));
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(fetchRecentConversation).toHaveBeenCalledWith("orch-1", undefined));
		await act(async () => {});
		expect(hook.result.current).toBeNull();
	});

	it("lights up when worker traffic leaves a question for the user", async () => {
		fetchRecentConversation.mockResolvedValue(workerTurn("Tunnel or loopback?"));
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("lights up when the settled turns cannot be read", async () => {
		fetchRecentConversation.mockRejectedValue(new Error("offline"));
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("keeps a terminal orchestrator lighting for every turn", () => {
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") } }));
		const hook = setup(orchestrator("active", "2026-11-08T10:05:00Z", "orch-1", "terminal"));
		expect(hook.result.current).toBe("working");
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z", "orch-1", "terminal"), viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBe("unread");
		expect(fetchRecentConversation).not.toHaveBeenCalled();
	});

	it("persists replies and seen times", () => {
		act(() => useOrchestratorReplyStore.getState().recordReply(":project-a", 5));
		expect(JSON.parse(window.localStorage.getItem("ao.orchestrator.replies") ?? "{}")).toEqual({
			replyAt: { ":project-a": 5 },
			seenAt: {},
		});
	});
});
