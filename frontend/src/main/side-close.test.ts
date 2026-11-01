import { describe, expect, it } from "vitest";
import { SideCloseRecovery } from "./side-close";

const record = { side: { id: "side", sessionId: "session", label: "Question", mainConversationId: "parent" }, providerHostId: "btw-run-side", providerForkId: "fork", harness: "codex", draft: "private draft", launchConfig: "private config" };
const snapshot = (sides: unknown[]) => ({ appRunId: "run", sides });

describe("side close recovery ownership", () => {
	it("pins minimal cleanup before DELETE and survives stale snapshots and daemon replacement", () => {
		const recovery = new SideCloseRecovery();
		recovery.remember(snapshot([record]));
		recovery.begin("side");
		recovery.remember(snapshot([record]));
		recovery.remember(snapshot([]));
		expect(recovery.state?.sides).toEqual([{ side: record.side, providerHostId: record.providerHostId, providerForkId: "fork", harness: "codex", cleanupOnly: true, hostStopped: undefined }]);
		expect(recovery.pending("session")).toEqual([{ id: "side", sessionId: "session", label: "Question" }]);
		expect(recovery.pending("other")).toEqual([]);
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
});
