import type { SideLaunchSnapshot, SideSnapshotCoverage } from "./side-close";
import { captureMainError, ExpectedIpcError } from "./main-error-capture";

export interface SideLaunchAttachment {
	generation: number;
	pid: number;
	port: number;
	appRunId: string;
}

export type SideLaunchCapture = { attachment: SideLaunchAttachment; state: SideLaunchSnapshot; coverage: SideSnapshotCoverage };
type Capture = SideLaunchCapture;
type CaptureWaiter = { resolve: (capture: Capture) => void; reject: (error: unknown) => void };
type Attempt = { promise: Promise<void>; resolve: () => void; reject: (error: unknown) => void };
type Recovery = {
	attachment: SideLaunchAttachment;
	abort: AbortController;
	attempt: Attempt;
	claimed: boolean;
	claimAttempt: Attempt;
	coverage: Set<string>;
	ready: boolean;
	payload?: string | null;
	failures: number;
	retry?: ReturnType<typeof setTimeout>;
	reading: boolean;
	waiters: CaptureWaiter[];
};

class RecoveryHTTPError extends Error {
	constructor(readonly status: number) { super(`Side chat launch recovery failed: HTTP ${status}`); }
}

function attempt(): Attempt {
	let resolve!: () => void, reject!: (error: unknown) => void;
	const promise = new Promise<void>((yes, no) => { resolve = yes; reject = no; });
	// Recovery starts on status changes even when no IPC caller is waiting yet.
	void promise.catch(() => undefined);
	return { promise, resolve, reject };
}

/** One recovery sequence and one snapshot read at a time for this daemon attachment. */
export class SideLaunchRecovery {
	private current?: Recovery;
	constructor(private readonly options: {
		fetch?: typeof fetch;
		snapshot: () => SideLaunchSnapshot | undefined;
		onRestore: (snapshot: SideLaunchSnapshot) => void;
		onRestoreAccepted?: (snapshot: SideLaunchSnapshot) => void;
		onClaimed?: () => void;
		onReady: () => void;
		onSnapshot: (state: SideLaunchSnapshot, coverage: SideSnapshotCoverage) => void;
		onError: (error: unknown) => void;
	}) {}
	get attachment(): SideLaunchAttachment | undefined { return this.current?.attachment; }
	get status(): "ready" | "restoring" | "unavailable" { return this.current?.ready ? "ready" : this.current?.claimed ? "restoring" : "unavailable"; }

	matches(attachment?: SideLaunchAttachment): boolean {
		const current = this.current?.attachment;
		return current?.generation === attachment?.generation && current?.pid === attachment?.pid && current?.port === attachment?.port && current?.appRunId === attachment?.appRunId;
	}

	setAttachment(attachment?: SideLaunchAttachment): boolean {
		if (this.matches(attachment)) return false;
		const old = this.current;
		this.current = undefined;
		if (old) {
			const error = new ExpectedIpcError("Side chat daemon attachment changed. Retry after reconnecting.");
			if (old.retry) clearTimeout(old.retry);
			old.abort.abort(error);
			old.attempt.reject(error);
			old.claimAttempt.reject(error);
			for (const waiter of old.waiters.splice(0)) waiter.reject(error);
			old.payload = undefined;
		}
		if (attachment) {
			const recovery: Recovery = { attachment, abort: new AbortController(), attempt: attempt(), claimAttempt: attempt(), coverage: new Set(), claimed: false, ready: false, failures: 0, reading: false, waiters: [] };
			this.current = recovery;
			void this.recover(recovery);
		}
		return true;
	}

	async ensureReady(): Promise<void> {
		const recovery = this.current;
		if (!recovery) throw new ExpectedIpcError("Side chat daemon is unavailable. Retry after reconnecting.");
		await recovery.attempt.promise;
		if (this.current !== recovery || !recovery.ready) throw new ExpectedIpcError("Side chat daemon attachment changed. Retry after reconnecting.");
	}
	async ensureClaimed(): Promise<void> {
		const recovery = this.current;
		if (!recovery) throw new ExpectedIpcError("Side chat daemon is unavailable. Retry after reconnecting.");
		await recovery.claimAttempt.promise;
		if (this.current !== recovery || !recovery.claimed) throw new ExpectedIpcError("Side chat daemon attachment changed. Retry after reconnecting.");
	}

	async capture(): Promise<Capture> {
		const recovery = this.current;
		if (!recovery) throw new ExpectedIpcError("Side chat daemon is unavailable. Retry after reconnecting.");
		await recovery.attempt.promise;
		if (this.current !== recovery || !recovery.ready) throw new ExpectedIpcError("Side chat daemon attachment changed. Retry after reconnecting.");
		return new Promise<Capture>((resolve, reject) => {
			recovery.waiters.push({ resolve, reject });
			if (!recovery.reading) void this.read(recovery);
		});
	}
	async captureForClose(): Promise<Capture> {
		const recovery = this.current;
		await this.ensureClaimed();
		if (!recovery || this.current !== recovery) throw new ExpectedIpcError("Side chat daemon attachment changed. Retry after reconnecting.");
		return new Promise<Capture>((resolve, reject) => {
			recovery.waiters.push({ resolve, reject });
			if (!recovery.reading) void this.read(recovery);
		});
	}

	captureInBackground(): void {
		void this.capture().catch(error => this.report(error));
	}
	captureCloseInBackground(): void { void this.captureForClose().catch(error => this.report(error)); }

