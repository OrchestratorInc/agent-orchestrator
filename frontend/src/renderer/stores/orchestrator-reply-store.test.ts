import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { useOrchestratorReplyState, useOrchestratorReplyStore } from "./orchestrator-reply-store";

function orchestrator(state: "active" | "idle" | "waiting_input" | "exited", at: string, id = "orch-1") {
	return { id, kind: "orchestrator", activity: { state, lastActivityAt: at } } as unknown as WorkspaceSession;
}

function setup(initial: WorkspaceSession, viewing = false, projectId = "project-a") {
	return renderHook(
		(props: { orchestrator: WorkspaceSession; viewing: boolean; projectId: string }) => useOrchestratorReplyState(props),
		{ initialProps: { orchestrator: initial, viewing, projectId } },
	);
}

describe("useOrchestratorReplyState", () => {
	beforeEach(() => {
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

	it("persists replies and seen times", () => {
		act(() => useOrchestratorReplyStore.getState().recordReply(":project-a", 5));
		expect(JSON.parse(window.localStorage.getItem("ao.orchestrator.replies") ?? "{}")).toEqual({
			replyAt: { ":project-a": 5 },
			seenAt: {},
		});
	});
});
