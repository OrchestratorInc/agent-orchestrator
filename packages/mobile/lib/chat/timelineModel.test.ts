import { describe, expect, it } from "vitest";
import { activityHierarchy, activityNodesRunning, activityStartsExpanded, canRollbackTurn, countActivityNodes, editableHumanTurns, groupConversationByTurn, sameEditableTurns, partitionTurnItems, readableConversationItems, settledTurnItems, summarizeActivities } from "./timelineModel";
import * as timelineModel from "./timelineModel";
import type { ConversationActivity, ConversationItem, ConversationSnapshot, ConversationTurn } from "./types";

function snapshot(): ConversationSnapshot {
	return {
		conversationId: "c", sessionId: "s", harness: "codex", mode: "chat", controller: { state: "ready" },
		latestSequence: 4, oldestSequence: 1, hasMoreBefore: false, settings: {}, capabilities: ["rollback"],
		turns: [
			{ id: "t1", state: "completed", providerTurnId: "p1", requestedAt: "2026-08-05T00:00:00Z" },
			{ id: "t2", state: "completed", providerTurnId: "p2", requestedAt: "2026-08-05T00:00:01Z" },
		],
		items: [
			{ kind: "message", id: "u1", turnId: "t1", sequence: 1, revision: 1, role: "user", origin: "human", text: "First task", streaming: false, createdAt: "" },
			{ kind: "message", id: "u2", turnId: "t2", sequence: 2, revision: 1, role: "user", origin: "human", text: "Queued task", streaming: false, createdAt: "" },
			{ kind: "message", id: "a1", turnId: "t1", sequence: 3, revision: 1, role: "assistant", origin: "provider", text: "First answer", streaming: false, createdAt: "" },
			{ kind: "message", id: "a2", turnId: "t2", sequence: 4, revision: 1, role: "assistant", origin: "provider", text: "Queued answer", streaming: false, createdAt: "" },
		],
	};
}

