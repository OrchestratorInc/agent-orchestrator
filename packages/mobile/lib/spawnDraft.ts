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
 * The last Start sent from the draft: its request id, what it asked for, and
 * which attachments it sent (by content, see `attachmentsKey`). Sent again
 * unchanged, with the same id, the daemon answers with the task it already
 * started instead of starting a second one.
 */
/**
 * A Start's identity, kept with the draft. `model` is the model it sent when
 * that was not the person's pick (the default row's, the catalog's default), so
 * a retry sends the same body even if the catalog's default changed meanwhile.
 */
export type SpawnDraftRequest = { id: string; payload: string; attachments: string; model?: string };
/**
 * What was picked on the page besides the prompt: the project, an agent, a
 * model, a level, an approval mode, the session mode. Each goes into a Start,
 * so a restored draft sends what the one before it sent.
 */
export type SpawnDraftSelections = {
	projectId?: string;
	/** Present only when an agent was picked, not resolved from the project. */
	harness?: string;
	/** Present only when a model was picked. */
	model?: string;
	effort?: string;
	/** Present only when Fast mode was turned on and the task could start in it. */
	fastMode?: boolean;
	/** Present only when Ultracode was turned on, so a restored draft still asks for it. */
	ultracode?: boolean;
	approvalMode?: string;
	mode?: string;
	/** Present only when an agent account was picked, not the default one; for `accountHarness` on the draft's machine. */
	accountId?: string;
	accountHarness?: string;
};

type StoredAttachment = Omit<SpawnAttachment, "data" | "previewUri"> & { file: string; image: boolean };
type StoredDraft = { v: 1; savedAt: number; prompt: string; attachments: StoredAttachment[]; owner?: SpawnDraftOwner; request?: SpawnDraftRequest; selections?: SpawnDraftSelections };

export type SpawnDraft = { prompt: string; attachments: SpawnAttachment[]; owner?: SpawnDraftOwner; request?: SpawnDraftRequest; selections?: SpawnDraftSelections };

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

// An attachment's bytes never change once picked, so each is hashed once: the
// draft is saved on every pause in typing, and hashing a few photos' worth of
// base64 each time is seconds of work on a slow phone.
const contentKeys = new WeakMap<SpawnAttachment, string>();

function contentKey(attachment: SpawnAttachment): string {
	const known = contentKeys.get(attachment);
	if (known !== undefined) return known;
	const key = `${contentHash(attachment.data)}-${attachment.data.length.toString(36)}`;
	contentKeys.set(attachment, key);
	return key;
}

/**
 * A file name that stays with its attachment: from its contents, not its place
 * in the list, so removing one never leaves another's restored preview
 * pointing at a file that was deleted or rewritten under it.
 */
function fileName(attachment: SpawnAttachment): string {
	return `${contentKey(attachment)}-${attachment.name.replace(/[^\w.-]+/g, "_").slice(-80) || "file"}`;
}

/**
 * The attachments as a Start sends them, in order: each one's type and bytes,
 * not its name, which a Start does not send. The same files picked again, or
 * read back from a restored draft, give the same key.
 */
export function attachmentsKey(attachments: readonly SpawnAttachment[]): string {
	return JSON.stringify(attachments.map((attachment) => [attachment.mimeType, contentKey(attachment)]));
}

/**
 * Whether a Start failed with a refusal the server answered (a 4xx), so no task
 * started and the next Start may go under a new request id with the model shown
 * then. A lost answer, a timeout, a conflict (409) or a rate limit (429) may
 * have started one, and keep the id so the daemon answers with that task. Read
 * from the daemon client's ApiError or a CloudError, also as a wrapped cause.
 */
export function startRefused(error: unknown): boolean {
	const statusOf = (value: unknown) => {
		const status = (value as { status?: unknown } | null | undefined)?.status;
		return typeof status === "number" ? status : undefined;
	};
	const status = statusOf(error) ?? statusOf((error as { cause?: unknown } | null | undefined)?.cause);
	return status !== undefined && status >= 400 && status < 500 && status !== 408 && status !== 409 && status !== 429;
}

