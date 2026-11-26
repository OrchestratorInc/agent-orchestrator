/**
 * Which session works from a link someone shared with the person, so "Start a
 * session with this" opens that session again instead of making another copy,
 * and where every such copy is, so none is published again as the person's own.
 *
 * One JSON file under ~/.ao holds both:
 * - links: one record per account and link. It names the session, the copy
 *   (its folder and entry) and the version of the link it holds: the link's
 *   updatedAt and a fingerprint of the copied bytes, so a new date with the
 *   same content is not an update, and a version the person chose to keep
 *   theirs over is not offered again.
 * - copies: every session folder a copy was ever written to. It only grows: a
 *   link starting a new session ("Continue" on an archived one) leaves the
 *   earlier session's copy marked as a copy.
 */

import { createHash } from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { AoError } from "../../../packages/shared/telemetry/ao-error";

export interface SharedArtifactSessionRecord {
	sessionId: string;
	/** The copy's folder and file, relative to the session's artifact folder. */
	dir: string;
	entry: string;
	/** The link's updatedAt when it was copied. */
	updatedAt: string;
	/** copySharedArtifact's fingerprint of what was copied. */
	fingerprint: string;
	/** A newer version the person chose to keep their copy over. */
	declinedFingerprint?: string;
}

export interface SharedArtifactSessionKey {
	/** The control plane the account belongs to. */
	baseUrl: string;
	userId: string;
	linkId: string;
}

/** A session folder holding a copy of someone else's shared artifact. */
export interface SharedArtifactCopyPlace {
	sessionId: string;
	dir: string;
}

type RecordMap = Record<string, SharedArtifactSessionRecord>;

interface Store {
	links: RecordMap;
	copies: SharedArtifactCopyPlace[];
}

export function sharedArtifactSessionKey({ baseUrl, userId, linkId }: SharedArtifactSessionKey): string {
	return JSON.stringify([baseUrl, userId, linkId]);
}

function validKey(key: SharedArtifactSessionKey): boolean {
	return [key.baseUrl, key.userId, key.linkId].every((part) => typeof part === "string" && part.length > 0 && part.length <= 2048);
}

/** A copy's folder: always under shared/, never climbing out of it. */
export function isCopyDir(dir: unknown): dir is string {
	return typeof dir === "string" && dir.startsWith("shared/") && dir.length <= 1024
		&& dir.split("/").every((part) => part !== "" && part !== "." && part !== "..");
}

function isRecord(value: unknown): value is SharedArtifactSessionRecord {
	if (!value || typeof value !== "object") return false;
	const record = value as Record<string, unknown>;
	return ["sessionId", "dir", "entry", "updatedAt", "fingerprint"].every((field) => typeof record[field] === "string")
		&& isCopyDir(record.dir) && (record.entry as string).startsWith(`${record.dir}/`)
		&& (record.declinedFingerprint === undefined || typeof record.declinedFingerprint === "string");
}

function isPlace(value: unknown): value is SharedArtifactCopyPlace {
	if (!value || typeof value !== "object") return false;
	const place = value as Record<string, unknown>;
	return typeof place.sessionId === "string" && place.sessionId !== "" && isCopyDir(place.dir);
}

/** Whether a current file's links and copies are the shapes this version writes. */
function wellShaped(parsed: Record<string, unknown>): boolean {
	const { links, copies } = parsed;
	return Boolean(links) && typeof links === "object" && !Array.isArray(links)
		&& Object.values(links as Record<string, unknown>).every(isRecord)
		&& Array.isArray(copies) && copies.every(isPlace);
}

/** The records and places in parsed, which is the current file or the first version's plain map of records. */
function storeFrom(parsed: Record<string, unknown>): Store {
	const links: RecordMap = {};
	const copies: SharedArtifactCopyPlace[] = [];
	const linkMap = parsed.version === 2 ? parsed.links : parsed;
	if (linkMap && typeof linkMap === "object" && !Array.isArray(linkMap)) {
		for (const [key, value] of Object.entries(linkMap)) if (isRecord(value)) links[key] = value;
	}
	if (parsed.version === 2 && Array.isArray(parsed.copies)) {
		for (const place of parsed.copies) if (isPlace(place)) copies.push({ sessionId: place.sessionId, dir: place.dir });
	}
	for (const record of Object.values(links)) addPlace(copies, { sessionId: record.sessionId, dir: record.dir });
	return { links, copies };
}

function addPlace(copies: SharedArtifactCopyPlace[], place: SharedArtifactCopyPlace): void {
	if (copies.some((copy) => copy.sessionId === place.sessionId && copy.dir === place.dir)) return;
	// Never trimmed: a mark dropped would make that copy publishable.
	copies.push(place);
}

