import { existsSync } from "node:fs";
import { mkdir, rm, writeFile } from "node:fs/promises";
import path from "node:path";

/** Same file the daemon's telemetry Identity reads (adapters/telemetry/identity.go). */
export const TELEMETRY_OPT_OUT_FILE = "telemetry_opt_out";

type Fetcher = (input: string, init: RequestInit) => Promise<Response>;

/** The newest sequence the daemon applied, from the details of its 409. */
async function daemonSequence(response: Response): Promise<number | null> {
	try {
		const body = (await response.json()) as { details?: { lastSequence?: unknown } };
		const last = body.details?.lastSequence;
		return typeof last === "number" && Number.isFinite(last) ? last : null;
	} catch {
		return null;
	}
}

const CLOUD_USER_ID_PATTERN = /^[A-Za-z0-9_-]{1,64}$/;

/**
 * Desktop side of the PostHog identity: the opt-out switch (a marker file the
 * daemon also reads) and the loopback hand-off of the signed-in AO Cloud user ID
 * to the daemon. The email never goes through here; it is set by the renderer at
 * identify time and the daemon never sees it.
 */
export class TelemetryIdentityController {
	private cloudUserId: string | null = null;
	// The renderer has not resolved its Cloud session until the first hand-off.
	private known = false;
	private pushed = true;
	// Bumped whenever the daemon's copy goes stale (restart, or a new user), so an
	// in-flight hand-off to the previous daemon cannot mark the new one as synced.
	private generation = 0;
	// The generation whose state the daemon acknowledged (2xx), i.e. durably
	// recorded. Tracked apart from `pushed`, which also stops retrying against an
	// older daemon that has no such route.
	private ackedGeneration = -1;
	private unsupportedGeneration = -1;
	// Hand-offs run one at a time. A request cannot be recalled once sent, so two
	// in flight could commit in the opposite order and leave the daemon on the
	// older user; queued runs always send the latest state when their turn comes.
	private queue: Promise<void> = Promise.resolve();
	// Stamped on every POST so the daemon can refuse one that was delayed in
	// transit (a client timeout cannot recall it) until after a newer one landed.
	private sequence = 0;

	constructor(
		private readonly dataDir: string,
		private readonly origin: () => string | null,
		private readonly fetcher: Fetcher = fetch,
		private readonly broadcast: (optedOut: boolean) => void = () => undefined,
	) {}

	isOptedOut(): boolean {
		return existsSync(path.join(this.dataDir, TELEMETRY_OPT_OUT_FILE));
	}

	async setOptedOut(optedOut: boolean): Promise<boolean> {
		const file = path.join(this.dataDir, TELEMETRY_OPT_OUT_FILE);
		if (optedOut) {
			await mkdir(this.dataDir, { recursive: true });
			await writeFile(file, "1\n", { mode: 0o600 });
		} else {
			await rm(file, { force: true });
		}
		this.broadcast(optedOut);
		return optedOut;
	}

	/**
	 * Records the signed-in user (null on sign-out) and pushes it to the daemon.
	 * Resolves true only when the daemon acknowledged exactly this state: the
	 * renderer identifies with PostHog only then, because identify() links the
	 * install ID to the account for good and the daemon must already have recorded
	 * that binding, or a later sign-out could not retire the ID. Unreachable
	 * daemon, an error response or a superseding call all resolve false.
	 */
	async setCloudUser(userId: string | null): Promise<boolean | "unsupported"> {
		if (userId !== null && !CLOUD_USER_ID_PATTERN.test(userId)) throw new Error("invalid cloud user id");
		this.known = true;
		this.cloudUserId = userId;
		this.pushed = false;
		const mine = ++this.generation;
		await this.flush();
		if (this.ackedGeneration === mine) return true;
		return this.unsupportedGeneration === mine ? "unsupported" : false;
	}

	/**
	 * Marks the daemon's copy stale. Called whenever the daemon leaves `ready`: a
	 * restarted daemon (even on the same port) starts with an empty identity, so
	 * the next ready transition must replay the signed-in user.
	 */
	invalidate(): void {
		if (!this.known) return;
		this.pushed = false;
		this.generation++;
	}

	/** Retries the hand-off; called when the daemon becomes ready. */
	flush(): Promise<void> {
		const run = this.queue.then(() => this.flushOnce());
		this.queue = run.catch(() => undefined);
		return run;
	}

	/** Strictly increasing and still increasing across an app restart (wall-clock based). */
	private nextSequence(): number {
		this.sequence = Math.max(this.sequence + 1, Date.now());
		return this.sequence;
	}

	private async flushOnce(): Promise<void> {
		if (!this.known || this.pushed) return;
		const base = this.origin();
		if (!base) return;
		const sent = this.cloudUserId ?? "";
		const generation = this.generation;
		try {
			// Two attempts: a 409 on the first means this process's sequence is behind
			// the daemon's (an earlier app process, or the clock moved backward), so it
			// jumps past the daemon's value and sends the same, newest, state again.
			for (let attempt = 0; attempt < 2; attempt++) {
				const response = await this.request(base, "/internal/telemetry/identity", { method: "POST", body: { cloudUserId: sent, sequence: this.nextSequence() } });
				// An older daemon without the route answers 404/405; nothing to retry.
				if (generation !== this.generation) return;
				if (response.status === 409 && attempt === 0) {
					const last = await daemonSequence(response);
					if (last !== null) {
						this.sequence = Math.max(this.sequence, last);
						continue;
					}
				}
				const unsupported = response.status === 404 || response.status === 405;
				this.pushed = response.ok || unsupported;
				if (response.ok) this.ackedGeneration = generation;
				if (unsupported) this.unsupportedGeneration = generation;
				return;
			}
		} catch {
			// Daemon not reachable yet; flush() runs again on the next ready transition.
		}
	}

	/** The GitHub login the daemon resolved from the authenticated account, or null. */
	async githubLogin(): Promise<string | null> {
		const base = this.origin();
		if (!base) return null;
		try {
			const response = await this.request(base, "/api/v1/telemetry/identity", { method: "GET" });
			if (!response.ok) return null;
			const body = (await response.json()) as { githubLogin?: unknown };
			return typeof body.githubLogin === "string" && body.githubLogin !== "" ? body.githubLogin : null;
		} catch {
			return null;
		}
	}

	private async request(base: string, pathname: string, init: { method: string; body?: object }): Promise<Response> {
		const parsed = new URL(base);
		if (parsed.protocol !== "http:" || parsed.hostname !== "127.0.0.1" || parsed.username || parsed.password || parsed.pathname !== "/" || parsed.search || parsed.hash) {
			throw new Error("daemon telemetry identity origin must be exact loopback HTTP");
		}
		const controller = new AbortController();
		const timer = setTimeout(() => controller.abort(), 2_000);
		try {
			return await this.fetcher(`${parsed.origin}${pathname}`, {
				method: init.method,
				signal: controller.signal,
				headers: init.body ? { "content-type": "application/json" } : undefined,
				body: init.body ? JSON.stringify(init.body) : undefined,
			});
		} finally {
			clearTimeout(timer);
		}
	}
}
