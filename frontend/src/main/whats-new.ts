import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import semver from "semver";
import {
	WHATS_NEW_DEFAULT_LIFETIME_MS,
	WHATS_NEW_FEED_URL,
	isWhatsNewItemId,
	parseWhatsNewFeed,
	type WhatsNewEntry,
	type WhatsNewFeed,
	type WhatsNewItem,
} from "../shared/whats-new";

/** Cache and seen state for the remote What's new feed, under the ~/.ao state dir. */
export const WHATS_NEW_FILE_NAME = "whats-new.json";

const REFRESH_INTERVAL_MS = 24 * 60 * 60 * 1000;
const FETCH_TIMEOUT_MS = 10_000;
const MAX_FEED_BYTES = 256 * 1024;
const MAX_DISMISSED = 200;

interface WhatsNewState {
	fetchedAt: number;
	feed: WhatsNewFeed | null;
	dismissed: string[];
}

export interface WhatsNewDeps {
	stateDir: string;
	appVersion: string;
	fetch: typeof fetch;
	now?: () => number;
	env?: NodeJS.ProcessEnv;
}

/** Local kill switch: AO_WHATS_NEW=0 (or false/off) stops fetching and hides the card. */
export function whatsNewDisabled(env: NodeJS.ProcessEnv): boolean {
	return ["0", "false", "off"].includes((env.AO_WHATS_NEW ?? "").trim().toLowerCase());
}

/** AO_WHATS_NEW_URL points a test build at another https or loopback feed. */
export function whatsNewFeedUrl(env: NodeJS.ProcessEnv): string {
	const override = env.AO_WHATS_NEW_URL?.trim();
	if (!override) return WHATS_NEW_FEED_URL;
	try {
		const url = new URL(override);
		if (url.protocol === "https:" || (url.protocol === "http:" && url.hostname === "127.0.0.1")) return url.toString();
	} catch {
		// Fall through to the published feed.
	}
	return WHATS_NEW_FEED_URL;
}

function versionInRange(item: WhatsNewItem, appVersion: string): boolean {
	const running = semver.valid(semver.coerce(appVersion));
	if (!item.minVersion && !item.maxVersion) return true;
	// A version range on an unreadable build version is a guess either way; hide it.
	if (!running) return false;
	const min = semver.valid(semver.coerce(item.minVersion ?? ""));
	const max = semver.valid(semver.coerce(item.maxVersion ?? ""));
	if (item.minVersion && (!min || semver.lt(running, min))) return false;
	if (item.maxVersion && (!max || semver.gte(running, max))) return false;
	return true;
}

/** The items this build should show right now, newest first. */
export function eligibleWhatsNewItems(feed: WhatsNewFeed | null, appVersion: string, now: number): WhatsNewItem[] {
	if (!feed?.enabled) return [];
	return feed.items
		.filter((item) => {
			const published = Date.parse(item.publishedAt);
			const expires = item.expiresAt ? Date.parse(item.expiresAt) : published + WHATS_NEW_DEFAULT_LIFETIME_MS;
			return published <= now && now < expires && versionInRange(item, appVersion);
		})
		.sort((left, right) => Date.parse(right.publishedAt) - Date.parse(left.publishedAt));
}

function emptyState(): WhatsNewState {
	return { fetchedAt: 0, feed: null, dismissed: [] };
}

export class WhatsNewService {
	private readonly file: string;
	private readonly now: () => number;
	private readonly env: NodeJS.ProcessEnv;
	private queue: Promise<unknown> = Promise.resolve();
	private refreshed = false;

	constructor(private readonly deps: WhatsNewDeps) {
		this.file = path.join(deps.stateDir, WHATS_NEW_FILE_NAME);
		this.now = deps.now ?? Date.now;
		this.env = deps.env ?? process.env;
	}

	get disabled(): boolean {
		return whatsNewDisabled(this.env);
	}

	private serial<T>(operation: () => Promise<T>): Promise<T> {
		const next = this.queue.then(operation, operation);
		this.queue = next.catch(() => undefined);
		return next;
	}

	private async read(): Promise<WhatsNewState> {
		try {
			const raw = JSON.parse(await readFile(this.file, "utf8")) as Partial<WhatsNewState>;
			return {
				fetchedAt: typeof raw.fetchedAt === "number" ? raw.fetchedAt : 0,
				feed: parseWhatsNewFeed(raw.feed ? { version: 1, ...raw.feed } : null),
				dismissed: Array.isArray(raw.dismissed) ? raw.dismissed.filter(isWhatsNewItemId).slice(-MAX_DISMISSED) : [],
			};
		} catch {
			return emptyState();
		}
	}

	private async write(state: WhatsNewState): Promise<void> {
		await mkdir(this.deps.stateDir, { recursive: true, mode: 0o750 });
		const tmp = path.join(this.deps.stateDir, `.whats-new-${process.pid}-${this.now()}.json`);
		await writeFile(tmp, `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
		await rename(tmp, this.file);
	}

	/**
	 * Fetches the feed at most once per launch and once per day. Sends no
	 * cookies, identifiers or version; any failure (offline, bad JSON, HTTP
	 * error) keeps the cached copy and is silent. Returns whether the cache changed.
	 */
	refresh(): Promise<boolean> {
		if (this.disabled || this.refreshed) return Promise.resolve(false);
		this.refreshed = true;
		return this.serial(async () => {
			const state = await this.read();
			if (state.fetchedAt && this.now() - state.fetchedAt < REFRESH_INTERVAL_MS) return false;
			let feed: WhatsNewFeed | null;
			try {
				const response = await this.deps.fetch(whatsNewFeedUrl(this.env), {
					cache: "no-store",
					credentials: "omit",
					headers: { Accept: "application/json" },
					redirect: "error",
					signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
				});
				if (!response.ok) return false;
				const body = await response.text();
				if (body.length > MAX_FEED_BYTES) return false;
				feed = parseWhatsNewFeed(JSON.parse(body));
			} catch {
				return false;
			}
			if (!feed) return false;
			await this.write({ ...state, fetchedAt: this.now(), feed });
			return true;
		}).catch(() => false);
	}

	/** Eligible items, newest first, each flagged with whether it was already dismissed. */
	list(): Promise<WhatsNewEntry[]> {
		if (this.disabled) return Promise.resolve([]);
		return this.serial(async () => {
			const state = await this.read();
			const dismissed = new Set(state.dismissed);
			return eligibleWhatsNewItems(state.feed, this.deps.appVersion, this.now()).map((item) => ({
				...item,
				seen: dismissed.has(item.id),
			}));
		});
	}

	/** Marks items seen so the card stops showing them; they stay listed in the panel. */
	dismiss(ids: unknown): Promise<void> {
		const valid = Array.isArray(ids) ? ids.filter(isWhatsNewItemId) : [];
		if (valid.length === 0) return Promise.resolve();
		return this.serial(async () => {
			const state = await this.read();
			const dismissed = [...new Set([...state.dismissed, ...valid])].slice(-MAX_DISMISSED);
			await this.write({ ...state, dismissed });
		});
	}
}