/** Whether a session's artifact file at relativePath is in one of copies. */
export function inCopyPlace(copies: readonly SharedArtifactCopyPlace[], sessionId: string, relativePath: string): boolean {
	return copies.some((copy) => copy.sessionId === sessionId && (relativePath === copy.dir || relativePath.startsWith(`${copy.dir}/`)));
}

/** The hidden file the daemon writes in a copy's folder (sessionartifacts.SharedCopyMarker). */
export const SHARED_COPY_MARKER = ".ao-shared-copy";

/**
 * Whether a folder above relativePath in a session's artifact folder holds
 * the daemon's copy marker. The daemon writes it with the copy, so a copy is
 * marked even when this file's own record of it was never saved.
 */
export async function hasCopyMarker(artifactRoot: string, relativePath: string): Promise<boolean> {
	const parts = relativePath.replace(/\\/g, "/").split("/").filter(Boolean);
	for (let depth = parts.length - 1; depth >= 1; depth--) {
		try {
			await fs.access(path.join(artifactRoot, ...parts.slice(0, depth), SHARED_COPY_MARKER));
			return true;
		} catch {
			// Not marked at this depth.
		}
	}
	return false;
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/** fs.rename, tried again for a moment on Windows, where antivirus or a backup holding the file makes it fail. */
async function renameRetrying(from: string, to: string): Promise<void> {
	for (let attempt = 0; ; attempt++) {
		try {
			await fs.rename(from, to);
			return;
		} catch (error) {
			const code = (error as NodeJS.ErrnoException).code;
			if (process.platform !== "win32" || attempt >= 5 || (code !== "EPERM" && code !== "EBUSY" && code !== "EACCES")) throw error;
			await sleep(20 * (attempt + 1));
		}
	}
}

export class SharedArtifactSessions {
	private store: Store | undefined;
	private writing: Promise<void> = Promise.resolve();
	private loading: Promise<Store> | null = null;

	constructor(private readonly file: string) {}

	async get(key: SharedArtifactSessionKey): Promise<SharedArtifactSessionRecord | null> {
		if (!validKey(key)) return null;
		return (await this.load()).links[sharedArtifactSessionKey(key)] ?? null;
	}

	/** Every record of one account. */
	async list(account: Omit<SharedArtifactSessionKey, "linkId">): Promise<Array<SharedArtifactSessionRecord & { linkId: string }>> {
		const { links } = await this.load();
		return Object.entries(links).flatMap(([key, record]) => {
			try {
				const [baseUrl, userId, linkId] = JSON.parse(key) as string[];
				return baseUrl === account.baseUrl && userId === account.userId ? [{ ...record, linkId }] : [];
			} catch {
				return [];
			}
		});
	}

	/** Every session folder holding a copy, whichever account made it. */
	async copies(): Promise<SharedArtifactCopyPlace[]> {
		return [...(await this.load()).copies];
	}

	/** Whether a session's artifact file is part of a copy of someone else's shared artifact. */
	async isCopy(sessionId: string, relativePath: string): Promise<boolean> {
		return inCopyPlace((await this.load()).copies, sessionId, relativePath.replace(/\\/g, "/"));
	}

	async set(key: SharedArtifactSessionKey, record: SharedArtifactSessionRecord): Promise<void> {
		if (!validKey(key) || !isRecord(record)) throw new AoError("SHARED_ARTIFACT_SESSION_INVALID", "Invalid shared artifact session record");
		const store = await this.load();
		store.links[sharedArtifactSessionKey(key)] = {
			sessionId: record.sessionId,
			dir: record.dir,
			entry: record.entry,
			updatedAt: record.updatedAt,
			fingerprint: record.fingerprint,
			...(record.declinedFingerprint ? { declinedFingerprint: record.declinedFingerprint } : {}),
		};
		addPlace(store.copies, { sessionId: record.sessionId, dir: record.dir });
		await this.save();
	}

	/** Forgets which session works from the link. Its copy stays marked as a copy. */
	async remove(key: SharedArtifactSessionKey): Promise<void> {
		if (!validKey(key)) return;
		const store = await this.load();
		if (!(sharedArtifactSessionKey(key) in store.links)) return;
		delete store.links[sharedArtifactSessionKey(key)];
		await this.save();
	}

	private load(): Promise<Store> {
		if (this.store) return Promise.resolve(this.store);
		// One read for every caller; a failed one is not kept, so the next call tries again.
		this.loading ??= this.read().finally(() => {
			this.loading = null;
		});
		return this.loading;
	}

	/**
	 * The file's contents. A missing file is empty, and so is one that is not
	 * this file's JSON object, after it is kept beside it under a .bak name of
	 * its own. Any other read error (a lock held by antivirus or a backup, too
	 * many open files), or a failure to keep the .bak, is thrown, never taken
	 * for empty: the next save would otherwise drop every other link's record.
	 */
	private async read(): Promise<Store> {
		let text: string | null = null;
		try {
			text = await fs.readFile(this.file, "utf8");
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
		}
		let parsed: unknown = {};
		if (text !== null) {
			try {
				// A file saved by an editor that writes a byte-order mark is still JSON.
				parsed = JSON.parse(text.replace(/^\uFEFF/, ""));
			} catch {
				parsed = null;
			}
			if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
				await fs.copyFile(this.file, `${this.file}.${Date.now()}.bak`);
				parsed = {};
			}
			const version = (parsed as Record<string, unknown>).version;
			// A newer app's file is left as it is: writing it in this shape would drop what it holds.
			if (version !== undefined && version !== 2) throw new AoError("SHARED_ARTIFACT_SESSIONS_NEWER_VERSION", `Unknown shared artifact sessions file version: ${String(version)}`);
			if (version === 2 && !wellShaped(parsed as Record<string, unknown>)) await fs.copyFile(this.file, `${this.file}.${Date.now()}.bak`);
		}
		this.store = storeFrom(parsed as Record<string, unknown>);
		return this.store;
	}

	private save(): Promise<void> {
		const snapshot = JSON.stringify({ version: 2, links: this.store?.links ?? {}, copies: this.store?.copies ?? [] }, null, 2);
		this.writing = this.writing.catch(() => undefined).then(async () => {
			await fs.mkdir(path.dirname(this.file), { recursive: true });
			const temp = `${this.file}.${process.pid}.tmp`;
			await fs.writeFile(temp, snapshot, { mode: 0o600 });
			await renameRetrying(temp, this.file);
		});
		return this.writing;
	}
}

