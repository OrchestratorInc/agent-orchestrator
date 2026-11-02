import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { SideLaunchRecovery, type SideLaunchAttachment } from "./side-launch-recovery";
import type { SideLaunchSnapshot } from "./side-close";
import { SideCloseRecovery } from "./side-close";

const attachment: SideLaunchAttachment = { generation: 1, pid: 11, port: 3002, appRunId: "run" };
const controllers: SideLaunchRecovery[] = [];
const ok = (state: unknown = { appRunId: "run", sides: [] }) => ({ ok: true, status: 200, json: async () => state }) as Response;
const status = (code: number) => ({ ok: false, status: code }) as Response;
const hung = (signal: AbortSignal) => new Promise<Response>((_resolve, reject) => signal.addEventListener("abort", () => reject(signal.reason), { once: true }));

function setup(snapshot?: SideLaunchSnapshot) {
	const fetcher = vi.fn<typeof fetch>().mockResolvedValue(ok());
	const options = { fetch: fetcher, snapshot: vi.fn(() => snapshot), onRestore: vi.fn(), onReady: vi.fn(), onSnapshot: vi.fn(), onError: vi.fn() };
	const recovery = new SideLaunchRecovery(options);
	controllers.push(recovery);
	return { recovery, fetcher, ...options };
}
beforeEach(() => vi.useFakeTimers());
afterEach(() => { for (const controller of controllers.splice(0)) controller.setAttachment(undefined); vi.useRealTimers(); });

it("recovers once and adds no requests or timers while healthy", async () => {
	const { recovery, fetcher, onReady } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	for (let i = 0; i < 10; i++) recovery.setAttachment({ ...attachment });
	await vi.advanceTimersByTimeAsync(300_000);
	expect(fetcher).toHaveBeenCalledTimes(1);
	expect(onReady).toHaveBeenCalledOnce();
	expect(vi.getTimerCount()).toBe(0);
});

it("releases unused HTTP bodies so retrying does not strand loopback connections", async () => {
	const { recovery, fetcher } = setup();
	const cancel = vi.fn().mockResolvedValue(undefined);
	fetcher.mockResolvedValueOnce({ ...status(503), body: { cancel } } as unknown as Response);
	recovery.setAttachment(attachment);
	await vi.advanceTimersByTimeAsync(0);
	expect(cancel).toHaveBeenCalledOnce();
	const ready = recovery.ensureReady();
	await vi.advanceTimersByTimeAsync(1000);
	await ready;
});

it.each([408, 429, 500, 503])("backs off transient HTTP %i failures while the daemon stays ready", async code => {
	const { recovery, fetcher } = setup();
	fetcher.mockResolvedValue(status(code));
	recovery.setAttachment(attachment);
	await vi.advanceTimersByTimeAsync(0);
	for (const [i, delay] of [1000, 2000, 4000, 8000, 16000, 30000, 30000].entries()) {
		await vi.advanceTimersByTimeAsync(delay - 1);
		expect(fetcher).toHaveBeenCalledTimes(i + 1);
		await vi.advanceTimersByTimeAsync(1);
		expect(fetcher).toHaveBeenCalledTimes(i + 2);
	}
	fetcher.mockResolvedValue(ok());
	const ready = recovery.ensureReady();
	await vi.advanceTimersByTimeAsync(30_000);
	await ready;
	expect(vi.getTimerCount()).toBe(0);
});

it("retries a claim timeout at five seconds without overlapping attempts", async () => {
	const { recovery, fetcher } = setup();
	fetcher.mockImplementationOnce((_url, init) => hung(init!.signal!));
	recovery.setAttachment(attachment);
	const first = recovery.ensureReady().catch(error => error);
	const alsoFirst = recovery.capture().catch(error => error);
	await vi.advanceTimersByTimeAsync(5000);
	expect((await first).message).toContain("timed out");
	expect(await alsoFirst).toBe(await first);
	expect(fetcher).toHaveBeenCalledTimes(1);
	const next = recovery.ensureReady();
	await vi.advanceTimersByTimeAsync(1000);
	await next;
	expect(fetcher).toHaveBeenCalledTimes(2);
});

it("retries the exact frozen restore after sixty seconds and releases the payload on success", async () => {
	const snapshot = { appRunId: "run", sides: [{ id: "original" }], numbers: { session: 4 } };
	const { recovery, fetcher, snapshot: getSnapshot, onRestore } = setup(snapshot);
	fetcher.mockResolvedValueOnce(ok()).mockImplementationOnce((_url, init) => hung(init!.signal!));
	recovery.setAttachment(attachment);
	const first = recovery.ensureReady().catch(error => error);
	await vi.advanceTimersByTimeAsync(15_001);
	expect(fetcher).toHaveBeenCalledTimes(2);
	snapshot.sides.push({ id: "newer local state" });
	await vi.advanceTimersByTimeAsync(44_999);
	expect((await first).message).toContain("timed out");
	const retry = recovery.ensureReady();
	await vi.advanceTimersByTimeAsync(1000);
	await retry;
	expect(fetcher).toHaveBeenCalledTimes(3); // Do not reclaim after acknowledged claim.
	expect(fetcher.mock.calls[2][1]?.body).toBe(fetcher.mock.calls[1][1]?.body);
	expect(String(fetcher.mock.calls[2][1]?.body)).not.toContain("newer local state");
	expect(getSnapshot).toHaveBeenCalledOnce();
	expect(onRestore).toHaveBeenCalledTimes(2);
	await vi.advanceTimersByTimeAsync(120_000);
	expect(fetcher).toHaveBeenCalledTimes(3);
});