describe("mobile Chat timeline model", () => {
	it("does not offer rollback on a hibernated provider", () => {
		const value = snapshot();
		value.controller.state = "hibernated";
		expect(canRollbackTurn(value, value.turns[0])).toBe(false);
	});
	it("keeps the empty state outside the conversation list", () => {
		const value = snapshot();
		value.items = [];
		value.turns = [];
		const buildPlan = (
			timelineModel as unknown as {
				conversationTimelineRenderPlan?: (snapshot: ConversationSnapshot) => {
					kind: "empty" | "list";
					groups: unknown[];
				};
			}
		).conversationTimelineRenderPlan;

		expect(buildPlan?.(value)).toEqual({ kind: "empty", groups: [] });
	});

	// Never inverted, on either platform: see conversationTimelineRenderPlan.
	it("renders exchanges in reading order", () => {
		const plan = timelineModel.conversationTimelineRenderPlan(snapshot());
		expect(plan).toMatchObject({ kind: "list" });
		expect(plan.groups.map((group) => group.key)).toEqual(["turn-t1", "turn-t2"]);
	});

	it("keeps queued questions with their own answers instead of strict-sequence interleaving", () => {
		const groups = groupConversationByTurn(snapshot());
		expect(groups.map((group) => group.items.map((item) => item.id))).toEqual([["u1", "a1"], ["u2", "a2"]]);
	});

	it("keys a loaded turn by durable identity rather than its current page boundary", () => {
		const value = snapshot();
		value.items = value.items.filter((item) => item.id !== "u1");
		expect(groupConversationByTurn(value).find((group) => group.turnId === "t1")?.key).toBe("turn-t1");
		value.items.unshift(snapshot().items[0]);
		expect(groupConversationByTurn(value).find((group) => group.turnId === "t1")?.key).toBe("turn-t1");
	});

	it("filters usage, reasoning and duplicate plan rows without hiding unknown work", () => {
		const value = snapshot();
		value.turns[0].plan = { steps: [{ text: "Do it", status: "pending" }] };
		value.items.push(
			activity("usage", 5, "t1"), activity("reasoning", 6, "t1"), activity("plan", 7, "t1"), activity("system", 8, "t1"),
		);
		expect(readableConversationItems(value).slice(-1)[0]).toMatchObject({ activityKind: "system" });
	});

	it("drops model reroute notes, which the model picker already names", () => {
		const value = snapshot();
		value.items.push({ ...activity("system", 5, "t1"), id: "rerouted", detail: { event: "model.rerouted", fromModel: "sonnet", toModel: "claude-sonnet-5-5" } });
		expect(readableConversationItems(value).map((item) => item.id)).not.toContain("rerouted");
	});

	// Claude keeps one task list across turns and can revise it after a turn ends,
	// when the revision belongs to no turn. The daemon marks every older copy
	// superseded, so only the newest shows.
	it("hides plan rows the daemon marked superseded", () => {
		const value = snapshot();
		const row = (sequence: number, superseded: boolean): ConversationActivity => ({
			...activity("plan", sequence),
			detail: { event: "plan", spansTurns: true, superseded, steps: [{ text: "Render", status: "completed" }] },
		});
		value.items.push(row(5, true), row(6, false));
		const planRows = groupConversationByTurn(value).flatMap((group) => group.items).filter((item) => item.kind === "activity" && item.activityKind === "plan");
		expect(planRows.map((item) => item.id)).toEqual(["plan-6"]);
	});

	// Provider turns AO never correlated each carry their own plan row. Without the
	// cross-turn flag those are separate plans, not revisions of one.
	it("keeps each plan row that is not a copy of a cross-turn list", () => {
		const value = snapshot();
		const row = (sequence: number, text: string): ConversationActivity => ({
			...activity("plan", sequence),
			detail: { event: "plan", steps: [{ text, status: "completed" }] },
		});
		value.items.push(row(5, "Read"), row(6, "Write"));
		const planRows = groupConversationByTurn(value).flatMap((group) => group.items).filter((item) => item.kind === "activity" && item.activityKind === "plan");
		expect(planRows.map((item) => item.id)).toEqual(["plan-5", "plan-6"]);
	});

	it("docks queued human messages instead of rendering them as sent timeline turns", () => {
		const value = snapshot();
		value.turns[1].state = "queued";
		value.items = value.items.filter((item) => item.id !== "a2");
		const queuedMessages = (
			timelineModel as unknown as {
				queuedConversationMessages?: (snapshot: ConversationSnapshot) => Array<{ turnId: string; message: { text: string } }>;
			}
		).queuedConversationMessages;

		expect(queuedMessages?.(value)).toMatchObject([
			{ turnId: "t2", message: { text: "Queued task" } },
		]);
		expect(readableConversationItems(value).map((item) => item.id)).not.toContain("u2");
	});

	it("shows a starting session's opening brief as sent instead of queued", () => {
		const value = snapshot();
		value.turns = [{ id: "t1", state: "queued", requestedAt: "2026-08-05T00:00:00Z" }];
		value.items = value.items.filter((item) => item.id === "u1");
		expect(timelineModel.queuedConversationMessages(value)).toHaveLength(1);
		expect(timelineModel.queuedConversationMessages(value, "t1")).toEqual([]);
		expect(readableConversationItems(value).map((item) => item.id)).toEqual([]);
		expect(readableConversationItems(value, "t1").map((item) => item.id)).toEqual(["u1"]);
	});

	it("always places a visible setup checklist, standalone when no turn hosts it", () => {
		const groups = [{ turnId: "t1" }, { turnId: "t2" }];
		expect(timelineModel.startupPlacement(groups, "t1", true)).toBe("turn");
		expect(timelineModel.startupPlacement(groups, "t9", true)).toBe("standalone");
		expect(timelineModel.startupPlacement(groups, undefined, true)).toBe("standalone");
		expect(timelineModel.startupPlacement(groups, "t1", false)).toBe("none");
	});

	it("gates rollback on the daemon capability, accepted history and idle state", () => {
		const value = snapshot();
		expect(canRollbackTurn(value, value.turns[0])).toBe(true);
		value.turns[1].state = "running";
		expect(canRollbackTurn(value, value.turns[0])).toBe(false);
		value.turns[1].state = "completed";
		value.capabilities = [];
		expect(canRollbackTurn(value, value.turns[0])).toBe(false);
	});

	it("opens failed and live-output activities but keeps settled mechanics collapsed", () => {
		const running = activity("command", 1, "t1");
		running.status = "running";
		running.detail = { output: "tick" };
		expect(activityStartsExpanded(running)).toBe(true);
		running.status = "completed";
		expect(activityStartsExpanded(running)).toBe(false);
		running.status = "failed";
		expect(activityStartsExpanded(running)).toBe(true);
		running.status = "cancelled";
		expect(activityStartsExpanded(running)).toBe(false);
	});

	it("builds nested provider work without hiding or looping malformed events", () => {
		const parent = activity("command", 1, "t1");
		parent.providerItemId = "parent";
		const child = activity("command", 2, "t1");
		child.providerItemId = "child";
		child.detail = { parentProviderItemId: "parent" };
		const orphan = activity("command", 3, "t1");
		orphan.detail = { parentProviderItemId: "missing" };
		const roots = activityHierarchy([parent, child, orphan]);
		expect(roots.map((node) => node.activity.id)).toEqual([parent.id, orphan.id]);
		expect(roots[0].children[0].activity.id).toBe(child.id);
		expect(countActivityNodes(roots)).toBe(3);
		child.status = "running";
		expect(activityNodesRunning(roots)).toBe(true);

		parent.detail = { parentProviderItemId: "child" };
		expect(activityHierarchy([parent, child])).toHaveLength(2);
		expect(countActivityNodes(activityHierarchy([parent, child]))).toBe(2);
	});
});

