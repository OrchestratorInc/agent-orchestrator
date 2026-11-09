/**
 * The share links for cloud sessions and projects that this device created,
 * so Copy link in the share dialog works after the dialog closes and opens
 * again. The control plane keeps only a hash of each link's code, so the URL
 * exists only where it was made: here, encrypted with the OS keychain
 * (safeStorage) and keyed by the control plane and the link's id. On another
 * device the dialog offers "Copy new link" instead.
 *
 * Without protected storage (no keychain, or Linux's basic_text backend) the
 * URLs live only in memory for this run, never as plain text on disk. They
 * belong to the account that made them: the file names it, and another
 * account's URLs are never shown, even after a restart that missed the
 * sign-out. Signing out, or in as someone else, clears them (main.ts).
 */
import { promises as fs } from "node:fs";
import path from "node:path";
import type { IpcMain, IpcMainInvokeEvent } from "electron";

export interface SessionShareUrlCrypto {
	available(): boolean;
	encrypt(text: string): Buffer;
	decrypt(value: Buffer): string;
}

/** The signed-in account's id, whose URLs these are; undefined while signed out. */
export type SessionShareUrlOwner = () => Promise<string | undefined> | string | undefined;

const MAX_URL_LENGTH = 512;
const MAX_LINKS = 500;
const LINK_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function key(baseUrl: string, linkId: string): string {
	return JSON.stringify([baseUrl.replace(/\/+$/, ""), linkId.toLowerCase()]);
}

/** Only an http(s) URL of sensible length is kept; anything else is not a share link. */
function shareUrl(value: unknown): string | undefined {
	if (typeof value !== "string" || value.length > MAX_URL_LENGTH) return undefined;
	try {
		const url = new URL(value);
		return url.protocol === "https:" || url.protocol === "http:" ? value : undefined;
	} catch {
		return undefined;
	}
}

export class SessionShareUrls {
	/** This run's URLs for `user`, with the file's merged in once it has been read. */
	private urls = new Map<string, string>();
	/** The account this run's URLs belong to. */
	private user: string | undefined;
	/** Links forgotten before the file was read, so reading it doesn't bring them back. */
	private forgotten = new Set<string>();
	/**
	 * Whether the file has been read (or found missing, another account's, or
	 * unreadable for good), so writing it keeps what it held. Until then
	 * nothing is written: a keychain unavailable for a moment must not cost the
	 * URLs already saved.
	 */
	private loaded = false;
	/** Bumped by clear() and by another account, so a read that started before can't bring the old URLs back. */
	private generation = 0;
	/** Cleared, and nothing remembered since: clearing again (every signed-out notice) has nothing to do. */
	private empty = false;
	private writing: Promise<void> = Promise.resolve();

	constructor(private readonly file: string, private readonly crypto: SessionShareUrlCrypto, private readonly owner: SessionShareUrlOwner) {}

	async remember(baseUrl: string, linkId: string, url: string): Promise<void> {
		const urls = await this.load();
		if (!urls) return;
		const id = key(baseUrl, linkId);
		this.empty = false;
		this.forgotten.delete(id);
		urls.delete(id);
		urls.set(id, url);
		// Oldest first: past the cap the oldest links are forgotten.
		for (const old of urls.keys()) {
			if (urls.size <= MAX_LINKS) break;
			urls.delete(old);
		}
		await this.save();
	}

	async recall(baseUrl: string, linkIds: readonly string[]): Promise<Record<string, string>> {
		const urls = await this.load();
		const found: Record<string, string> = {};
		for (const linkId of linkIds) {
			const url = urls?.get(key(baseUrl, linkId));
			if (url) found[linkId] = url;
		}
		return found;
	}

	async forget(baseUrl: string, linkIds: readonly string[]): Promise<void> {
		const urls = await this.load();
		if (!urls) return;
		let changed = false;
		for (const linkId of linkIds) {
			const id = key(baseUrl, linkId);
			if (!this.loaded) this.forgotten.add(id);
			changed = urls.delete(id) || changed;
		}
		if (changed) await this.save();
	}

	/**
	 * Forgets every URL, on disk too: the account that made them signed out
	 * or another signed in. Waits for any write in flight, so none lands after.
	 */
	async clear(): Promise<void> {
		if (this.empty) return;
		this.empty = true;
		this.urls = new Map();
		this.forgotten.clear();
		this.generation += 1;
		// Nothing left to keep from the file: writing again starts it afresh.
		this.loaded = true;
		this.writing = this.writing.then(() => fs.rm(this.file, { force: true })).catch(() => undefined);
		await this.writing;
	}

