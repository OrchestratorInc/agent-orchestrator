import AsyncStorage from "@react-native-async-storage/async-storage";
import { Directory, File, Paths } from "expo-file-system";
import type { SpawnAttachment } from "./spawn-attachments";

// The New task page's draft: the prompt and its attachments, saved on the
// phone so they survive the phone closing the app in the background (cheaper
// phones do it often) and leaving the page by accident. The prompt goes to
// AsyncStorage; attachment bytes, which can be megabytes, go to files in the
// app's own documents folder. Cleared once the task starts.

export const SPAWN_DRAFT_KEY = "ao.spawn.draft.v1";
/** A draft untouched for this long is not restored. */
export const SPAWN_DRAFT_KEEP_MS = 7 * 24 * 60 * 60 * 1000;

type StoredAttachment = Omit<SpawnAttachment, "data" | "previewUri"> & { file: string; image: boolean };
type StoredDraft = { v: 1; savedAt: number; prompt: string; attachments: StoredAttachment[] };

export type SpawnDraft = { prompt: string; attachments: SpawnAttachment[] };

function folder(): Directory {
	return new Directory(Paths.document, "spawn-draft");
}

function fileName(index: number, name: string): string {
	return `${index}-${name.replace(/[^\w.-]+/g, "_").slice(-80) || "file"}`;
}

/** The saved draft, or null when there is none, it is a week old, or it cannot be read. */
export async function loadSpawnDraft(now = Date.now()): Promise<SpawnDraft | null> {
	try {
		const raw = await AsyncStorage.getItem(SPAWN_DRAFT_KEY);
		if (!raw) return null;
		const stored = JSON.parse(raw) as Partial<StoredDraft>;
		if (stored.v !== 1 || typeof stored.prompt !== "string" || typeof stored.savedAt !== "number") return null;
		if (now - stored.savedAt >= SPAWN_DRAFT_KEEP_MS) {
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
		return { prompt: stored.prompt, attachments };
	} catch {
		return null;
	}
}

/** Saves the draft, replacing the last one; an empty draft clears it. */
export async function saveSpawnDraft(draft: SpawnDraft, now = Date.now()): Promise<void> {
	if (!draft.prompt.trim() && draft.attachments.length === 0) return clearSpawnDraft();
	const dir = folder();
	if (dir.exists) dir.delete();
	if (draft.attachments.length) dir.create({ intermediates: true, idempotent: true });
	const attachments = draft.attachments.map((attachment, index): StoredAttachment => {
		const name = fileName(index, attachment.name);
		new File(dir, name).write(attachment.data, { encoding: "base64" });
		return { name: attachment.name, mimeType: attachment.mimeType, bytes: attachment.bytes, file: name, image: Boolean(attachment.previewUri) };
	});
	const stored: StoredDraft = { v: 1, savedAt: now, prompt: draft.prompt, attachments };
	await AsyncStorage.setItem(SPAWN_DRAFT_KEY, JSON.stringify(stored));
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