/** Whether a Start would send exactly what `request` sent: then it reuses its id. */
export function requestMatches(request: SpawnDraftRequest | undefined, payload: string, attachments: readonly SpawnAttachment[]): request is SpawnDraftRequest {
	return Boolean(request && request.payload === payload && request.attachments === attachmentsKey(attachments));
}

/**
 * The model a request saved before `model` was kept sent: then a picked model
 * went only in the payload. Its retry matches that payload and reuses the id,
 * so it must send that model again: the daemon fingerprints the body.
 */
function payloadModel(payload: string): string | undefined {
	try {
		const parsed = JSON.parse(payload) as { model?: unknown } | null;
		return parsed && typeof parsed.model === "string" && parsed.model ? parsed.model : undefined;
	} catch {
		return undefined;
	}
}

function readRequest(value: unknown): SpawnDraftRequest | undefined {
	const request = value as Partial<SpawnDraftRequest> | null | undefined;
	if (!(request && typeof request.id === "string" && typeof request.payload === "string" && typeof request.attachments === "string")) return undefined;
	const model = typeof request.model === "string" && request.model ? request.model : request.model === undefined ? payloadModel(request.payload) : undefined;
	return { id: request.id, payload: request.payload, attachments: request.attachments, ...(model ? { model } : {}) };
}

function readSelections(value: unknown): SpawnDraftSelections | undefined {
	if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
	const selections: SpawnDraftSelections = {};
	for (const key of ["projectId", "harness", "model", "effort", "approvalMode", "mode", "accountId", "accountHarness"] as const) {
		const field = (value as Record<string, unknown>)[key];
		if (typeof field === "string") selections[key] = field;
	}
	if ((value as Record<string, unknown>).fastMode === true) selections.fastMode = true;
	if ((value as Record<string, unknown>).ultracode === true) selections.ultracode = true;
	return selections;
}

function readStored(raw: string | null): StoredDraft | null {
	if (!raw) return null;
	const stored = JSON.parse(raw) as Partial<StoredDraft>;
	if (stored.v !== 1 || typeof stored.prompt !== "string" || typeof stored.savedAt !== "number") return null;
	// A request or selections not shaped as this version writes them are left out.
	const { request: rawRequest, selections: rawSelections, ...rest } = stored as StoredDraft;
	const request = readRequest(rawRequest);
	const selections = readSelections(rawSelections);
	return { ...rest, ...(request ? { request } : {}), ...(selections ? { selections } : {}) };
}

/**
 * The saved draft, or null when there is none, it is a week old, it is not a
 * draft this version wrote, or it belongs to an account no longer signed in or
 * a machine no longer paired. Rejects when storage could not be read: the
 * draft may still be there, and the page must not save over it as empty.
 */
export function loadSpawnDraft(now = Date.now(), context: SpawnDraftContext = currentContext): Promise<SpawnDraft | null> {
	// In the draft queue: a save from another page cannot commit a new draft
	// and delete this one's attachment files while they are read.
	return inDraftQueue(() => readDraft(now, context));
}

