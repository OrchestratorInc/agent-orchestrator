import { describe, expect, it } from "vitest";
import { SideCloseRecovery } from "./side-close";

const record = { side: { id: "side", sessionId: "session", label: "Question", mainConversationId: "parent" }, providerHostId: "btw-run-side", providerForkId: "fork", harness: "codex", draft: "private draft", launchConfig: "private config" };
const snapshot = (sides: unknown[]) => ({ appRunId: "run", sides });

describe("side close recovery ownership", () => {
	it("preserves unreconciled originals and numbering through partial recovery reads", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember({ ...snapshot([record]), numbers: { session: 4 } });
		recovery.remember({ ...snapshot([]), numbers: { session: 1 } }, { ids: [] });
		expect(recovery.state?.sides).toEqual([record]);
		expect(recovery.state?.numbers).toEqual({ session: 4 });
	});
	it("an offered OPEN record does not make a newly pinned close non-cancelable", () => {
		const recovery = new SideCloseRecovery(); const offered = snapshot([record]);
		recovery.remember(offered); recovery.begin("side"); recovery.markRestoreAttempt(offered);
		expect(recovery.cancel("side", record)).toBe(true);
	});
	it("partial absence cannot retire accepted cleanup before the post-ack barrier", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side"); recovery.markRestoreAccepted(recovery.state!);
		recovery.remember(snapshot([]), { ids: [] });
		expect(recovery.pending()[0].phase).toBe("cleanup_pending");
		recovery.remember(snapshot([]), { ids: ["side"] });
		expect(recovery.summary("session").closedIds).toEqual(["side"]);
	});
	it("pins minimal cleanup before DELETE and survives stale snapshots and daemon replacement", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record]));
		recovery.begin("side");
		recovery.remember(snapshot([record]));
		recovery.remember(snapshot([]));
		expect(recovery.state?.sides).toEqual([{ side: record.side, providerHostId: record.providerHostId, providerForkId: "fork", harness: "codex", cleanupOnly: true, hostStopped: undefined }]);
		expect(recovery.pending("session")).toEqual([{ id: "side", sessionId: "session", label: "Question", phase: "unconfirmed" }]);
		expect(recovery.pending("other")).toEqual([]);
	});
	it("distinguishes restore ownership from daemon-confirmed logical closure", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side");
		recovery.markRestoreAttempt();
		expect(recovery.pending()[0].phase).toBe("unconfirmed");
		recovery.remember(snapshot([{ ...record, cleanupOnly: true }]));
		expect(recovery.pending()).toEqual([{ id: "side", sessionId: "session", label: "Question", phase: "cleanup_pending" }]);
		// Older open records cannot undo observed closure or leak private fields.
		recovery.remember(snapshot([record]));
		expect(recovery.pending()[0].phase).toBe("cleanup_pending");
	});
	it.each([
		[[], "closed"],
		[[{ ...record, cleanupOnly: true, hostStopped: true }], "closed"],
		[[{ ...record, cleanupOnly: true }], "cleanup_pending"],
		[[record], "unconfirmed"],
	] as const)("classifies fresh already-closed evidence %#", (records, outcome) => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side");
		expect(recovery.reconcileMissing("side", snapshot([...records]))).toBe(outcome);
	});
	it("discovers daemon cleanup without a renderer close request and removes it after host stop", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([{ ...record, cleanupOnly: true }]));
		expect(recovery.pending()[0].phase).toBe("cleanup_pending");
		recovery.remember(snapshot([{ ...record, cleanupOnly: true, hostStopped: true }]));
		expect(recovery.pending()).toEqual([]);
		expect(recovery.state?.sides).toHaveLength(1);
	});
	it("restores the original only after a definitive rejection", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side"); recovery.cancel("side");
		expect(recovery.state?.sides).toEqual([record]);
		expect(recovery.pending()).toEqual([]);
	});
	it("hides confirmed shutdown while retaining fork cleanup and suppresses stale reopen", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side");
		recovery.remember(snapshot([{ ...record, cleanupOnly: true, hostStopped: true }]));
		recovery.cancel("side");
		expect(recovery.pending()).toEqual([]);
		expect(recovery.state?.sides).toHaveLength(1);
		recovery.remember(snapshot([]));
		recovery.remember(snapshot([record]));
		expect(recovery.state?.sides).toEqual([]);
	});
	it("retains accepted cleanup until a subsequent snapshot confirms its removal", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side"); recovery.confirm("side");
		expect(recovery.pending()).toEqual([]);
		expect(recovery.state?.sides).toHaveLength(1);
		recovery.remember(snapshot([]));
		expect(recovery.state?.sides).toEqual([]);
	});
	it("reconciles a missing side only after an already-closed response", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side");
		recovery.remember(snapshot([]));
		expect(recovery.pending()).toHaveLength(1);
		recovery.reconcileMissing("side", snapshot([]));
		expect(recovery.pending()).toEqual([]);
		expect(recovery.state?.sides).toEqual([]);
	});
	it("does not let an old rejection undo cleanup offered to a replacement daemon", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side");
		recovery.markRestoreAttempt();
		recovery.cancel("side");
		expect(recovery.pending()).toHaveLength(1);
		expect(recovery.state?.sides).toMatchObject([{ cleanupOnly: true }]);
	});
	it("retains confirmed host shutdown when the renderer misses cleanup pending", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side");
		expect(recovery.summary("session")).toEqual({ pending: recovery.pending(), closedIds: [] });
		recovery.remember(snapshot([{ ...record, cleanupOnly: true, hostStopped: true }]));
		expect(recovery.summary("session")).toEqual({ pending: [], closedIds: ["side"] });
		// Native fork cleanup is still owned, but it must no longer keep a tab open.
		expect(recovery.state?.sides).toHaveLength(1);
		recovery.remember(snapshot([{ ...record, cleanupOnly: true }]));
		expect(recovery.summary("session")).toEqual({ pending: [], closedIds: ["side"] });
		recovery.remember(snapshot([]));
		recovery.remember(snapshot([record]));
		expect(recovery.state?.sides).toEqual([]);
		expect(recovery.summary("session")).toEqual({ pending: [], closedIds: ["side"] });
		expect(recovery.summary("other")).toEqual({ pending: [], closedIds: [] });
	});
	it("discovers completed shutdown without a local close intent", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([{ ...record, cleanupOnly: true, hostStopped: true }]));
		expect(recovery.summary("session")).toEqual({ pending: [], closedIds: ["side"] });
	});
	it("remembers completion when observed daemon cleanup disappears", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side");
		recovery.remember(snapshot([{ ...record, cleanupOnly: true }]));
		recovery.remember(snapshot([]));
		expect(recovery.summary("session")).toEqual({ pending: [], closedIds: ["side"] });
	});
	it("does not convert restore ownership and ordinary absence into shutdown proof", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side");
		recovery.markRestoreAttempt(); recovery.remember(snapshot([]));
		expect(recovery.summary("session")).toEqual({
			pending: [{ id: "side", sessionId: "session", label: "Question", phase: "unconfirmed" }], closedIds: [],
		});
		expect(recovery.state?.sides).toHaveLength(1);
	});
	it("retains successful DELETE proof after cleanup bookkeeping drains", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side"); recovery.confirm("side");
		recovery.remember(snapshot([]));
		expect(recovery.summary("session")).toEqual({ pending: [], closedIds: ["side"] });
	});
	it("does not report a rejected close as completed", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record])); recovery.begin("side"); recovery.cancel("side");
		expect(recovery.summary("session")).toEqual({ pending: [], closedIds: [] });
	});
});