function activity(kind: ConversationActivity["activityKind"], sequence: number, turnId?: string): ConversationActivity {
	return { kind: "activity", id: `${kind}-${sequence}`, turnId, sequence, revision: 1, activityKind: kind, status: "completed", summary: kind, createdAt: "" };
}

describe("activity run summary", () => {
	it("labels consecutive AO actions as actions", () => {
		const renamed = { ...activity("ao_action", 1), summary: "Renamed a session" };
		const switched = { ...activity("ao_action", 2), summary: "Switched agent" };
		expect(summarizeActivities([renamed, switched])).toBe("Performed 2 actions");
	});

	it("counts AO actions separately from commands", () => {
		const command = { ...activity("command", 1), summary: "npm test" };
		const renamed = { ...activity("ao_action", 2), summary: "Renamed a session" };
		expect(summarizeActivities([command, renamed])).toBe("Ran 1 command, 1 action");
	});
});

describe("partitionTurnItems", () => {
	const message = (id: string, sequence: number, role: "user" | "assistant", text: string): ConversationItem => ({ kind: "message", id, turnId: "t", sequence, revision: 1, role, origin: role === "user" ? "human" : "provider", text, streaming: false, createdAt: "" });
	const activity = (id: string, sequence: number, extra: Partial<ConversationActivity> = {}): ConversationItem => ({ kind: "activity", id, turnId: "t", sequence, revision: 1, activityKind: "command", status: "completed", summary: id, createdAt: "", ...extra } as ConversationActivity);
	const turn = (state: ConversationTurn["state"]): ConversationTurn => ({ id: "t", state, requestedAt: "2026-08-05T00:00:00Z" });
	const ids = (items: ConversationItem[]) => items.map((item) => item.id);

	it("folds work behind the final reply once the turn settles", () => {
		const items = [message("u", 1, "user", "go"), activity("ls", 2), message("note", 3, "assistant", "Looking"), activity("cat", 4), message("done", 5, "assistant", "All done")];
		const part = partitionTurnItems(items, turn("completed"))!;
		expect(part.settled).toBe(true);
		expect(ids(part.prompt)).toEqual(["u"]);
		expect(ids(part.work)).toEqual(["ls", "note", "cat"]);
		expect(ids(part.final)).toEqual(["done"]);
	});

	it("keeps a running turn's work inline with no final reply", () => {
		const items = [message("u", 1, "user", "go"), activity("ls", 2), message("note", 3, "assistant", "Looking")];
		const part = partitionTurnItems(items, turn("running"))!;
		expect(part.settled).toBe(false);
		expect(ids(part.work)).toEqual(["ls", "note"]);
		expect(part.final).toEqual([]);
	});

	it("keeps errors and warnings out of the fold, and leaves later steers inside it", () => {
		const items = [message("u", 1, "user", "go"), activity("ls", 2), activity("steer", 3, { activityKind: "system", detail: { event: "steer" } }), activity("err", 4, { activityKind: "error" }), activity("warn", 5, { activityKind: "system", detail: { event: "auth.reauth_required" } }), message("done", 6, "assistant", "ok")];
		const part = partitionTurnItems(items, turn("failed"))!;
		expect(ids(part.work)).toEqual(["ls", "steer"]);
		expect(ids(part.notices)).toEqual(["err", "warn"]);
	});

	it("drops blank assistant messages so the accordion is never empty", () => {
		const items = [message("u", 1, "user", "go"), message("blank", 2, "assistant", "  "), message("done", 4, "assistant", "ok")];
		expect(partitionTurnItems(items, turn("completed"))!.work).toEqual([]);
	});

	it("folds approval and user-input cards with the work instead of dropping them", () => {
		const approval = activity("ask", 2, { activityKind: "approval", status: "pending" });
		const input = activity("question", 3, { activityKind: "user_input", status: "completed" });
		const items = [message("u", 1, "user", "go"), approval, input, message("done", 4, "assistant", "ok")];
		expect(partitionTurnItems(items, turn("interrupted"))!.work).toEqual([approval, input]);
	});

	it("leaves queued and unrecorded turns flat", () => {
		expect(partitionTurnItems([message("u", 1, "user", "go")], turn("queued"))).toBeUndefined();
		expect(partitionTurnItems([message("u", 1, "user", "go")], undefined)).toBeUndefined();
	});
});