	/** The signed-in account's URLs, read from the file once; undefined while signed out. */
	private async load(): Promise<Map<string, string> | undefined> {
		const user = await this.owner();
		if (!user) return undefined;
		if (user !== this.user) {
			// Another account's URLs, from this run or the file, are never its.
			this.user = user;
			this.urls = new Map();
			this.forgotten.clear();
			this.loaded = false;
			this.generation += 1;
		}
		if (this.loaded || !this.crypto.available()) return this.urls;
		const generation = this.generation;
		let raw: Buffer;
		try {
			raw = await fs.readFile(this.file);
		} catch (error) {
			// None yet: there is nothing to keep. Unreadable for now: try again next time.
			if (generation === this.generation && (error as NodeJS.ErrnoException).code === "ENOENT") this.loaded = true;
			return this.urls;
		}
		if (generation !== this.generation) return this.urls;
		let parsed: unknown;
		try {
			parsed = JSON.parse(this.crypto.decrypt(raw));
		} catch {
			// Another keychain's (another install or app signature) or damaged:
			// it can't be read now or later, so it counts as empty and the next
			// write replaces it, rather than every recall trying it again.
			parsed = undefined;
		}
		const saved = parsed as { user?: unknown; links?: unknown } | undefined;
		// Only the signed-in account's own; a file of another's (or from before
		// files named their account) holds nothing for it and goes on the next write.
		const links = saved?.user === user && Array.isArray(saved.links) ? saved.links : [];
		if (saved && saved.user !== user) {
			this.writing = this.writing.then(() => fs.rm(this.file, { force: true })).catch(() => undefined);
		}
		// This run's URLs are newer than the file's, and a link forgotten in the meantime stays forgotten.
		const merged = new Map<string, string>();
		for (const item of links) {
			if (Array.isArray(item) && typeof item[0] === "string" && shareUrl(item[1]) && !this.forgotten.has(item[0])) merged.set(item[0], item[1] as string);
		}
		for (const [id, url] of this.urls) {
			merged.delete(id);
			merged.set(id, url);
		}
		this.urls = merged;
		this.forgotten.clear();
		this.loaded = true;
		return this.urls;
	}

	// Writes are serialized and atomic: a crash leaves the old file or the new.
	// Without protected storage, or before the file could be read, nothing is
	// written and nothing removed: the URLs stay in memory for this run.
	private save(): Promise<void> {
		if (!this.loaded || !this.user || !this.crypto.available()) return this.writing;
		const snapshot = JSON.stringify({ user: this.user, links: [...this.urls.entries()] });
		this.writing = this.writing.then(async () => {
			await fs.mkdir(path.dirname(this.file), { recursive: true, mode: 0o700 });
			const temporary = `${this.file}.${process.pid}.tmp`;
			await fs.writeFile(temporary, this.crypto.encrypt(snapshot), { mode: 0o600 });
			await fs.rename(temporary, this.file);
		}).catch(() => undefined);
		return this.writing;
	}
}

/** Refuses a call from anything but a window's main frame, where the bridge lives. */
function requireMainFrame(event: unknown): void {
	const { senderFrame } = event as Partial<IpcMainInvokeEvent>;
	if (senderFrame === null || (senderFrame && senderFrame.parent !== null)) {
		throw new Error("Share links are only available from the app window.");
	}
}

function linkIds(value: unknown): string[] {
	if (!Array.isArray(value) || value.length > MAX_LINKS) throw new Error("Not a list of share links.");
	return value.filter((id): id is string => typeof id === "string" && LINK_ID.test(id));
}

function baseUrlArgument(value: unknown): string {
	const url = shareUrl(value);
	if (!url) throw new Error("Not a control plane.");
	return url;
}

export function registerSessionShareUrlsIpc(ipcMain: Pick<IpcMain, "handle">, store: SessionShareUrls): void {
	ipcMain.handle("sessionShareUrls:remember", async (event, input: { baseUrl?: unknown; linkId?: unknown; url?: unknown }) => {
		requireMainFrame(event);
		const url = shareUrl(input?.url);
		if (!url || typeof input?.linkId !== "string" || !LINK_ID.test(input.linkId)) throw new Error("Not a share link.");
		await store.remember(baseUrlArgument(input.baseUrl), input.linkId, url);
	});
	ipcMain.handle("sessionShareUrls:recall", async (event, input: { baseUrl?: unknown; linkIds?: unknown }) => {
		requireMainFrame(event);
		return store.recall(baseUrlArgument(input?.baseUrl), linkIds(input?.linkIds));
	});
	ipcMain.handle("sessionShareUrls:forget", async (event, input: { baseUrl?: unknown; linkIds?: unknown }) => {
		requireMainFrame(event);
		await store.forget(baseUrlArgument(input?.baseUrl), linkIds(input?.linkIds));
	});
}
