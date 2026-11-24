import type { BrowserDialog, BrowserFrame, BrowserTransport, BrowserViewState } from "./browserTransport";

/** No message for this long, from a host that sends a heartbeat, means the stream is gone. */
export const STALE_AFTER_MS = 3_000;
const FIRST_RETRY_MS = 1_000;
const MAX_RETRY_MS = 15_000;
// A failed transport may still be asking why (the local probe gives up after
// 5 s); it is closed after this, so its own listeners don't outlive it.
const RETIRE_AFTER_MS = 6_000;

/** Wait before reconnect attempt `failures` (0-based): 1 s, doubling, capped at 15 s. */
export function reconnectDelayMs(failures: number): number {
	return Math.min(FIRST_RETRY_MS * 2 ** Math.max(0, failures), MAX_RETRY_MS);
}

export type SupervisedStatus = "connecting" | "open" | "reconnecting" | "error";

export type SupervisorEvents = {
	onStatus(status: SupervisedStatus, message?: string): void;
	onState(state: BrowserViewState): void;
	onFrame(frame: BrowserFrame): void;
	onDialog?(dialog: BrowserDialog): void;
	onDisabled?(): void;
	/** Whether the last frame on screen may be out of date: the stream went quiet or is reconnecting. */
	onStale(stale: boolean): void;
};

export type SupervisorDeps = {
	/** A new transport for every (re)connect. */
	create(): BrowserTransport;
	/** React Native AppState changes: "active", "background", "inactive". */
	subscribeAppState?(listener: (state: string) => void): () => void;
	/** Whether the phone has a network, on every change. */
	subscribeNetwork?(listener: (connected: boolean) => void): () => void;
};

/**
 * Keeps one browser stream alive while the app is in front, and none while
 * it isn't. It reconnects with backoff after a failure, at once when the
 * network comes back or the app returns, and treats a heartbeat host's
 * silence as a failure. In the background it closes the stream and leaves no
 * timer running, so neither the desktop nor a Cloud sandbox is kept awake.
 */
export class BrowserStreamSupervisor {
	private transport: BrowserTransport | null = null;
	private generation = 0;
	private failures = 0;
	private retry: ReturnType<typeof setTimeout> | null = null;
	private watchdog: ReturnType<typeof setInterval> | null = null;
	private lastAlive = 0;
	private heartbeat = false;
	private healthy = false;
	private stale = false;
	private everOpen = false;
	private running = false;
	// The connection now running has already failed: its error and the close
	// that follows count once.
	private failed = false;
	private away = false;
	private offline = false;
	private disabled = false;
	private unsubscribes: Array<() => void> = [];
	private retired = new Map<BrowserTransport, ReturnType<typeof setTimeout>>();

	constructor(private readonly deps: SupervisorDeps, private readonly events: SupervisorEvents) {}

	start(): void {
		if (this.running) return;
		this.running = true;
		const app = this.deps.subscribeAppState?.((state) => {
			if (state === "background") this.goAway();
			else if (state === "active" && this.away) this.comeBack();
		});
		const net = this.deps.subscribeNetwork?.((connected) => {
			const wasOffline = this.offline;
			this.offline = !connected;
			// A new network invalidates the old socket's path; reconnect now
			// rather than wait out a backoff.
			if (connected && !this.away && (wasOffline || !this.healthy)) this.reconnectNow();
		});
		this.unsubscribes = [app, net].filter((fn): fn is () => void => Boolean(fn));
		this.connect();
	}

	stop(): void {
		this.running = false;
		for (const unsubscribe of this.unsubscribes) unsubscribe();
		this.unsubscribes = [];
		this.drop();
		this.closeRetired();
	}

	private retire(transport: BrowserTransport): void {
		this.transport = null;
		this.retired.set(transport, setTimeout(() => {
			this.retired.delete(transport);
			transport.close();
		}, RETIRE_AFTER_MS));
	}

	private closeRetired(): void {
		for (const [transport, timer] of this.retired) {
			clearTimeout(timer);
			transport.close();
		}
		this.retired.clear();
	}

