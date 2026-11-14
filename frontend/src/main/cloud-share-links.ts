/**
 * Which share link each local artifact file was last published to, so Share
 * on a published file opens to its link instead of offering to publish it
 * again. A record is only a hint: the renderer shows a link when the
 * signed-in user's own list has it active, so a record for another account
 * or for a link unpublished elsewhere is ignored.
 */
import { promises as fs } from "node:fs";
import path from "node:path";
import { type CloudShareAccess, type CloudShareLink, artifactShareUrl, isPrivateShare } from "../shared/cloud-share";

/**
 * The record key of one file by its control plane, session and path. A file
 * with an artifact ID is recorded under artifactRecordKey instead; this key
 * still names records written before artifacts had one.
 */
export function shareRecordKey(baseUrl: string, sessionId: string, relativePath: string): string {
	return JSON.stringify([baseUrl, sessionId, relativePath]);
}

/**
 * The record key of one artifact: its control plane, session and ID. The ID
 * survives a rename, so the renamed file finds the link it was published to.
 * The empty third part keeps it apart from every path key.
 */
export function artifactRecordKey(baseUrl: string, sessionId: string, artifactId: string): string {
	return JSON.stringify([baseUrl, sessionId, "", artifactId]);
}

/** One file's record: the link it was published to, and what it held then. */
interface ShareLinkRecord {
	shareId: string;
	/** The file's fingerprint when it was published; see shareFileFingerprint (a cloud file's SHA-256). */
	fingerprint?: string;
	url?: string;
	/** Who the link opens for, as last known; the daemon serves it with the URL. */
	access?: CloudShareAccess;
}

export interface ShareLinkRecordStore {
	get(key: string): Promise<string | undefined>;
	/** The fingerprint the file had when it was last published, if recorded. */
	publishedFingerprint(key: string): Promise<string | undefined>;
	set(key: string, shareId: string, fingerprint?: string, url?: string, access?: CloudShareAccess): Promise<void>;
	/** Records who a link opens for now, on every record of it. */
	setAccess(shareId: string, access: CloudShareAccess): Promise<void>;
	/** Moves the record at `from` to `to`, unless `to` already has one. */
	adopt(to: string, from: string): Promise<void>;
	/** Drops every record of a link, after it is unpublished. */
	forgetShare(shareId: string): Promise<void>;
	/**
	 * The current write generation. Taken before a link list is requested and
	 * passed to syncLinks, so a list that was in flight while a link was
	 * published, changed or unpublished here does not undo that write.
	 */
	generation(): number;
	/**
	 * Brings records up to date with a link list. With `since`, a link written
	 * here after that generation is left as written: the list predates it.
	 */
	syncLinks(baseUrl: string, shares: CloudShareLink[], since?: number): Promise<void>;
}

/**
 * A ShareLinkRecordStore kept as a private JSON file. A record written
 * before fingerprints were kept is the link's ID alone.
 */
export class ShareLinkRecords implements ShareLinkRecordStore {
	private records: Record<string, string | ShareLinkRecord> | undefined;
	private writing: Promise<void> = Promise.resolve();
	/** Bumped by every write of a link's record, here; each link keeps the one it was last written at. */
	private writes = 0;
	private writtenAt = new Map<string, number>();

	constructor(private readonly file: string) {}

	generation(): number {
		return this.writes;
	}

	private written(shareId: string): void {
		this.writes += 1;
		this.writtenAt.set(shareId, this.writes);
	}

	async get(key: string): Promise<string | undefined> {
		return asRecord((await this.load())[key])?.shareId;
	}

	async publishedFingerprint(key: string): Promise<string | undefined> {
		return asRecord((await this.load())[key])?.fingerprint;
	}

	async set(key: string, shareId: string, fingerprint?: string, url?: string, access?: CloudShareAccess): Promise<void> {
		const records = await this.load();
		this.written(shareId);
		records[key] = encodeRecord({ shareId, fingerprint, url, access });
		await this.save();
	}

	async setAccess(shareId: string, access: CloudShareAccess): Promise<void> {
		const records = await this.load();
		this.written(shareId);
		let changed = false;
		for (const [key, value] of Object.entries(records)) {
			const record = asRecord(value);
			if (record?.shareId !== shareId || record.access === access) continue;
			records[key] = encodeRecord({ ...record, access });
			changed = true;
		}
		if (changed) await this.save();
	}