describe("editable human prompts", () => {
	function editable(): ConversationSnapshot {
		const value = snapshot();
		value.capabilities = ["fork"];
		value.nativeForkAvailableAfterSequence = 1;
		value.items = value.items.map((item) => item.kind === "message" && item.role === "user" ? { ...item, editAvailable: true } : item);
		return value;
	}

	it("offers the first prompt and natively forkable later prompts once their turns settle", () => {
		const { editable: turns, reconstructed } = editableHumanTurns(editable());
		expect([...turns]).toEqual(["t1", "t2"]);
		expect(reconstructed.size).toBe(0);
	});

	it("offers nothing while the agent is working or not ready", () => {
		for (const state of ["busy", "connecting", "recovering", "hibernated", "stopped"] as const) {
			const value = editable();
			value.controller.state = state;
			expect(editableHumanTurns(value).editable.size).toBe(0);
		}
	});

	it("skips prompts the daemon cannot edit and turns that never settled", () => {
		const value = editable();
		value.items = value.items.map((item) => item.id === "u1" ? { ...item, editAvailable: false } : item);
		value.turns[1] = { ...value.turns[1], state: "running" };
		expect(editableHumanTurns(value).editable.size).toBe(0);
	});

	it("needs a fork or replay to edit a later prompt", () => {
		const value = editable();
		value.capabilities = [];
		expect([...editableHumanTurns(value).editable]).toEqual(["t1"]);
		value.capabilities = ["prompt_replay", "embedded_context"];
		const replay = editableHumanTurns(value);
		expect([...replay.editable]).toEqual(["t1", "t2"]);
		expect([...replay.reconstructed]).toEqual(["t2"]);
	});

	it("treats a refresh with the same editable turns as unchanged", () => {
		const first = editableHumanTurns(editable());
		expect(sameEditableTurns(first, editableHumanTurns(editable()))).toBe(true);
		const busy = editable();
		busy.controller.state = "busy";
		expect(sameEditableTurns(first, editableHumanTurns(busy))).toBe(false);
		expect(sameEditableTurns(undefined, undefined)).toBe(true);
		expect(sameEditableTurns(first, undefined)).toBe(false);
	});

	it("does not treat the oldest loaded prompt as the first while older history is unloaded", () => {
		const value = editable();
		value.capabilities = [];
		value.hasMoreBefore = true;
		expect(editableHumanTurns(value).editable.size).toBe(0);
	});
});

describe("turn plans", () => {
	const list = (status: "pending" | "in_progress" | "completed") => [
		{ text: "Read the code", status: "completed" as const },
		{ text: "Write the fix", status },
	];

	it("shows a list the agent keeps across turns only on the latest turn that carries it", () => {
		const value = snapshot();
		// The daemon marks every older copy superseded, a failed turn's included.
		value.turns[0].state = "failed";
		value.turns[0].plan = { spansTurns: true, superseded: true, steps: list("in_progress") };
		value.turns[1].plan = { spansTurns: true, steps: list("completed") };
		const groups = groupConversationByTurn(value);
		expect(groups.find((group) => group.turnId === "t1")?.plan).toBeUndefined();
		expect(groups.find((group) => group.turnId === "t2")?.plan).toBe(value.turns[1].plan);
	});

	it("keeps each turn's own plan", () => {
		const value = snapshot();
		value.turns[0].plan = { steps: list("in_progress") };
		value.turns[1].plan = { steps: list("completed") };
		const groups = groupConversationByTurn(value);
		expect(groups.map((group) => group.plan)).toEqual([value.turns[0].plan, value.turns[1].plan]);
	});

	it("shows no panel for a plan without steps", () => {
		const value = snapshot();
		value.turns[0].plan = { steps: [] };
		expect(groupConversationByTurn(value)[0].plan).toBeUndefined();
	});
});

