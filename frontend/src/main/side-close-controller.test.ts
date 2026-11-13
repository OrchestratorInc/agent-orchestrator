import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { SideCloseController } from "./side-close-controller";
import { SideCloseRecovery, type SideLaunchSnapshot } from "./side-close";
import type { SideLaunchAttachment } from "./side-launch-recovery";
import type { SideCloseEntry } from "../shared/side-close";

const record = { side: { id: "side", sessionId: "session", label: "Side", mainConversationId: "main" }, providerHostId: "btw-run-side", providerForkId: "private-fork", launchConfig: "private-credentials" };
const snapshot = (sides: unknown[] = [record]): SideLaunchSnapshot => ({ appRunId: "run", sides });
const reply = (status = 204, code = "CHAT_SIDE_UNAVAILABLE", extra = {}) => ({ status, json: async () => ({ code, ...extra }), body: { cancel: vi.fn().mockResolvedValue(undefined) } }) as unknown as Response;
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => { resolve = yes; }); return { promise, resolve }; }
function setup(initial = snapshot()) {
	const ledger = new SideCloseRecovery(); ledger.remember(initial);
	let attachment: SideLaunchAttachment | undefined = { appRunId: "run", generation: 1, pid: 11, port: 3002 };
	let remote = initial;
	const onChange = vi.fn(), onOwnershipChange = vi.fn();
	const ensureClaimed = vi.fn().mockResolvedValue(undefined);
	const fetcher = vi.fn<typeof fetch>().mockResolvedValue(reply());
	const capture = vi.fn().mockImplementation(async () => {
		const coverage = { ids: ["side"], ready: true };
		ledger.remember(remote, coverage); controller.reconcile(coverage);
		return { attachment: attachment!, state: remote, coverage };
	});
	const controller = new SideCloseController({ ledger, attachment: () => attachment, ensureClaimed, capture, fetch: fetcher, onChange, onOwnershipChange });
	controller.setAttachment(attachment, "ready"); controller.reconcile({ ids: ["side"], ready: true });
	let requestNumber = 0;
	const close = (overrides = {}) => controller.requestClose({ sessionId: "session", sideId: "side", epoch: controller.summary().epoch, requestId: `request-${++requestNumber}`, expectedVersion: controller.summary("session").sides.find(side => side.id === "side")?.version ?? 0, ...overrides });
	return { controller, ledger, capture, fetcher, onChange, onOwnershipChange, ensureClaimed, close,
		remote: (value: SideLaunchSnapshot) => { remote = value; },
		replace: () => { attachment = { ...attachment!, generation: 2, pid: 22 }; controller.setAttachment(attachment, "restoring"); } };
}
beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

it.each(["ledger", "ownership", "notification"])("keeps 204 proof across renderer loss when %s bookkeeping throws", async failure => {
	const h = setup();
	if (failure === "ledger") vi.spyOn(h.ledger, "confirm").mockImplementation(() => { throw new Error("ledger mirror failed"); });
	if (failure === "ownership") h.onOwnershipChange.mockImplementation(() => { if (h.fetcher.mock.calls.length) throw new Error("mirror failed"); });
	if (failure === "notification") h.onChange.mockImplementation(() => { throw new Error("renderer gone"); });
	await h.close();
	h.remote(snapshot([])); await h.capture();
	// A replacement renderer only queries main; it never needs the lost reply.
	expect(h.controller.summary("session").sides).toMatchObject([{ id: "side", status: "closed" }]);
	expect(JSON.stringify(h.controller.summary())).not.toMatch(/private-fork|credentials|providerHost/);
});

it("deduplicates before the first await and rejects stale replays without another DELETE", async () => {
	const h = setup(); const ready = deferred<void>(); h.ensureClaimed.mockReturnValueOnce(ready.promise);
	const first = h.close({ requestId: "one" }); const joined = h.close({ requestId: "two", expectedVersion: 0 });
	expect(h.controller.summary().sides[0].status).toBe("closing");
	expect(h.fetcher).not.toHaveBeenCalled(); ready.resolve(); await Promise.all([first, joined]);
	expect(h.fetcher).toHaveBeenCalledOnce();
	await h.close({ requestId: "one", expectedVersion: 0 });
	await h.close({ requestId: "late", expectedVersion: 0 });
	expect(h.fetcher).toHaveBeenCalledOnce();
});

