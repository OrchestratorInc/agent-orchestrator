import { promises as fs } from "node:fs";
import path from "node:path";
import { type CloudShareAccess, type CloudShareLink, artifactShareUrl, isPrivateShare } from "../shared/cloud-share";

export function shareRecordKey(baseUrl: string, sessionId: string, relativePath: string): string {
	return JSON.stringify([baseUrl, sessionId, relativePath]);
}

export function artifactRecordKey(baseUrl: string, sessionId: string, artifactId: string): string {
	return JSON.stringify([baseUrl, sessionId, "", artifactId]);
}

interface ShareLinkRecord {
	shareId: string;
	fingerprint?: string;
	url?: string;
	access?: CloudShareAccess;
}

export interface ShareLinkRecordStore {
	get(key: string): Promise<string | undefined>;
	publishedFingerprint(key: string): Promise<string | undefined>;
	set(key: string, shareId: string, fingerprint?: string, url?: string, access?: CloudShareAccess): Promise<void>;
	setAccess(shareId: string, access: CloudShareAccess): Promise<void>;
	adopt(to: string, from: string): Promise<void>;
	forgetShare(shareId: string): Promise<void>;
	generation(): number;
	syncLinks(baseUrl: string, shares: CloudShareLink[], since?: number): Promise<void>;
}

export class ShareLinkRecords implements ShareLinkRecordStore {
	private records: Record<string, string | ShareLinkRecord> | undefined;
	private writing: Promise<void> = Promise.resolve();
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

export function shareAccess(share: Pick<CloudShareLink, "access">): CloudShareAccess {
	return isPrivateShare(share) ? "restricted" : "anyone";
}

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