describe("settledTurnItems", () => {
	const limit = "You've hit your 5-hour limit · resets 4:50pm (Asia/Calcutta)";
	const notice = (id: string, summary: string, status: ConversationActivity["status"] = "running"): ConversationActivity => ({
		kind: "activity", id, turnId: "t", sequence: 1, revision: 1, activityKind: "system", status, summary, createdAt: "",
		detail: { event: "provider.failure" },
	} as ConversationActivity);
	const command = (status: ConversationActivity["status"]): ConversationActivity => ({
		kind: "activity", id: "c", turnId: "t", sequence: 2, revision: 1, activityKind: "command", status, summary: "ls", createdAt: "",
	} as ConversationActivity);
	const turn = (state: ConversationTurn["state"], errorMessage?: string): ConversationTurn => ({ id: "t", state, requestedAt: "x", errorMessage });

	it("leaves a running turn's rows alone, spinners included", () => {
		const items = [notice("n1", limit), command("running")];
		expect(settledTurnItems(items, turn("running"))).toBe(items);
		expect(settledTurnItems(items, undefined)).toBe(items);
	});

	// Older daemons store the replayed limit notice again, running, on every agent restart.
	it("drops a finished turn's notices that restate its error, however many replays added", () => {
		const items = [notice("n1", limit), notice("n2", limit), notice("n3", limit, "failed")];
		expect(settledTurnItems(items, turn("failed", limit))).toEqual([]);
	});

	it("shows one copy of another replayed notice, without a spinner", () => {
		const weekly = "You've hit your weekly limit · resets 12:30pm (Asia/Calcutta)";
		const shown = settledTurnItems([notice("n1", weekly), notice("n2", weekly)], turn("completed"));
		expect(shown.map((item) => [item.id, item.kind === "activity" && item.status])).toEqual([["n1", "completed"]]);
	});

	it("keeps notices that share a title but say something different", () => {
		const title = "Reconnecting to Claude";
		const first = { ...notice("n1", title), detail: { event: "provider.failure", category: "connection", text: "Trying again in 4s." } } as ConversationActivity;
		const second = { ...notice("n2", title), detail: { event: "provider.failure", category: "connection", text: "The API is overloaded." } } as ConversationActivity;
		const replay = { ...first, id: "n3" } as ConversationActivity;
		expect(settledTurnItems([first, second, replay], turn("completed")).map((item) => item.id)).toEqual(["n1", "n2"]);
	});

	it("settles any leftover running row the way the daemon settles a turn", () => {
		const status = (state: ConversationTurn["state"]) => (settledTurnItems([command("running")], turn(state))[0] as ConversationActivity).status;
		expect([status("completed"), status("recovered"), status("interrupted"), status("failed")]).toEqual(["completed", "completed", "cancelled", "failed"]);
		expect((settledTurnItems([command("failed")], turn("completed"))[0] as ConversationActivity).status).toBe("failed");
	});
});

