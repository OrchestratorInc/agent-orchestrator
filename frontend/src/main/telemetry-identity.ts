import { randomUUID } from "node:crypto";
import { existsSync } from "node:fs";
import { lstat, mkdir, rename, rm, writeFile } from "node:fs/promises";
import path from "node:path";

export const TELEMETRY_OPT_OUT_FILE = "telemetry_opt_out";

type Fetcher = (input: string, init: RequestInit) => Promise<Response>;

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

export class TelemetryIdentityController {
	private cloudUserId: string | null = null;
	private known = false;
	private pushed = true;
	private generation = 0;
	private ackedGeneration = -1;
	private unsupportedGeneration = -1;
	private queue: Promise<void> = Promise.resolve();
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
			await rm(path.join(this.dataDir, "telemetry", "cli-usage-daily.json"), { force: true });
		} else {
			await rm(file, { force: true });
		}
		this.broadcast(optedOut);
		return optedOut;
	}

	async setCloudUser(userId: string | null): Promise<boolean | "unsupported"> {
		if (userId !== null && !CLOUD_USER_ID_PATTERN.test(userId)) throw new Error("invalid cloud user id");
		this.known = true;
		this.cloudUserId = userId;
		this.pushed = false;
		const mine = ++this.generation;
		if (userId === null) await this.retireSignedOutBinding();
		await this.flush();
		if (this.ackedGeneration === mine) return true;
		return this.unsupportedGeneration === mine ? "unsupported" : false;
	}

	invalidate(): void {
		if (!this.known) return;
		this.pushed = false;
		this.generation++;
	}

	async signOut(): Promise<void> {
		this.known = true;
		this.cloudUserId = null;
		this.pushed = false;
		this.generation++;
		await this.retireSignedOutBinding();
		void this.flush();
	}

	retireSignedOutBinding(): Promise<void> {
		const retire = this.queue.then(() => (this.cloudUserId === null ? this.retireBinding() : undefined));
		this.queue = retire.catch(() => undefined);
		return this.queue;
	}

	private async retireBinding(): Promise<void> {
		const bound = path.join(this.dataDir, "telemetry_bound_user");
		try {
			if (!(await lstat(bound)).isFile()) return;
		} catch {
			return;
		}
		const anonymous = path.join(this.dataDir, "telemetry_anonymous_id");
		const temporary = `${anonymous}.${randomUUID()}.tmp`;
		await writeFile(temporary, `anon_${randomUUID()}\n`, { mode: 0o600 });
		await rename(temporary, anonymous);
		await rm(path.join(this.dataDir, "telemetry", "cli-usage-daily.json"), { force: true });
		await rm(bound, { force: true });
	}

	flush(): Promise<void> {
		const run = this.queue.then(() => this.flushOnce());
		this.queue = run.catch(() => undefined);
		return run;
	}

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
			for (let attempt = 0; attempt < 2; attempt++) {
				const response = await this.request(base, "/internal/telemetry/identity", { method: "POST", body: { cloudUserId: sent, sequence: this.nextSequence() } });
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
		}
	}

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
