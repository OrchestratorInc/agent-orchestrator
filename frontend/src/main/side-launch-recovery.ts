import type { SideLaunchSnapshot } from "./side-close";

export interface SideLaunchAttachment {
	generation: number;
	pid: number;
	port: number;
	appRunId: string;
}

type Capture = { attachment: SideLaunchAttachment; state: SideLaunchSnapshot };
type CaptureWaiter = { resolve: (capture: Capture) => void; reject: (error: unknown) => void };
type Attempt = { promise: Promise<void>; resolve: () => void; reject: (error: unknown) => void };
type Recovery = {
	attachment: SideLaunchAttachment;
	abort: AbortController;
	attempt: Attempt;
	claimed: boolean;
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
		onRestore: () => void;
		onReady: () => void;
		onSnapshot: (state: SideLaunchSnapshot) => void;
		onError: (error: unknown) => void;
	}) {}

	matches(attachment?: SideLaunchAttachment): boolean {
		const current = this.current?.attachment;
		return current?.generation === attachment?.generation && current?.pid === attachment?.pid && current?.port === attachment?.port && current?.appRunId === attachment?.appRunId;
	}

	setAttachment(attachment?: SideLaunchAttachment): boolean {
		if (this.matches(attachment)) return false;
		const old = this.current;
		this.current = undefined;
		if (old) {
			const error = new Error("Side chat daemon attachment changed. Retry after reconnecting.");
			if (old.retry) clearTimeout(old.retry);
			old.abort.abort(error);
			old.attempt.reject(error);
			for (const waiter of old.waiters.splice(0)) waiter.reject(error);
			old.payload = undefined;
		}
		if (attachment) {
			const recovery: Recovery = { attachment, abort: new AbortController(), attempt: attempt(), claimed: false, ready: false, failures: 0, reading: false, waiters: [] };
			this.current = recovery;
			void this.recover(recovery);
		}
		return true;
	}

	async ensureReady(): Promise<void> {
		const recovery = this.current;
		if (!recovery) throw new Error("Side chat daemon is unavailable. Retry after reconnecting.");
		await recovery.attempt.promise;
		if (this.current !== recovery || !recovery.ready) throw new Error("Side chat daemon attachment changed. Retry after reconnecting.");
	}

	async capture(): Promise<Capture> {
		const recovery = this.current;
		if (!recovery) throw new Error("Side chat daemon is unavailable. Retry after reconnecting.");
		await recovery.attempt.promise;
		if (this.current !== recovery || !recovery.ready) throw new Error("Side chat daemon attachment changed. Retry after reconnecting.");
		return new Promise<Capture>((resolve, reject) => {
			recovery.waiters.push({ resolve, reject });
			if (!recovery.reading) void this.read(recovery);
		});
	}

	captureInBackground(): void {
		void this.capture().catch(error => this.options.onError(error));
	}

	private async request<T = void>(recovery: Recovery, path: string, init: RequestInit, timeoutMs: number, decode?: (response: Response) => Promise<T>): Promise<T> {
		const abort = new AbortController();
		const cancel = () => abort.abort(recovery.abort.signal.reason);
		if (recovery.abort.signal.aborted) cancel();
		else recovery.abort.signal.addEventListener("abort", cancel, { once: true });
		const timer = setTimeout(() => abort.abort(new Error("Side chat recovery request timed out")), timeoutMs);
		try {
			const response = await (this.options.fetch ?? fetch)(`http://127.0.0.1:${recovery.attachment.port}/api/v1/side-chats/launch${path}`, { ...init, signal: abort.signal });
			if (!response.ok) {
				await response.body?.cancel().catch(() => undefined);
				throw new RecoveryHTTPError(response.status);
			}
			if (decode) return await decode(response);
			await response.body?.cancel();
			return undefined as T;
		} finally {
			clearTimeout(timer);
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
			}
			if (recovery.payload === undefined) {
				const snapshot = this.options.snapshot();
				recovery.payload = snapshot?.appRunId === recovery.attachment.appRunId && (snapshot.sides.length > 0 || Object.keys(snapshot.numbers ?? {}).length > 0) ? JSON.stringify(snapshot) : null;
			}
			if (recovery.payload) {
				this.options.onRestore();
				await this.request(recovery, "/state", { method: "POST", headers, body: recovery.payload }, 60_000);
			}
			if (this.current !== recovery) return;
			recovery.ready = true;
			recovery.payload = undefined;
			running.resolve();
			this.options.onReady();
		} catch (error) {
			if (this.current !== recovery) return;
			running.reject(error);
			this.options.onError(error);
			if (error instanceof RecoveryHTTPError && error.status !== 408 && error.status !== 429 && error.status < 500) { recovery.payload = undefined; return; }
			const delay = Math.min(30_000, 1000 * 2 ** Math.min(recovery.failures++, 5));
			recovery.attempt = attempt();
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
			try {
				const state = await this.request(recovery, `/state?appRunId=${encodeURIComponent(recovery.attachment.appRunId)}`, {}, 5_000, response => response.json() as Promise<SideLaunchSnapshot>);
				if (this.current !== recovery || state.appRunId !== recovery.attachment.appRunId) throw new Error("Stale side chat snapshot");
				this.options.onSnapshot(state);
				for (const waiter of batch) waiter.resolve({ attachment: recovery.attachment, state });
			} catch (error) {
				for (const waiter of batch) waiter.reject(error);
			}
		}
		recovery.reading = false;
	}
}