describe("steers split a turn", () => {
	const at = (second: number) => `2026-08-05T00:00:${String(second).padStart(2, "0")}Z`;
	const user = (id: string, sequence: number, text: string): ConversationItem => ({ kind: "message", id, turnId: "t", sequence, revision: 1, role: "user", origin: "human", text, streaming: false, createdAt: at(sequence) });
	const reply = (id: string, sequence: number, text: string): ConversationItem => ({ kind: "message", id, turnId: "t", sequence, revision: 1, role: "assistant", origin: "provider", text, streaming: false, createdAt: at(sequence) });
	const command = (id: string, sequence: number): ConversationItem => ({ kind: "activity", id, turnId: "t", sequence, revision: 1, activityKind: "command", status: "completed", summary: id, createdAt: at(sequence) } as ConversationActivity);
	const steer = (id: string, sequence: number, detail: Record<string, unknown> = {}): ConversationItem => ({ kind: "activity", id, turnId: "t", sequence, revision: 1, activityKind: "system", status: "completed", summary: id, createdAt: at(sequence), detail: { event: "steer", text: id, ...detail } } as ConversationActivity);
	const chat = (state: ConversationTurn["state"], items: ConversationItem[]): ConversationSnapshot => ({
		...snapshot(),
		turns: [{ id: "t", state, providerTurnId: "p", requestedAt: at(0), startedAt: at(1), ...(state === "running" ? {} : { completedAt: at(50) }), plan: { steps: [{ text: "Fix", status: "completed" }] } }],
		items,
	});
	const ids = (group: { items: ConversationItem[] }) => group.items.map((item) => item.id);

	it("starts a new segment at each steer the person typed, so it never folds into Worked for", () => {
		const value = chat("completed", [user("u", 1, "go"), command("ls", 2), reply("r1", 3, "Looking"), steer("s1", 10), command("cat", 11), steer("s2", 20), reply("r2", 21, "Done")]);
		const groups = groupConversationByTurn(value);
		expect(groups.map(ids)).toEqual([["u", "ls", "r1"], ["s1", "cat"], ["s2", "r2"]]);
		expect(groups.map((group) => group.key)).toEqual(["turn-t", "turn-t:steer:s1", "turn-t:steer:s2"]);
		expect(groups.every((group) => group.turnId === "t")).toBe(true);
		// Each earlier segment settles as worked up to the steer; the last keeps the turn's own outcome.
		expect(groups.map((group) => [group.turn?.state, group.turn?.startedAt, group.turn?.completedAt])).toEqual([
			["completed", at(1), at(10)],
			["completed", at(10), at(20)],
			["completed", at(20), at(50)],
		]);
		expect(groups.map((group) => group.continuesTurn ?? false)).toEqual([false, true, true]);
		expect(groups.map((group) => group.endsTurn)).toEqual([false, false, true]);
		// The plan, diff and actions belong to the real turn, drawn under its last segment.
		expect(groups.map((group) => Boolean(group.plan))).toEqual([false, false, true]);
		expect(partitionTurnItems(groups[1].items, groups[1].turn)!.prompt.map((item) => item.id)).toEqual(["s1"]);
	});

	it("settles the work before a steer while the turn keeps running after it", () => {
		const groups = groupConversationByTurn(chat("running", [user("u", 1, "go"), command("ls", 2), steer("s1", 10), command("cat", 11)]));
		expect(groups.map((group) => group.turn?.state)).toEqual(["completed", "running"]);
		expect(groups[1].turn?.startedAt).toBe(at(10));
	});

	it("keeps a steer another session sent inside the work", () => {
		const groups = groupConversationByTurn(chat("completed", [user("u", 1, "go"), steer("s1", 10, { senderSessionId: "worker-1" }), reply("r", 11, "ok")]));
		expect(groups.map(ids)).toEqual([["u", "s1", "r"]]);
		expect(groups[0].endsTurn).toBe(true);
	});

	it("keeps each part's turn view while its turn is unchanged, so a settled part keeps its memo", () => {
		const value = chat("running", [user("u", 1, "go"), command("ls", 2), steer("s1", 10), command("cat", 11)]);
		const first = groupConversationByTurn(value);
		const streamed = { ...value, items: [...value.items, reply("r", 12, "Work")] };
		const second = groupConversationByTurn(streamed);
		expect(second.map((group) => group.turn)).toEqual(first.map((group) => group.turn));
		expect(second[0].turn).toBe(first[0].turn);
		expect(second[1].turn).toBe(first[1].turn);
		// A new turn record from the daemon gives new views.
		const settled = groupConversationByTurn({ ...streamed, turns: [{ ...streamed.turns[0], state: "completed", completedAt: at(50) }] });
		expect(settled[1].turn).not.toBe(first[1].turn);
		expect(settled[1].turn?.state).toBe("completed");
	});

	it("leaves a queued turn whole", () => {
		const value = chat("queued", [user("u", 1, "go"), steer("s1", 10)]);
		const groups = groupConversationByTurn(value, value.items);
		expect(groups.map(ids)).toEqual([["u", "s1"]]);
	});
});
