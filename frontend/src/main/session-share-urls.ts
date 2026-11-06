/**
 * The share links for cloud sessions and projects that this device created,
 * so Copy link in the share dialog works after the dialog closes and opens
 * again. The control plane keeps only a hash of each link's code, so the URL
 * exists only where it was made: here, encrypted with the OS keychain
 * (safeStorage) and keyed by the control plane and the link's id. On another
 * device the dialog offers "Copy new link" instead.
 *
 * Without protected storage (no keychain, or Linux's basic_text backend) the
 * URLs live only in memory for this run, never as plain text on disk.
 */
import { promises as fs } from "node:fs";
import path from "node:path";
import type { IpcMain, IpcMainInvokeEvent } from "electron";

export interface SessionShareUrlCrypto {
	available(): boolean;
	encrypt(text: string): Buffer;
	decrypt(value: Buffer): string;
}

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
	private urls: Map<string, string> | undefined;
	private writing: Promise<void> = Promise.resolve();

	constructor(private readonly file: string, private readonly crypto: SessionShareUrlCrypto) {}

	async remember(baseUrl: string, linkId: string, url: string): Promise<void> {
		const urls = await this.load();
		urls.delete(key(baseUrl, linkId));
		urls.set(key(baseUrl, linkId), url);
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
			const url = urls.get(key(baseUrl, linkId));
			if (url) found[linkId] = url;
		}
		return found;
	}

	async forget(baseUrl: string, linkIds: readonly string[]): Promise<void> {
		const urls = await this.load();
		let changed = false;
		for (const linkId of linkIds) changed = urls.delete(key(baseUrl, linkId)) || changed;
		if (changed) await this.save();
	}

	private async load(): Promise<Map<string, string>> {
		if (this.urls) return this.urls;
		this.urls = new Map();
		if (!this.crypto.available()) return this.urls;
		try {
			const parsed: unknown = JSON.parse(this.crypto.decrypt(await fs.readFile(this.file)));
			if (Array.isArray(parsed)) {
				for (const item of parsed) {
					if (Array.isArray(item) && typeof item[0] === "string" && shareUrl(item[1])) this.urls.set(item[0], item[1] as string);
				}
			}
		} catch {
			// Missing, from another keychain, or damaged: start empty.
		}
		return this.urls;
	}

	// Writes are serialized and atomic: a crash leaves the old file or the new.
	private save(): Promise<void> {
		if (!this.crypto.available()) {
			this.writing = this.writing.then(() => fs.rm(this.file, { force: true })).catch(() => undefined);
			return this.writing;
		}
		const snapshot = JSON.stringify([...(this.urls ?? new Map()).entries()]);
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
