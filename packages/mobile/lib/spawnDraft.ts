import AsyncStorage from "@react-native-async-storage/async-storage";
import { Directory, File, Paths } from "expo-file-system";
import { loadAccount } from "./account";
import { isCloudHost } from "./cloud/mapping";
import { loadHosts } from "./hosts";
import type { SpawnAttachment } from "./spawn-attachments";

// The New task page's draft: the prompt and its attachments, saved on the
// phone so they survive the phone closing the app in the background (cheaper
// phones do it often) and leaving the page by accident. The prompt goes to
// AsyncStorage; attachment bytes, which can be megabytes, go to files in the
// app's own documents folder. Cleared once the task starts.
//
// A draft belongs to the machine it was written for and the account signed in
// then: it is not restored once that machine is forgotten or that account has
// signed out (or another signed in), and it is deleted when either happens.

export const SPAWN_DRAFT_KEY = "ao.spawn.draft.v1";
/** A draft untouched for this long is not restored. */
export const SPAWN_DRAFT_KEEP_MS = 7 * 24 * 60 * 60 * 1000;

/** Who a draft was written for: the machine (or Orchestrator Cloud) picked, and the account signed in. */
export type SpawnDraftOwner = { hostId: string | null; accountId: string | null };
/**
 * The last Start sent from the draft: its request id and what it asked for.
 * Sent again unchanged, with the same id, the daemon answers with the task it
 * already started instead of starting a second one.
 */
export type SpawnDraftRequest = { id: string; payload: string };

type StoredAttachment = Omit<SpawnAttachment, "data" | "previewUri"> & { file: string; image: boolean };
type StoredDraft = { v: 1; savedAt: number; prompt: string; attachments: StoredAttachment[]; owner?: SpawnDraftOwner; request?: SpawnDraftRequest };

export type SpawnDraft = { prompt: string; attachments: SpawnAttachment[]; owner?: SpawnDraftOwner; request?: SpawnDraftRequest };

/** What a restored draft is checked against: the account signed in now, and the machines still paired. */
export type SpawnDraftContext = () => Promise<{ accountId: string | null; hostIds: readonly string[] }>;

const currentContext: SpawnDraftContext = async () => {
	const [account, hosts] = await Promise.all([loadAccount(), loadHosts()]);
	return { accountId: account?.id ?? null, hostIds: hosts.map((host) => host.id) };
};

function folder(): Directory {
	return new Directory(Paths.document, "spawn-draft");
}

/** FNV-1a over the attachment's bytes (as base64): names a file by what is in it. */
function contentHash(data: string): string {
	let hash = 0x811c9dc5;
	for (let index = 0; index < data.length; index++) {
		hash ^= data.charCodeAt(index);
		hash = Math.imul(hash, 0x01000193);
	}
	return (hash >>> 0).toString(36);
}

/**
 * A file name that stays with its attachment: from its contents, not its place
 * in the list, so removing one never leaves another's restored preview
 * pointing at a file that was deleted or rewritten under it.
 */
function fileName(attachment: SpawnAttachment): string {
	return `${contentHash(attachment.data)}-${attachment.data.length.toString(36)}-${attachment.name.replace(/[^\w.-]+/g, "_").slice(-80) || "file"}`;
}

function readStored(raw: string | null): StoredDraft | null {
	if (!raw) return null;
	const stored = JSON.parse(raw) as Partial<StoredDraft>;
	if (stored.v !== 1 || typeof stored.prompt !== "string" || typeof stored.savedAt !== "number") return null;
	return stored as StoredDraft;
}

/**
 * The saved draft, or null when there is none, it is a week old, it cannot be
 * read, or it belongs to an account no longer signed in or a machine no longer paired.
 */
export async function loadSpawnDraft(now = Date.now(), context: SpawnDraftContext = currentContext): Promise<SpawnDraft | null> {
	try {
		const stored = readStored(await AsyncStorage.getItem(SPAWN_DRAFT_KEY));
		if (!stored) return null;
		if (now - stored.savedAt >= SPAWN_DRAFT_KEEP_MS) {
			await clearSpawnDraft();
			return null;
		}
		const owner = stored.owner ?? { hostId: null, accountId: null };
		const { accountId, hostIds } = await context();
		const accountGone = owner.accountId !== null && owner.accountId !== accountId;
		const machineGone = owner.hostId !== null && !isCloudHost(owner.hostId) && !hostIds.includes(owner.hostId);
		if (accountGone || machineGone) {
			await clearSpawnDraft();
			return null;
		}
		const attachments: SpawnAttachment[] = [];
		for (const saved of Array.isArray(stored.attachments) ? stored.attachments : []) {
			const file = new File(folder(), saved.file);
			// A file the system cleared is left out; the rest of the draft still comes back.
			if (!file.exists) continue;
			attachments.push({
				name: saved.name,
				mimeType: saved.mimeType,
				bytes: saved.bytes,
				data: await file.base64(),
				...(saved.image ? { previewUri: file.uri } : {}),
			});
		}
		return { prompt: stored.prompt, attachments, owner, ...(stored.request ? { request: stored.request } : {}) };
	} catch {
		return null;
	}
}

