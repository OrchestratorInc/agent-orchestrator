import { promises as fs } from "node:fs";
import path from "node:path";
import type { IpcMain, IpcMainInvokeEvent } from "electron";

export interface SessionShareUrlCrypto {
	available(): boolean;
	encrypt(text: string): Buffer;
	decrypt(value: Buffer): string;
}

export type SessionShareUrlOwner = () => Promise<string | undefined> | string | undefined;

const MAX_URL_LENGTH = 512;
const MAX_LINKS = 500;
const LINK_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function key(baseUrl: string, linkId: string): string {
	return JSON.stringify([baseUrl.replace(/\/+$/, ""), linkId.toLowerCase()]);
}

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
	private urls = new Map<string, string>();
	private user: string | undefined;
	private forgotten = new Set<string>();
	private loaded = false;
	private generation = 0;
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

	async clear(): Promise<void> {
		if (this.empty) return;
		this.empty = true;
		this.urls = new Map();
		this.forgotten.clear();
		this.generation += 1;
		this.loaded = true;
		this.writing = this.writing.then(() => fs.rm(this.file, { force: true })).catch(() => undefined);
		await this.writing;
	}

	private async load(): Promise<Map<string, string> | undefined> {
		const user = await this.owner();
		if (!user) return undefined;
		if (user !== this.user) {
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
			if (generation === this.generation && (error as NodeJS.ErrnoException).code === "ENOENT") this.loaded = true;
			return this.urls;
		}
		if (generation !== this.generation) return this.urls;
		let parsed: unknown;
		try {
			parsed = JSON.parse(this.crypto.decrypt(raw));
		} catch {
			parsed = undefined;
		}
		const saved = parsed as { user?: unknown; links?: unknown } | undefined;
		const links = saved?.user === user && Array.isArray(saved.links) ? saved.links : [];
		if (saved && saved.user !== user) {
			this.writing = this.writing.then(() => fs.rm(this.file, { force: true })).catch(() => undefined);
		}
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