it.each([400, 401, 403, 409, 422])("does not spin on permanent rejection %i", async code => {
	const { recovery, fetcher } = setup();
	fetcher.mockResolvedValue(status(code));
	recovery.setAttachment(attachment);
	await expect(recovery.ensureReady()).rejects.toThrow(`HTTP ${code}`);
	await vi.advanceTimersByTimeAsync(120_000);
	expect(fetcher).toHaveBeenCalledOnce();
	expect(vi.getTimerCount()).toBe(0);
});

it("coalesces concurrent captures into one read plus a follow-up for later mutations", async () => {
	const { recovery, fetcher, onSnapshot } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	let finish!: (response: Response) => void;
	fetcher.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
	const first = recovery.capture();
	await vi.advanceTimersByTimeAsync(0);
	const later = Array.from({ length: 100 }, () => recovery.capture());
	await vi.advanceTimersByTimeAsync(0);
	expect(fetcher).toHaveBeenCalledTimes(2); // Claim and the first read.
	finish(ok());
	await Promise.all([first, ...later]);
	expect(fetcher).toHaveBeenCalledTimes(3);
	expect(onSnapshot).toHaveBeenCalledTimes(2);
});

it("rejects snapshots from the wrong app launch", async () => {
	const { recovery, fetcher, onSnapshot } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	fetcher.mockResolvedValueOnce(ok({ appRunId: "foreign", sides: [] }));
	await expect(recovery.capture()).rejects.toThrow("Stale");
	expect(onSnapshot).not.toHaveBeenCalled();
});

it("aborts obsolete requests and ignores late replies on daemon replacement", async () => {
	const { recovery, fetcher, onReady } = setup();
	let finish!: (response: Response) => void;
	fetcher.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
	recovery.setAttachment(attachment);
	const old = recovery.ensureReady().catch(error => error);
	const oldSignal = fetcher.mock.calls[0][1]!.signal!;
	recovery.setAttachment({ ...attachment, generation: 2, port: 3003 });
	expect(oldSignal.aborted).toBe(true);
	await recovery.ensureReady();
	finish(ok());
	await vi.advanceTimersByTimeAsync(0);
	expect((await old).message).toContain("attachment changed");
	expect(onReady).toHaveBeenCalledOnce();
	expect(fetcher).toHaveBeenCalledTimes(2);
});

it("disposes scheduled retries when the daemon leaves", async () => {
	const { recovery, fetcher } = setup();
	fetcher.mockRejectedValue(new TypeError("network unavailable"));
	recovery.setAttachment(attachment);
	await vi.advanceTimersByTimeAsync(0);
	const waiting = recovery.capture().catch(error => error);
	recovery.setAttachment(undefined);
	await waiting;
	await vi.advanceTimersByTimeAsync(300_000);
	expect(fetcher).toHaveBeenCalledOnce();
	expect(vi.getTimerCount()).toBe(0);
});

it("does not apply a stale snapshot or resolve old waiters into a replacement daemon", async () => {
	const { recovery, fetcher, onSnapshot } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	let finish!: (response: Response) => void;
	fetcher.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
	const old = recovery.capture().catch(error => error);
	await vi.advanceTimersByTimeAsync(0);
	const queued = recovery.capture().catch(error => error);
	await vi.advanceTimersByTimeAsync(0);
	recovery.setAttachment({ ...attachment, generation: 2 });
	await recovery.ensureReady();
	finish(ok({ appRunId: "run", sides: [{ stale: true }] }));
	await vi.advanceTimersByTimeAsync(0);
	expect(await old).toBeInstanceOf(Error);
	expect(await queued).toBeInstanceOf(Error);
	expect(onSnapshot).not.toHaveBeenCalled();
	await recovery.capture();
	expect(onSnapshot).toHaveBeenCalledOnce();
});

it("bounds capture time and permits a fresh follow-up without reclaiming", async () => {
	const { recovery, fetcher } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	fetcher.mockImplementationOnce((_url, init) => hung(init!.signal!));
	const slow = recovery.capture().catch(error => error);
	await vi.advanceTimersByTimeAsync(0);
	const fresh = recovery.capture();
	await vi.advanceTimersByTimeAsync(5000);
	expect((await slow).message).toContain("timed out");
	await fresh;
	expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/claim"))).toHaveLength(1);
	expect(fetcher).toHaveBeenCalledTimes(3);
});

it("retains accepted close ownership when the background snapshot fails", async () => {
	const ledger = new SideCloseRecovery();
	ledger.remember({ appRunId: "run", sides: [{ side: { id: "side", sessionId: "session" }, providerHostId: "host", providerForkId: "fork" }] });
	ledger.begin("side");
	const { recovery, fetcher, onError } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	ledger.confirm("side"); // The daemon's DELETE already succeeded.
	fetcher.mockRejectedValueOnce(new Error("snapshot transport failed"));
	expect(recovery.captureInBackground()).toBeUndefined();
	expect(ledger.pending()).toEqual([]);
	await vi.advanceTimersByTimeAsync(0);
	expect(onError).toHaveBeenCalledOnce();
	expect(ledger.state?.sides).toMatchObject([{ cleanupOnly: true, providerForkId: "fork" }]);
	await recovery.capture(); // A later capture still works without another claim.
	expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/claim"))).toHaveLength(1);
});