async function readDraft(now: number, context: SpawnDraftContext): Promise<SpawnDraft | null> {
	const raw = await AsyncStorage.getItem(SPAWN_DRAFT_KEY);
	let stored: StoredDraft | null;
	try {
		stored = readStored(raw);
	} catch {
		// Unparseable: nothing anyone can restore.
		return null;
	}
	if (!stored) return null;
	if (now - stored.savedAt >= SPAWN_DRAFT_KEEP_MS) {
		// Too old to restore; a removal that fails is tried on the next read.
		// Already in the queue, so removed directly.
		await removeDraft().catch(() => {});
		return null;
	}
	const owner = stored.owner ?? { hostId: null, accountId: null };
	const { accountId, hostIds } = await context();
	const accountGone = owner.accountId !== null && owner.accountId !== accountId;
	const machineGone = owner.hostId !== null && !isCloudHost(owner.hostId) && !hostIds.includes(owner.hostId);
	if (accountGone || machineGone) {
		await removeDraft().catch(() => {});
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
	return { prompt: stored.prompt, attachments, owner, ...(stored.request ? { request: stored.request } : {}), ...(stored.selections ? { selections: stored.selections } : {}) };
}

// Every write, commit and removal of the draft, from every open page's saver
// and from clearing it, runs here one at a time: two pages' saves (a cloud
// project's page left open under a second New task) must not delete each
// other's attachment files between writing them and committing the draft.
let draftQueue: Promise<unknown> = Promise.resolve();
function inDraftQueue<T>(task: () => Promise<T>): Promise<T> {
	const run = draftQueue.then(task);
	draftQueue = run.catch(() => {});
	return run;
}

/**
 * Saves the draft, replacing the last one; an empty draft clears it. Files are
 * written before the draft that names them is, and the last draft's files are
 * deleted only after: a save that fails part-way leaves the last draft whole.
 */
export async function saveSpawnDraft(draft: SpawnDraft, now = Date.now()): Promise<void> {
	// Already in the draft queue (see createSpawnDraftSaver): cleared directly.
	if (!draft.prompt.trim() && draft.attachments.length === 0) return removeDraft();
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
		...(draft.selections ? { selections: draft.selections } : {}),
	};
	await AsyncStorage.setItem(SPAWN_DRAFT_KEY, JSON.stringify(stored));
	try {
		const kept = new Set(attachments.map((attachment) => attachment.file));
		if (dir.exists) for (const entry of dir.list()) if (entry instanceof File && !kept.has(entry.name)) entry.delete();
	} catch {
		// A file left over is deleted by the next save, or with the draft.
	}
}

/**
 * Deletes the draft, then its files: a removal that fails keeps the files the
 * draft still names, so a restored draft resends the same attachments (and
 * reuses its request id) rather than coming back without them.
 */
export function clearSpawnDraft(): Promise<void> {
	return inDraftQueue(removeDraft);
}

/**
 * Clears the draft a Start began a task with, `requestId` the id it was saved
 * with before the request left: only while the phone still holds it. A draft
 * another open New task page saved meanwhile is someone's next task, and stays.
 */
export function clearStartedSpawnDraft(requestId: string): Promise<void> {
	return inDraftQueue(async () => {
		if (readStoredOrNull(await AsyncStorage.getItem(SPAWN_DRAFT_KEY))?.request?.id === requestId) await removeDraft();
	});
}

function readStoredOrNull(raw: string | null): StoredDraft | null {
	try {
		return readStored(raw);
	} catch {
		// Unparseable: nothing anyone can restore.
		return null;
	}
}

async function removeDraft(): Promise<void> {
	await AsyncStorage.removeItem(SPAWN_DRAFT_KEY);
	try {
		const dir = folder();
		if (dir.exists) dir.delete();
	} catch {
		// A folder that cannot be removed is overwritten by the next save.
	}
}

type ForgetOwner = { hostId?: string; accountId?: string };

function ownedBy(written: SpawnDraftOwner | undefined, owner: ForgetOwner): boolean {
	const by = written ?? { hostId: null, accountId: null };
	return (owner.hostId !== undefined && by.hostId === owner.hostId) || (owner.accountId !== undefined && by.accountId === owner.accountId);
}

/**
 * Deletes the draft if it was written for `hostId` (a forgotten machine) or
 * while `accountId` (signed out) was signed in. Saves of such a draft still
 * waiting on an open page are dropped first, and one already writing finishes
 * before the draft is deleted: none can write it back afterwards.
 */
export async function forgetSpawnDraftFor(owner: ForgetOwner): Promise<void> {
	await Promise.all([...draftSavers].map((saver) => saver.forget(owner)));
	// Read, checked and removed in one turn of the queue: another owner's save
	// committed in between is not deleted on this owner's account. A read that
	// fails rejects, so the caller can try again or report it: the draft may
	// still be there.
	await inDraftQueue(async () => {
		const stored = readStoredOrNull(await AsyncStorage.getItem(SPAWN_DRAFT_KEY));
		if (stored && ownedBy(stored.owner, owner)) await removeDraft();
	});
}

type SpawnDraftSaverHandle = { forget(owner: ForgetOwner): Promise<void> };
/** Saved, failed, or dropped by a cancel or a forget before it was written. */
export type SpawnDraftSaveOutcome = boolean | "dropped";
/** The savers of the pages open now, so forgetting reaches the saves they hold. */
const draftSavers = new Set<SpawnDraftSaverHandle>();

