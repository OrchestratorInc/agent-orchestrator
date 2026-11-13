/**
 * Which share link each local artifact file was last published to, so Share
 * on a published file opens to its link instead of offering to publish it
 * again. A record is only a hint: the renderer shows a link when the
 * signed-in user's own list has it active, so a record for another account
 * or for a link unpublished elsewhere is ignored.
 */
import { promises as fs } from "node:fs";
import path from "node:path";
import { type CloudShareLink, artifactShareUrl } from "../shared/cloud-share";

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
	/** The file's fingerprint when it was published; see scanShareFile. */
	fingerprint?: string;
	url?: string;
	/** Kinds of credential the published copy looked like it held. */
	secretFindings?: string[];
}

export interface ShareLinkRecordStore {
	get(key: string): Promise<string | undefined>;
	/** The fingerprint the file had when it was last published, if recorded. */
	publishedFingerprint(key: string): Promise<string | undefined>;
	/** Kinds of credential the copy last published looked like it held, if recorded. */
	publishedSecretFindings(key: string): Promise<string[] | undefined>;
	set(key: string, shareId: string, fingerprint?: string, url?: string, secretFindings?: string[]): Promise<void>;
	/** Moves the record at `from` to `to`, unless `to` already has one. */
	adopt(to: string, from: string): Promise<void>;
	/** Drops every record of a link, after it is unpublished. */
	forgetShare(shareId: string): Promise<void>;
	syncLinks(baseUrl: string, shares: CloudShareLink[]): Promise<void>;
}

/**
 * A ShareLinkRecordStore kept as a private JSON file. A record written
 * before fingerprints were kept is the link's ID alone.
 */
export class ShareLinkRecords implements ShareLinkRecordStore {
	private records: Record<string, string | ShareLinkRecord> | undefined;
	private writing: Promise<void> = Promise.resolve();

	constructor(private readonly file: string) {}

	async get(key: string): Promise<string | undefined> {
		return asRecord((await this.load())[key])?.shareId;
	}

	async publishedFingerprint(key: string): Promise<string | undefined> {
		return asRecord((await this.load())[key])?.fingerprint;
	}

	async publishedSecretFindings(key: string): Promise<string[] | undefined> {
		return asRecord((await this.load())[key])?.secretFindings;
	}

	async set(key: string, shareId: string, fingerprint?: string, url?: string, secretFindings?: string[]): Promise<void> {
		const records = await this.load();
		records[key] = encodeRecord({ shareId, fingerprint, url, secretFindings });
		await this.save();
	}

	async adopt(to: string, from: string): Promise<void> {
		const records = await this.load();
		if (to === from || records[to] !== undefined || records[from] === undefined) return;
		records[to] = records[from];
		delete records[from];
		await this.save();
	}

	async syncLinks(baseUrl: string, shares: CloudShareLink[]): Promise<void> {
		const byId = new Map(shares.map((share) => [share.id, share]));
		const records = await this.load();
		let changed = false;
		for (const [key, value] of Object.entries(records)) {
			const record = asRecord(value);
			if (!record || recordBaseUrl(key) !== baseUrl) continue;
			const share = byId.get(record.shareId);
			const url = share && !share.revokedAt ? artifactShareUrl(share) : undefined;
			if (record.url === url) continue;
			records[key] = encodeRecord({ ...record, url });
			changed = true;
		}
		if (changed) await this.save();
	}

	async forgetShare(shareId: string): Promise<void> {
		const records = await this.load();
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

function encodeRecord({ shareId, fingerprint, url, secretFindings }: ShareLinkRecord): string | ShareLinkRecord {
	const findings = secretFindings?.length ? secretFindings : undefined;
	if (!fingerprint && !url && !findings) return shareId;
	return { shareId, ...(fingerprint ? { fingerprint } : {}), ...(url ? { url } : {}), ...(findings ? { secretFindings: findings } : {}) };
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
			((record as ShareLinkRecord).secretFindings === undefined || (Array.isArray((record as ShareLinkRecord).secretFindings) &&
				(record as ShareLinkRecord).secretFindings!.every((kind) => typeof kind === "string")))
		));
}