/**
 * Whether a local session's artifact file is a copy of an artifact someone
 * else shared: by the pairing file's marks, or by the daemon's marker. The
 * pairing file failing to read (a lock, a newer app's file) is logged and
 * falls back to the marker, so it never stops the person publishing their own work.
 */
export function sharedCopyCheck(sessions: Pick<SharedArtifactSessions, "isCopy">, artifactRoot: (sessionId: string) => string) {
	return async (sessionId: string, relativePath: string): Promise<boolean> => {
		const parts = relativePath.replace(/\\/g, "/").split("/");
		if (!/^[A-Za-z0-9._-]+$/.test(sessionId) || sessionId === "." || sessionId === ".." || parts.some((part) => part === ".." || part === "")) return false;
		try {
			if (await sessions.isCopy(sessionId, relativePath)) return true;
		} catch (error) {
			console.warn("AO: unable to read the shared artifact sessions file", error);
		}
		const root = artifactRoot(sessionId);
		return await hasCopyMarker(root, relativePath) || await isCopiedContent(root, relativePath);
	};
}

/** Files no larger than the daemon copies (50 MiB) are hashed; nothing larger is a copy. */
const MAX_HASHED_BYTES = 50 * 1024 * 1024;

/**
 * Whether the file at relativePath holds content a copy in this session was
 * made of: its sha256 is listed by one of the session's copy markers
 * (shared/<name>/.ao-shared-copy). So a copied file moved or renamed out of
 * its copy's folder is still recognised as a copy.
 */
export async function isCopiedContent(artifactRoot: string, relativePath: string): Promise<boolean> {
	let folders: string[];
	try {
		folders = await fs.readdir(path.join(artifactRoot, "shared"));
	} catch {
		return false;
	}
	const listed = new Set<string>();
	for (const folder of folders) {
		try {
			for (const line of (await fs.readFile(path.join(artifactRoot, "shared", folder, SHARED_COPY_MARKER), "utf8")).split(/\s+/)) {
				if (line) listed.add(line);
			}
		} catch {
			// Not a copy's folder.
		}
	}
	if (listed.size === 0) return false;
	try {
		const file = path.join(artifactRoot, ...relativePath.replace(/\\/g, "/").split("/"));
		const stat = await fs.stat(file);
		if (!stat.isFile() || stat.size > MAX_HASHED_BYTES) return false;
		return listed.has(createHash("sha256").update(await fs.readFile(file)).digest("hex"));
	} catch {
		return false;
	}
}