/**
 * Saves after the draft stops changing for a moment, writes at once when asked
 * (leaving the app), and runs one save at a time (in the queue every saver
 * shares) so an older draft never lands last. `flush` resolves to whether the
 * save it ran (or the last one) reached the phone, or to "dropped" when a
 * cancel or a forget dropped it before it ran; it never rejects. A save that
 * failed stays waiting, unless a newer draft replaced it, so the next flush
 * (leaving the app) tries it again.
 */
export function createSpawnDraftSaver(save: (draft: SpawnDraft) => Promise<void> = saveSpawnDraft, delayMs = 500) {
	let pending: SpawnDraft | null = null;
	// Counts drafts scheduled: a failed save is kept only while none came after it.
	let scheduled = 0;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let chain = Promise.resolve();
	let last: Promise<SpawnDraftSaveOutcome> = Promise.resolve(true);
	// Saves waiting behind the one running: a cancel or a forget drops them too.
	const queued = new Set<{ draft: SpawnDraft; dropped: boolean }>();
	const flush = (): Promise<SpawnDraftSaveOutcome> => {
		clearTimeout(timer);
		timer = undefined;
		if (!pending) return last;
		const item = { draft: pending, dropped: false, order: scheduled };
		pending = null;
		queued.add(item);
		const result = inDraftQueue(async (): Promise<SpawnDraftSaveOutcome> => {
			if (item.dropped) return "dropped";
			await save(item.draft);
			return true;
		}).catch(() => {
			// Kept for the next flush, unless a newer draft, a cancel or a forget came since.
			if (!item.dropped && !pending && item.order === scheduled) pending = item.draft;
			return false;
		}).finally(() => { queued.delete(item); });
		last = result;
		chain = result.then(() => {});
		return result;
	};
	const saver = {
		schedule(draft: SpawnDraft) {
			// Registered again if it was let go: whatever it holds, forgetting reaches.
			draftSavers.add(saver);
			scheduled += 1;
			pending = draft;
			clearTimeout(timer);
			timer = setTimeout(() => void flush(), delayMs);
		},
		flush,
		/** Forgets the saves still waiting: the task started, and the draft is cleared instead. Resolves once one already running is done. */
		cancel() {
			clearTimeout(timer);
			timer = undefined;
			pending = null;
			for (const item of queued) item.dropped = true;
			return chain;
		},
		/** Drops the saves waiting for a draft `owner` wrote; resolves once one already running is done. */
		forget(owner: ForgetOwner) {
			if (pending && ownedBy(pending.owner, owner)) {
				clearTimeout(timer);
				timer = undefined;
				pending = null;
			}
			for (const item of queued) if (ownedBy(item.draft.owner, owner)) item.dropped = true;
			return chain;
		},
		/** The page closed: once its last save is done, forgetting no longer waits on it. */
		release() {
			void chain.then(() => { draftSavers.delete(saver); });
		},
	};
	draftSavers.add(saver);
	return saver;
}

/**
 * Saves the draft a Start is about to send, with its request id, before it
 * leaves: closed after the daemon took the task, the restored draft's Start
 * reuses the id and the daemon answers with that task instead of a second one.
 *
 * A save that fails (twice) does not hold the task back. The draft on the
 * phone is deleted instead: one saved earlier has no id for this Start, and
 * restored after the app closed mid-request, its Start would start the task
 * again. The page keeps the id in memory, so a Start sent again from it still
 * reuses the id. Resolves to whether the draft was saved, or to "dropped" when
 * the account changed or the draft's machine or account was forgotten while
 * the save waited: that cleanup dropped it, and the Start does not go.
 */
export async function saveBeforeStart(saver: Pick<ReturnType<typeof createSpawnDraftSaver>, "schedule" | "flush">, draft: SpawnDraft): Promise<SpawnDraftSaveOutcome> {
	for (let attempt = 0; attempt < 2; attempt++) {
		saver.schedule(draft);
		const outcome = await saver.flush();
		if (outcome) return outcome;
	}
	await clearSpawnDraft().catch(() => {});
	return false;
}