	async adopt(to: string, from: string): Promise<void> {
		const records = await this.load();
		if (to === from || records[to] !== undefined || records[from] === undefined) return;
		records[to] = records[from];
		delete records[from];
		await this.save();
	}

	async syncLinks(baseUrl: string, shares: CloudShareLink[], since?: number): Promise<void> {
		const byId = new Map(shares.map((share) => [share.id, share]));
		const records = await this.load();
		let changed = false;
		for (const [key, value] of Object.entries(records)) {
			const record = asRecord(value);
			if (!record || recordBaseUrl(key) !== baseUrl) continue;
			// Written here after the list was requested: the list is older than the record.
			if (since !== undefined && (this.writtenAt.get(record.shareId) ?? 0) > since) continue;
			const share = byId.get(record.shareId);
			const active = share && !share.revokedAt ? share : undefined;
			const url = active ? artifactShareUrl(active) : undefined;
			const access = active ? shareAccess(active) : undefined;
			if (record.url === url && record.access === access) continue;
			records[key] = encodeRecord({ ...record, url, access });
			changed = true;
		}
		if (changed) await this.save();
	}

	async forgetShare(shareId: string): Promise<void> {
		const records = await this.load();
		this.written(shareId);
		let changed = false;
		for (const [key, record] of Object.entries(records)) {
			if (asRecord(record)?.shareId === shareId) {
				delete records[key];
				changed = true;
			}
		}
		if (changed) await this.save();
	}

	private async load(): Promise<Record<string, string | ShareLinkRecord>> {
		if (this.records) return this.records;
		try {
			const parsed: unknown = JSON.parse(await fs.readFile(this.file, "utf8"));
			this.records = isRecordMap(parsed) ? parsed : {};
		} catch {
			this.records = {};
		}
		return this.records;
	}

	// Writes are serialized and atomic: a crash leaves the old file or the new.
	private save(): Promise<void> {
		const snapshot = JSON.stringify(this.records ?? {});
		this.writing = this.writing.then(async () => {
			await fs.mkdir(path.dirname(this.file), { recursive: true, mode: 0o700 });
			const temporary = `${this.file}.${process.pid}.tmp`;
			await fs.writeFile(temporary, snapshot, { mode: 0o600 });
			await fs.rename(temporary, this.file);
		}).catch(() => undefined);
		return this.writing;
	}
}

/** Who a link opens for; a control plane that predates private links names none, and those are public. */
export function shareAccess(share: Pick<CloudShareLink, "access">): CloudShareAccess {
	return isPrivateShare(share) ? "restricted" : "anyone";
}

// Writes only the fields it knows: a record from an older version loses any
// other field the next time it is written.
function encodeRecord({ shareId, fingerprint, url, access }: ShareLinkRecord): string | ShareLinkRecord {
	if (!fingerprint && !url && !access) return shareId;
	return {
		shareId,
		...(fingerprint ? { fingerprint } : {}),
		...(url ? { url } : {}),
		...(access ? { access } : {}),
	};
}

function recordBaseUrl(key: string): string | undefined {
	try {
		const parts: unknown = JSON.parse(key);
		return Array.isArray(parts) && typeof parts[0] === "string" ? parts[0] : undefined;
	} catch {
		return undefined;
	}
}

function asRecord(value: string | ShareLinkRecord | undefined): ShareLinkRecord | undefined {
	return typeof value === "string" ? { shareId: value } : value;
}

function isRecordMap(value: unknown): value is Record<string, string | ShareLinkRecord> {
	return typeof value === "object" && value !== null && !Array.isArray(value) &&
		Object.values(value).every((record) => typeof record === "string" || (
			typeof record === "object" && record !== null && typeof (record as ShareLinkRecord).shareId === "string" &&
			((record as ShareLinkRecord).fingerprint === undefined || typeof (record as ShareLinkRecord).fingerprint === "string") &&
			((record as ShareLinkRecord).url === undefined || typeof (record as ShareLinkRecord).url === "string") &&
			((record as ShareLinkRecord).access === undefined || (record as ShareLinkRecord).access === "restricted" || (record as ShareLinkRecord).access === "anyone")
		));
}
