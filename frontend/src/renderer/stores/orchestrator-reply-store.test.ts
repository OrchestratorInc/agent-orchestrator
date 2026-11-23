import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { chatFixtureEmpty } from "../lib/chat-fixture";
import type { ConversationSnapshot } from "../types/conversation";
import type { WorkspaceSession } from "../types/workspace";
import { useOrchestratorReplyState, useOrchestratorReplyStore } from "./orchestrator-reply-store";

const fetchRecentConversation = vi.hoisted(() => vi.fn());
vi.mock("../hooks/useConversation", () => ({ fetchRecentConversation }));

function orchestrator(state: "active" | "idle" | "waiting_input" | "exited", at: string, id = "orch-1", mode: "chat" | "tui" = "chat") {
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
	act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, handledAt: { ":project-a": Date.parse("2026-11-08T09:59:00Z") } }));
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
		act(() => useOrchestratorReplyStore.setState({ replyAt: {}, seenAt: {}, handledAt: {} }));
	});

	it("marks a reply unread when a working orchestrator settles", () => {
		const hook = setup(orchestrator("active", "2026-11-08T10:00:00Z", "orch-1", "tui"));
		expect(hook.result.current).toBe("working");
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:00:05Z", "orch-1", "tui"), viewing: false, projectId: "project-a" });
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
		const hook = setup(orchestrator("active", "2026-11-08T10:00:00Z", "orch-1", "tui"));
		const settled = orchestrator("idle", "2026-11-08T10:00:05Z", "orch-1", "tui");
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
		const hook = setup(orchestrator("active", "2026-11-08T10:00:00Z", "orch-1", "tui"));
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:00:05Z", "orch-1", "tui"), viewing: false, projectId: "project-a" });
		const other = setup(orchestrator("idle", "2026-11-08T10:00:05Z", "orch-1", "tui"), false, "project-b");
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

	it("lights up when a background turn failed on the account", async () => {
		const failed = workerTurn("");
		fetchRecentConversation.mockResolvedValue({ ...failed, turns: failed.turns.map((turn) => ({ ...turn, state: "failed", usageLimit: { autoResume: false } })) });
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("ignores a queued message the user cancelled during the work", async () => {
		const quiet = workerTurn("Noted.");
		fetchRecentConversation.mockResolvedValue({
			...quiet,
			turns: [...quiet.turns, { id: "t-cancelled", state: "cancelled", hasHumanPrompt: true, requestedAt: "2026-11-08T10:05:05Z", completedAt: "2026-11-08T10:05:10Z" }],
		});
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(fetchRecentConversation).toHaveBeenCalledWith("orch-1", undefined));
		await act(async () => {});
		expect(hook.result.current).toBeNull();
	});

	it("stays quiet for background work after the user cancels a queued message", async () => {
		const quiet = workerTurn("Noted.");
		fetchRecentConversation.mockResolvedValue({
			...quiet,
			turns: [...quiet.turns, { id: "t2", state: "completed", requestedAt: "2026-11-08T10:06:00Z", completedAt: "2026-11-08T10:06:30Z" }],
			items: [...quiet.items, ...quiet.items.map((item, i) => ({ ...item, id: `${item.id}-2`, turnId: "t2", sequence: 3 + i }))],
		});
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") } }));
		const wrote = (state: "active" | "idle", at: string) => ({ ...orchestrator(state, at), lastUserMessageAt: "2026-11-08T10:05:10Z" }) as WorkspaceSession;
		const hook = setup(orchestrator("active", "2026-11-08T10:05:00Z"));
		hook.rerender({ orchestrator: wrote("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(fetchRecentConversation).toHaveBeenCalledTimes(1));
		await act(async () => {});
		hook.rerender({ orchestrator: wrote("active", "2026-11-08T10:06:00Z"), viewing: false, projectId: "project-a" });
		hook.rerender({ orchestrator: wrote("idle", "2026-11-08T10:06:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(fetchRecentConversation).toHaveBeenCalledTimes(2));
		await act(async () => {});
		expect(hook.result.current).toBeNull();
	});

	it("checks the turn a button first sees running instead of counting it as the user's answer", async () => {
		fetchRecentConversation.mockResolvedValue(workerTurn("Noted."));
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") } }));
		const missed = (state: "active" | "idle", at: string) => ({ ...orchestrator(state, at), lastUserMessageAt: "2026-11-08T10:01:00Z" }) as WorkspaceSession;
		const hook = setup(missed("active", "2026-11-08T10:05:00Z"));
		hook.rerender({ orchestrator: missed("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(fetchRecentConversation).toHaveBeenCalled());
		await act(async () => {});
		expect(hook.result.current).toBeNull();
	});

	it("shows working for the user's queued message after a background turn they watched settle", () => {
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, handledAt: { ":project-a": Date.parse("2026-11-08T09:59:00Z") } }));
		const sent = (state: "active" | "idle", at: string) => ({ ...orchestrator(state, at), lastUserMessageAt: "2026-11-08T10:05:10Z" }) as WorkspaceSession;
		const hook = setup(orchestrator("idle", "2026-11-08T10:04:00Z"), true);
		hook.rerender({ orchestrator: orchestrator("active", "2026-11-08T10:05:00Z"), viewing: true, projectId: "project-a" });
		hook.rerender({ orchestrator: sent("active", "2026-11-08T10:05:10Z"), viewing: true, projectId: "project-a" });
		hook.rerender({ orchestrator: sent("idle", "2026-11-08T10:05:30Z"), viewing: true, projectId: "project-a" });
		hook.rerender({ orchestrator: sent("active", "2026-11-08T10:05:31Z"), viewing: true, projectId: "project-a" });
		hook.rerender({ orchestrator: sent("active", "2026-11-08T10:05:40Z"), viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBe("working");
	});

	it("takes up the user's message once a check finds none of their turns still waiting", async () => {
		const quiet = workerTurn("Noted.");
		fetchRecentConversation.mockResolvedValue({
			...quiet,
			turns: [...quiet.turns, { id: "t-yours", state: "completed", hasHumanPrompt: true, requestedAt: "2026-11-08T10:05:10Z", completedAt: "2026-11-08T10:05:50Z" }],
			items: [
				...quiet.items,
				{ kind: "message", id: "u1", turnId: "t-yours", sequence: 3, revision: 0, role: "user", origin: "human", streaming: false, createdAt: "2026-11-08T10:05:10Z", text: "Merge it?" },
				{ kind: "message", id: "a1", turnId: "t-yours", sequence: 4, revision: 0, role: "assistant", origin: "provider", streaming: false, createdAt: "2026-11-08T10:05:50Z", text: "Merged." },
			],
		});
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, handledAt: { ":project-a": Date.parse("2026-11-08T09:59:00Z") } }));
		const sent = (state: "active" | "idle", at: string) => ({ ...orchestrator(state, at), lastUserMessageAt: "2026-11-08T10:05:10Z" }) as WorkspaceSession;
		const hook = setup(orchestrator("idle", "2026-11-08T10:04:00Z"));
		hook.rerender({ orchestrator: orchestrator("active", "2026-11-08T10:05:00Z"), viewing: false, projectId: "project-a" });
		hook.rerender({ orchestrator: sent("active", "2026-11-08T10:05:20Z"), viewing: false, projectId: "project-a" });
		hook.rerender({ orchestrator: sent("idle", "2026-11-08T10:05:50Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
		hook.rerender({ orchestrator: sent("active", "2026-11-08T10:07:00Z"), viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBe("unread");
	});

	it("leaves a queued message pending when a button mounted after the work began", async () => {
		const quiet = workerTurn("Noted.");
		fetchRecentConversation.mockResolvedValue({ ...quiet, turns: [...quiet.turns, { id: "t-yours", state: "queued", hasHumanPrompt: true, requestedAt: "2026-11-08T10:05:00Z" }] });
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, handledAt: { ":project-a": Date.parse("2026-11-08T09:59:00Z") } }));
		const sent = (state: "active" | "idle", at: string) => ({ ...orchestrator(state, at), lastUserMessageAt: "2026-11-08T10:05:00Z" }) as WorkspaceSession;
		const hook = setup(sent("active", "2026-11-08T10:05:03Z"));
		hook.rerender({ orchestrator: sent("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(fetchRecentConversation).toHaveBeenCalled());
		await act(async () => {});
		hook.rerender({ orchestrator: sent("active", "2026-11-08T10:05:31Z"), viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBe("working");
	});

	it("lights up when the user's own turn settles", async () => {
		fetchRecentConversation.mockResolvedValue({
			...chatFixtureEmpty,
			turns: [{ id: "t0", state: "completed", hasHumanPrompt: true, requestedAt: "2026-11-08T09:59:00Z", completedAt: "2026-11-08T10:00:05Z" }],
			items: [
				{ kind: "message", id: "u0", turnId: "t0", sequence: 1, revision: 0, role: "user", origin: "human", streaming: false, createdAt: "2026-11-08T09:59:00Z", text: "Merge it?" },
				{ kind: "message", id: "a0", turnId: "t0", sequence: 2, revision: 0, role: "assistant", origin: "provider", streaming: false, createdAt: "2026-11-08T10:00:05Z", text: "Merged." },
			],
		});
		const hook = setup(orchestrator("active", "2026-11-08T10:00:00Z"));
		expect(hook.result.current).toBe("working");
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:00:05Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("lights up when worker traffic leaves a question for the user", async () => {
		fetchRecentConversation.mockResolvedValue(workerTurn("Tunnel or loopback?"));
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("lights up when the turn that just ended is not recorded yet", async () => {
		const quiet = workerTurn("Noted.");
		fetchRecentConversation.mockResolvedValue({ ...quiet, turns: [...quiet.turns, { id: "t2", state: "running", requestedAt: "2026-11-08T10:05:40Z" }] });
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:50Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("lights up when a turn that began during the work is still marked running", async () => {
		const quiet = workerTurn("Noted.");
		fetchRecentConversation.mockResolvedValue({ ...quiet, turns: [...quiet.turns, { id: "t2", state: "running", requestedAt: "2026-11-08T10:04:50Z", startedAt: "2026-11-08T10:05:20Z" }] });
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("stays quiet when the only running turn began after the work settled", async () => {
		const quiet = workerTurn("Noted.");
		fetchRecentConversation.mockResolvedValue({ ...quiet, turns: [...quiet.turns, { id: "t2", state: "running", requestedAt: "2026-11-08T10:05:31Z" }] });
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(fetchRecentConversation).toHaveBeenCalled());
		await act(async () => {});
		expect(hook.result.current).toBeNull();
	});

	it("checks a background turn the user wrote during, instead of counting it as their answer", async () => {
		fetchRecentConversation.mockResolvedValue(workerTurn("Noted."));
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") } }));
		const wrote = (state: "active" | "idle", at: string) => ({ ...orchestrator(state, at), lastUserMessageAt: "2026-11-08T10:05:10Z" }) as WorkspaceSession;
		const hook = setup(orchestrator("active", "2026-11-08T10:05:00Z"));
		hook.rerender({ orchestrator: wrote("active", "2026-11-08T10:05:10Z"), viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBe("working");
		hook.rerender({ orchestrator: wrote("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(fetchRecentConversation).toHaveBeenCalled());
		await act(async () => {});
		expect(hook.result.current).toBeNull();
	});

	it("keeps showing working when the user's queued turn starts right after a background turn", async () => {
		let resolve!: (value: ConversationSnapshot) => void;
		fetchRecentConversation.mockReturnValue(new Promise<ConversationSnapshot>((done) => { resolve = done; }));
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") } }));
		const wrote = (state: "active" | "idle", at: string) => ({ ...orchestrator(state, at), lastUserMessageAt: "2026-11-08T10:05:10Z" }) as WorkspaceSession;
		const hook = setup(orchestrator("active", "2026-11-08T10:05:00Z"));
		hook.rerender({ orchestrator: wrote("active", "2026-11-08T10:05:10Z"), viewing: false, projectId: "project-a" });
		hook.rerender({ orchestrator: wrote("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(fetchRecentConversation).toHaveBeenCalled());
		hook.rerender({ orchestrator: wrote("active", "2026-11-08T10:05:31Z"), viewing: false, projectId: "project-a" });
		const quiet = workerTurn("Noted.");
		await act(async () => resolve({ ...quiet, turns: [...quiet.turns, { id: "t2", state: "running", hasHumanPrompt: true, requestedAt: "2026-11-08T10:05:10Z" }] }));
		expect(hook.result.current).toBe("working");
	});

	it("does not light a reply the user opened the chat for while it was being checked", async () => {
		let resolve!: (value: ConversationSnapshot) => void;
		fetchRecentConversation.mockReturnValue(new Promise<ConversationSnapshot>((done) => { resolve = done; }));
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(fetchRecentConversation).toHaveBeenCalled());
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: true, projectId: "project-a" });
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await act(async () => resolve(workerTurn("Tunnel or loopback?")));
		expect(hook.result.current).toBeNull();
	});

	it("lights up when the recent page does not reach back to when work began", async () => {
		fetchRecentConversation.mockResolvedValue({ ...workerTurn("Noted."), hasMoreBefore: true });
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") } }));
		const hook = setup(orchestrator("active", "2026-11-08T10:04:00Z"));
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("checks an unsure span again at the next settle when work restarted before the check came back", async () => {
		let resolve!: (value: ConversationSnapshot) => void;
		const asked = workerTurn("Tunnel or loopback?");
		fetchRecentConversation.mockReturnValueOnce(new Promise<ConversationSnapshot>((done) => { resolve = done; }));
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(fetchRecentConversation).toHaveBeenCalledTimes(1));
		hook.rerender({ orchestrator: orchestrator("active", "2026-11-08T10:05:40Z"), viewing: false, projectId: "project-a" });
		await act(async () => resolve({ ...asked, turns: [{ ...asked.turns[0]!, state: "running", completedAt: undefined }] }));
		expect(hook.result.current).toBeNull();
		const later = workerTurn("Noted.").items.map((item, index) => ({ ...item, id: `later-${index}`, turnId: "t2", sequence: 3 + index, createdAt: "2026-11-08T10:05:40Z" }));
		fetchRecentConversation.mockResolvedValueOnce({
			...asked,
			turns: [...asked.turns, { id: "t2", state: "completed", requestedAt: "2026-11-08T10:05:40Z", completedAt: "2026-11-08T10:06:00Z" }],
			items: [...asked.items, ...later],
		});
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:06:00Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("checks each start time a button saw, so one mounted late does not narrow the window", async () => {
		const asked = workerTurn("Tunnel or loopback?");
		const quiet = workerTurn("Noted.").items.map((item, index) => ({ ...item, id: `quiet-${index}`, turnId: "t2", sequence: 3 + index, createdAt: "2026-11-08T10:05:30Z" }));
		fetchRecentConversation.mockResolvedValue({
			...asked,
			turns: [{ ...asked.turns[0]!, completedAt: "2026-11-08T10:05:10Z" }, { id: "t2", state: "completed", requestedAt: "2026-11-08T10:05:30Z", completedAt: "2026-11-08T10:05:40Z" }],
			items: [...asked.items, ...quiet],
		});
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") } }));
		const early = setup(orchestrator("active", "2026-11-08T10:05:00Z"));
		const late = setup(orchestrator("active", "2026-11-08T10:05:20Z"));
		late.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:50Z"), viewing: false, projectId: "project-a" });
		early.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:50Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(early.result.current).toBe("unread"));
	});

	it("lights up when a worker's steer carried in a report that still needs the user", async () => {
		const quiet = workerTurn("Noted.");
		fetchRecentConversation.mockResolvedValue({
			...quiet,
			items: [
				quiet.items[0]!,
				{ kind: "activity", id: "steer-1", turnId: "t1", sequence: 2, revision: 0, activityKind: "system", status: "completed", summary: "Steer", createdAt: "2026-11-08T10:05:10Z", detail: { event: "steer", senderSessionId: "proj-7", text: "Rebased." } },
				{ ...quiet.items[0]!, id: "carried", turnId: "t-report", sequence: 3, text: "Reports since your previous turn:\n\n[needs_input] [iOS pages](ao://sessions/proj/proj-286)\nTunnel or loopback?" },
				{ ...quiet.items[1]!, sequence: 4 },
			],
			batchedDeliveries: [{ turnId: "t-report", deliveredWithTurnId: "t1", steerActivityId: "steer-1" }],
		});
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("lights up when a report drained with a worker turn still needs the user", async () => {
		const quiet = workerTurn("Noted.");
		fetchRecentConversation.mockResolvedValue({
			...quiet,
			items: [
				quiet.items[0]!,
				{ ...quiet.items[0]!, id: "drained", turnId: "t-report", sequence: 2, text: "Reports since your previous turn:\n\n[needs_input] [iOS pages](ao://sessions/proj/proj-286)\nTunnel or loopback?" },
				{ ...quiet.items[1]!, sequence: 3 },
			],
			batchedDeliveries: [{ turnId: "t-report", deliveredWithTurnId: "t1" }],
		});
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("shows working for a message the user sent just before a button mounted", () => {
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") } }));
		const sent = (state: "active" | "idle", at: string) => ({ ...orchestrator(state, at), lastUserMessageAt: "2026-11-08T10:01:00Z" }) as WorkspaceSession;
		const hook = setup(sent("idle", "2026-11-08T10:00:30Z"));
		hook.rerender({ orchestrator: sent("active", "2026-11-08T10:01:02Z"), viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBe("working");
	});

	it("lights up when the settled turns cannot be read", async () => {
		fetchRecentConversation.mockRejectedValue(new Error("offline"));
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		await waitFor(() => expect(hook.result.current).toBe("unread"));
	});

	it("lights up when the orchestrator waits for the user, whatever settled before", () => {
		fetchRecentConversation.mockResolvedValue(workerTurn("Noted."));
		const hook = afterYourReply();
		hook.rerender({ orchestrator: orchestrator("waiting_input", "2026-11-08T10:05:30Z"), viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBe("unread");
		expect(fetchRecentConversation).not.toHaveBeenCalled();
	});

	it("keeps a terminal orchestrator lighting for every turn", () => {
		act(() => useOrchestratorReplyStore.setState({ replyAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") }, seenAt: { ":project-a": Date.parse("2026-11-08T10:00:00Z") } }));
		const hook = setup(orchestrator("active", "2026-11-08T10:05:00Z", "orch-1", "tui"));
		expect(hook.result.current).toBe("working");
		hook.rerender({ orchestrator: orchestrator("idle", "2026-11-08T10:05:30Z", "orch-1", "tui"), viewing: false, projectId: "project-a" });
		expect(hook.result.current).toBe("unread");
		expect(fetchRecentConversation).not.toHaveBeenCalled();
	});

	it("persists replies and seen times", () => {
		act(() => useOrchestratorReplyStore.getState().recordReply(":project-a", 5));
		expect(JSON.parse(window.localStorage.getItem("ao.orchestrator.replies") ?? "{}")).toEqual({
			replyAt: { ":project-a": 5 },
			seenAt: {},
			handledAt: {},
		});
	});
});