	private report(error: unknown): void {
		if (!(error instanceof RecoveryHTTPError && (error.status < 500 || error.status === 503 || error.status === 504))) {
			captureMainError({ category: "side_chat", reason: "recovery", error });
		}
		this.options.onError(error);
	}

	private async request<T = void>(recovery: Recovery, path: string, init: RequestInit, timeoutMs: number, decode?: (response: Response) => Promise<T>): Promise<T> {
		const abort = new AbortController();
		const cancel = () => abort.abort(recovery.abort.signal.reason);
		if (recovery.abort.signal.aborted) cancel();
		else recovery.abort.signal.addEventListener("abort", cancel, { once: true });
		const timer = setTimeout(() => abort.abort(new ExpectedIpcError("Side chat recovery request timed out")), timeoutMs);
		let interrupt!: () => void;
		const interrupted = new Promise<never>((_resolve, reject) => {
			interrupt = () => reject(abort.signal.reason);
			if (abort.signal.aborted) interrupt();
			else abort.signal.addEventListener("abort", interrupt, { once: true });
		});
		try {
			const work = (async () => {
				const response = await (this.options.fetch ?? fetch)(`http://127.0.0.1:${recovery.attachment.port}/api/v1/side-chats/launch${path}`, { ...init, redirect: "error", signal: abort.signal }).catch((error: unknown) => {
					if (abort.signal.aborted) throw abort.signal.reason;
					throw new ExpectedIpcError(error instanceof Error ? error.message : "Side chat connection unavailable", { cause: error });
				});
				if (abort.signal.aborted) { void response.body?.cancel().catch(() => undefined); throw abort.signal.reason; }
				if (!response.ok) {
					await response.body?.cancel().catch(() => undefined);
					throw new RecoveryHTTPError(response.status);
				}
				if (decode) return await decode(response);
				await response.body?.cancel();
				return undefined as T;
			})();
			// Bound the complete response lifecycle even when a transport or body
			// reader ignores AbortSignal; later capture batches must still run.
			return await Promise.race([work, interrupted]);
		} finally {
			clearTimeout(timer);
			abort.signal.removeEventListener("abort", interrupt);
			recovery.abort.signal.removeEventListener("abort", cancel);
		}
	}

	private async recover(recovery: Recovery): Promise<void> {
		const running = recovery.attempt;
		try {
			const headers = { "content-type": "application/json" };
			if (!recovery.claimed) {
				await this.request(recovery, "/claim", { method: "POST", headers, body: JSON.stringify({ appRunId: recovery.attachment.appRunId }) }, 5_000);
				if (this.current !== recovery) return;
				recovery.claimed = true;
				recovery.claimAttempt.resolve();
				this.options.onClaimed?.();
			}
			if (recovery.payload === undefined) {
				const snapshot = this.options.snapshot();
				recovery.payload = snapshot?.appRunId === recovery.attachment.appRunId && (snapshot.sides.length > 0 || Object.keys(snapshot.numbers ?? {}).length > 0) ? JSON.stringify(snapshot) : null;
			}
			if (recovery.payload) {
				const offered = JSON.parse(recovery.payload) as SideLaunchSnapshot;
				this.options.onRestore(offered);
				await this.request(recovery, "/state", { method: "POST", headers, body: recovery.payload }, 60_000);
				if (this.current !== recovery) return;
				// Only acknowledged cleanup owns absence as shutdown evidence.
				// An OPEN payload may predate a locally pinned close intent.
				for (const record of offered.sides as { side?: { id?: string }; cleanupOnly?: boolean }[]) if (record.cleanupOnly && record.side?.id) recovery.coverage.add(record.side.id);
				this.options.onRestoreAccepted?.(offered);
			}
			if (this.current !== recovery) return;
			recovery.ready = true;
			recovery.payload = undefined;
			running.resolve();
			this.options.onReady();
		} catch (error) {
			if (this.current !== recovery) return;
			running.reject(error);
			if (!recovery.claimed) recovery.claimAttempt.reject(error);
			this.report(error);
			if (error instanceof RecoveryHTTPError && error.status !== 408 && error.status !== 429 && error.status < 500) { recovery.payload = undefined; return; }
			const delay = Math.min(30_000, 1000 * 2 ** Math.min(recovery.failures++, 5));
			recovery.attempt = attempt();
			if (!recovery.claimed) recovery.claimAttempt = attempt();
			recovery.retry = setTimeout(() => {
				recovery.retry = undefined;
				if (this.current === recovery) void this.recover(recovery);
			}, delay);
		}
	}

	private async read(recovery: Recovery): Promise<void> {
		recovery.reading = true;
		while (this.current === recovery && recovery.waiters.length) {
			// Calls after this read starts need one subsequent snapshot of their mutations.
			const batch = recovery.waiters.splice(0);
			const coverage = { ids: [...recovery.coverage], ready: recovery.ready };
			try {
				const state = await this.request(recovery, `/state?appRunId=${encodeURIComponent(recovery.attachment.appRunId)}`, {}, 5_000, response => response.json() as Promise<SideLaunchSnapshot>);
				if (this.current !== recovery || state.appRunId !== recovery.attachment.appRunId) throw new ExpectedIpcError("Stale side chat snapshot");
				this.options.onSnapshot(state, coverage);
				for (const record of state.sides as { side?: { id?: string } }[]) if (record.side?.id) recovery.coverage.add(record.side.id);
				for (const waiter of batch) waiter.resolve({ attachment: recovery.attachment, state, coverage });
			} catch (error) {
				for (const waiter of batch) waiter.reject(error);
			}
		}
		recovery.reading = false;
	}
}