/**
 * Saves the draft, replacing the last one; an empty draft clears it. Files are
 * written before the draft that names them is, and the last draft's files are
 * deleted only after: a save that fails part-way leaves the last draft whole.
 */
export async function saveSpawnDraft(draft: SpawnDraft, now = Date.now()): Promise<void> {
	if (!draft.prompt.trim() && draft.attachments.length === 0) return clearSpawnDraft();
	const dir = folder();
	if (draft.attachments.length) dir.create({ intermediates: true, idempotent: true });
	const attachments = draft.attachments.map((attachment): StoredAttachment => {
		const name = fileName(attachment);
		if (!new File(dir, name).exists) {
			// Written aside, then renamed into place: a write that fails part-way
			// never leaves a file under the name a draft points at.
			const partial = new File(dir, `${name}.partial`);
			if (partial.exists) partial.delete();
			partial.write(attachment.data, { encoding: "base64" });
			partial.rename(name);
		}
		return { name: attachment.name, mimeType: attachment.mimeType, bytes: attachment.bytes, file: name, image: Boolean(attachment.previewUri) };
	});
	const stored: StoredDraft = {
		v: 1,
		savedAt: now,
		prompt: draft.prompt,
		attachments,
		owner: draft.owner ?? { hostId: null, accountId: null },
		...(draft.request ? { request: draft.request } : {}),
	};
	await AsyncStorage.setItem(SPAWN_DRAFT_KEY, JSON.stringify(stored));
	try {
		const kept = new Set(attachments.map((attachment) => attachment.file));
		if (dir.exists) for (const entry of dir.list()) if (entry instanceof File && !kept.has(entry.name)) entry.delete();
	} catch {
		// A file left over is deleted by the next save, or with the draft.
	}
}

export async function clearSpawnDraft(): Promise<void> {
	try {
		const dir = folder();
		if (dir.exists) dir.delete();
	} catch {
		// A folder that cannot be removed is overwritten by the next save.
	}
	await AsyncStorage.removeItem(SPAWN_DRAFT_KEY);
}

/** Deletes the draft if it was written for `hostId` (a forgotten machine) or while `accountId` (signed out) was signed in. */
export async function forgetSpawnDraftFor(owner: { hostId?: string; accountId?: string }): Promise<void> {
	let stored: StoredDraft | null = null;
	try {
		stored = readStored(await AsyncStorage.getItem(SPAWN_DRAFT_KEY));
	} catch {
		// Unreadable: nothing anyone can restore either.
	}
	if (!stored) return;
	const written = stored.owner ?? { hostId: null, accountId: null };
	if ((owner.hostId !== undefined && written.hostId === owner.hostId) || (owner.accountId !== undefined && written.accountId === owner.accountId)) await clearSpawnDraft();
}

/**
 * Saves after the draft stops changing for a moment, writes at once when asked
 * (leaving the app), and runs one save at a time so an older draft never lands last.
 */
export function createSpawnDraftSaver(save: (draft: SpawnDraft) => Promise<void> = saveSpawnDraft, delayMs = 500) {
	let pending: SpawnDraft | null = null;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let chain = Promise.resolve();
	const flush = () => {
		clearTimeout(timer);
		timer = undefined;
		if (!pending) return chain;
		const draft = pending;
		pending = null;
		chain = chain.then(() => save(draft)).catch(() => {});
		return chain;
	};
	return {
		schedule(draft: SpawnDraft) {
			pending = draft;
			clearTimeout(timer);
			timer = setTimeout(() => void flush(), delayMs);
		},
		flush,
		/** Forgets a save still waiting: the task started, and the draft is cleared instead. */
		cancel() {
			clearTimeout(timer);
			timer = undefined;
			pending = null;
			return chain;
		},
	};
}