it.each([200, 202, 408, 429, 500, 503])("does not treat HTTP %s as successful shutdown", async status => {
	const h = setup(); h.fetcher.mockResolvedValue(reply(status));
	expect((await h.close()).sides[0].status).toBe("unconfirmed");
});

it("requires current OPEN evidence and preserves cancellation through later mirror failure", async () => {
	const h = setup();
	h.fetcher.mockImplementation(async (_url, init) => init?.method === "DELETE" ? reply(409) : ({ status: 200, json: async () => ({ sides: [record.side] }) }) as Response);
	h.onOwnershipChange.mockImplementation(() => { if (h.fetcher.mock.calls.length) throw new Error("post cancellation mirror"); });
	expect((await h.close()).sides[0].status).toBe("rejected_open");
	expect(h.ledger.pending()).toEqual([]);
});

it.each(["absent", "unavailable", "terminated", "different_owner"])("keeps rejection readonly when fresh list proof is %s", async reason => {
	const h = setup();
	h.fetcher.mockImplementation(async (_url, init) => init?.method === "DELETE" ? reply(409) : reason === "unavailable" ? Promise.reject(new Error("offline")) : ({ status: reason === "terminated" ? 409 : 200, json: async () => ({ sides: reason === "different_owner" ? [{ ...record.side, sessionId: "other" }] : [] }) }) as Response);
	expect((await h.close()).sides[0].status).toBe("unconfirmed");
	expect(h.ledger.pending()).toHaveLength(1);
});

it("a later rejection cannot cancel an earlier dispatched unknown request", async () => {
	const h = setup(); h.fetcher.mockRejectedValueOnce(new Error("lost DELETE reply"));
	await h.close();
	h.fetcher.mockImplementation(async (_url, init) => init?.method === "DELETE" ? reply(409) : ({ status: 200, json: async () => ({ sides: [record.side] }) }) as Response);
	expect((await h.close()).sides[0].status).toBe("unconfirmed");
	expect(h.fetcher.mock.calls.filter(([, init]) => init?.method === "DELETE")).toHaveLength(2);
});

it("does not cancel cleanup offered to another attachment", async () => {
	const h = setup(); h.ledger.begin("side"); h.ledger.markRestoreAttempt();
	h.fetcher.mockImplementation(async (_url, init) => init?.method === "DELETE" ? reply(409) : ({ status: 200, json: async () => ({ sides: [record.side] }) }) as Response);
	expect((await h.close()).sides[0].status).toBe("unconfirmed");
});

it.each(["pending", "stopped", "gone"])("classifies already-closed response with fresh %s host evidence", async mode => {
	const h = setup(); h.fetcher.mockImplementationOnce(async () => {
		h.remote(snapshot(mode === "gone" ? [] : [{ ...record, cleanupOnly: true, hostStopped: mode === "stopped" }]));
		return reply(404, "CHAT_SIDE_CLOSED");
	});
	expect((await h.close()).sides[0].status).toBe(mode === "pending" ? "cleanup_pending" : "closed");
});

it("does not convert 404 and absent uncaptured ownership into success", async () => {
	const h = setup(snapshot([]));
	h.capture.mockImplementation(async () => ({ attachment: { appRunId: "run", generation: 1, pid: 11, port: 3002 }, state: snapshot([]), coverage: { ids: [] } }));
	h.fetcher.mockResolvedValue(reply(404, "CHAT_SIDE_CLOSED"));
	expect((await h.close()).sides[0].status).toBe("unconfirmed");
});