	/** The stream running now, if any: commands go to it. */
	get current(): BrowserTransport | null {
		return this.transport;
	}

	/** Reconnect at once, as the person's "Check again" does. */
	reconnectNow(): void {
		if (!this.running || this.disabled || this.away) return;
		this.failures = 0;
		this.connect();
	}

	private goAway(): void {
		this.away = true;
		this.drop();
		this.closeRetired();
	}

	private comeBack(): void {
		this.away = false;
		this.reconnectNow();
	}

	private drop(): void {
		this.generation++;
		this.clearRetry();
		this.stopWatchdog();
		this.healthy = false;
		const transport = this.transport;
		this.transport = null;
		transport?.close();
	}

	private connect(): void {
		// A transport that already failed has a dead socket. It is let go, not
		// closed: the local one may still be asking the daemon why it failed.
		if (this.failed && this.transport) this.retire(this.transport);
		this.drop();
		if (!this.running || this.disabled || this.away) return;
		const generation = this.generation;
		const live = () => generation === this.generation;
		this.heartbeat = false;
		this.failed = false;
		this.lastAlive = Date.now();
		const transport = this.deps.create();
		this.transport = transport;
		this.events.onStatus(this.everOpen ? "reconnecting" : "connecting");
		transport.connect({
			onStatus: (status, message) => {
				if (!live()) return;
				if (status === "open") {
					this.everOpen = true;
					this.lastAlive = Date.now();
					this.startWatchdog();
					this.events.onStatus("open", message);
				} else if (status === "error" || status === "closed") {
					this.fail(message);
				}
			},
			onState: (state) => { if (live()) { this.markAlive(false); this.events.onState(state); } },
			onFrame: (frame) => { if (live()) { this.markAlive(false); this.events.onFrame(frame); } },
			onDialog: (dialog) => { if (live()) this.events.onDialog?.(dialog); },
			// Sharing being off is the host's answer, not one socket's: it counts
			// even from a connection a retry has replaced.
			onDisabled: () => {
				if (!this.running || this.away || this.disabled) return;
				this.disabled = true;
				this.drop();
				this.closeRetired();
				this.events.onDisabled?.();
			},
			onAlive: ({ heartbeat }) => { if (live()) this.markAlive(heartbeat); },
		});
	}

	private markAlive(heartbeat: boolean): void {
		this.lastAlive = Date.now();
		if (heartbeat) this.heartbeat = true;
		if (!this.healthy) {
			this.healthy = true;
			this.failures = 0;
		}
		this.setStale(false);
	}

	/**
	 * The connection failed. A transport that errored is kept until the retry
	 * replaces it: the local one asks the daemon why its handshake failed, and a
	 * "sharing is off" answer must still reach onDisabled. A stream that went
	 * silent is closed at once (`close`).
	 */
	private fail(message?: string, close = false): void {
		if (close) this.drop();
		if (this.failed && !close) return;
		this.failed = true;
		this.clearRetry();
		this.stopWatchdog();
		this.healthy = false;
		if (this.disabled || this.away) return;
		this.setStale(true);
		this.events.onStatus(this.everOpen ? "reconnecting" : "error", message);
		// Offline: the network listener reconnects when it comes back.
		if (this.offline) return;
		const delay = reconnectDelayMs(this.failures);
		this.failures++;
		this.retry = setTimeout(() => {
			this.retry = null;
			this.connect();
		}, delay);
	}

	private startWatchdog(): void {
		this.stopWatchdog();
		this.watchdog = setInterval(() => {
			if (this.heartbeat && Date.now() - this.lastAlive > STALE_AFTER_MS) this.fail("The browser stopped answering. Reconnecting…", true);
		}, 500);
	}

	private stopWatchdog(): void {
		if (this.watchdog) clearInterval(this.watchdog);
		this.watchdog = null;
	}

	private clearRetry(): void {
		if (this.retry) clearTimeout(this.retry);
		this.retry = null;
	}

	private setStale(stale: boolean): void {
		if (stale === this.stale) return;
		this.stale = stale;
		this.events.onStale(stale);
	}
}