it("bounds a hung DELETE, aborts transport, and retains late positive same-attachment proof", async () => {
	const h = setup(); const response = deferred<Response>(); h.fetcher.mockReturnValueOnce(response.promise);
	const closing = h.close(); await vi.advanceTimersByTimeAsync(0);
	await vi.advanceTimersByTimeAsync(30_001);
	expect((await closing).sides[0].status).toBe("unconfirmed");
	expect(h.fetcher.mock.calls[0][1]?.signal?.aborted).toBe(true);
	response.resolve(reply()); await vi.advanceTimersByTimeAsync(0);
	expect(h.controller.summary().sides[0].status).toBe("closed");
});

it("late preparation cannot dispatch or repin after its watchdog and a newer rejection", async () => {
	const h = setup(); const first = deferred<void>(); h.ensureClaimed.mockReturnValueOnce(first.promise);
	const old = h.close(); await vi.advanceTimersByTimeAsync(5_001); await old;
	h.fetcher.mockImplementation(async (_url, init) => init?.method === "DELETE" ? reply(409) : ({ status: 200, json: async () => ({ sides: [record.side] }) }) as Response);
	expect((await h.close()).sides[0].status).toBe("rejected_open");
	first.resolve(); await vi.advanceTimersByTimeAsync(0);
	expect(h.ledger.pending()).toEqual([]);
	expect(h.fetcher.mock.calls.filter(([, init]) => init?.method === "DELETE")).toHaveLength(1);
});

it("fences old attachment completion and a stale epoch retry", async () => {
	const h = setup(); const response = deferred<Response>(); h.fetcher.mockReturnValueOnce(response.promise);
	const oldEpoch = h.controller.summary().epoch; const closing = h.close(); await vi.advanceTimersByTimeAsync(0);
	h.replace(); response.resolve(reply()); await closing;
	expect(h.controller.summary().sides[0].status).toBe("unconfirmed");
	await h.close({ epoch: oldEpoch }); expect(h.fetcher).toHaveBeenCalledOnce();
});

it("invalidates settled positive OPEN proof on every attachment change", async () => {
	const h = setup();
	h.fetcher.mockImplementation(async (_url, init) => init?.method === "DELETE" ? reply(409) : ({ status: 200, json: async () => ({ sides: [record.side] }) }) as Response);
	expect((await h.close()).sides[0].status).toBe("rejected_open");
	h.replace();
	expect(h.controller.summary()).toMatchObject({ recovery: "restoring", sides: [{ id: "side", status: "unconfirmed" }] });
});

it("publishes initially discovered stopped cleanup even without a local operation", () => {
	const h = setup(snapshot([{ ...record, cleanupOnly: true, hostStopped: true }]));
	expect(h.controller.summary("session").sides).toMatchObject([{ id: "side", status: "closed" }]);
});

it("retains the title learned during preparation for a replacement renderer after an unknown close", async () => {
	const h = setup(snapshot([]));
	h.remote(snapshot([{ ...record, side: { ...record.side, label: "Custom side title" } }]));
	h.fetcher.mockRejectedValueOnce(new Error("DELETE result lost"));
	await h.close();
	const state = h.controller.summary("session");
	expect(state.sides[0]).toMatchObject({ id: "side", label: "Custom side title", status: "unconfirmed" });
	expect(h.onChange.mock.calls.some(([update]) => update.sides.some((side: SideCloseEntry) => side.label === "Custom side title" && side.status === "closing"))).toBe(true);
	expect(state.sides[0].version).toBeGreaterThan(2);
	expect(JSON.stringify(state)).not.toMatch(/private-fork|credentials|providerHost/);
});

it("never publishes ready+empty before the first post-ready capture", () => {
	const h = setup(); h.replace(); h.controller.setRecovery("ready");
	expect(h.controller.summary().recovery).toBe("restoring");
	h.controller.reconcile({ ids: [], ready: false }); expect(h.controller.summary().recovery).toBe("restoring");
	h.ledger.remember(snapshot([{ ...record, cleanupOnly: true }]));
	h.controller.reconcile({ ids: ["side"], ready: true });
	expect(h.controller.summary()).toMatchObject({ recovery: "ready", sides: [{ id: "side", status: "cleanup_pending" }] });
});
