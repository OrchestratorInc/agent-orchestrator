import AsyncStorage from "@react-native-async-storage/async-storage";

import type { ChatExcerpt } from "./types";
import type { ProviderSwitchInput } from "./api";
import { excerptDraftText, isChatExcerpt } from "./responseQuotes";

export type PendingSendRecord = {
	id: string;
	draftText: string;
	/**
	 * The draft as the composer held it, before anything was added to what was
	 * sent (Ultracode's prefix): what a retry's completion matches the composer
	 * against, exactly. Absent on older records and where nothing was added,
	 * and `draftText` is it.
	 */
	typedDraft?: string;
	text: string;
	hasAttachments: boolean;
	excerpts?: ChatExcerpt[];
	/** Absent on records saved by older builds, where every delivery was a send. */
	kind?: "send" | "steer";
	/** A send that also switches the chat's agent (desktop's pending switch). */
	providerSwitch?: ProviderSwitchInput;
};

export function pendingSendKey(host: string, conversation: string): string {
	return `ao.chat.pending.${encodeURIComponent(JSON.stringify([host, conversation]))}`;
}

export function needsAttachmentRecovery(restored: boolean, hasAttachments: boolean, payloadAvailable: boolean): boolean {
	return restored && hasAttachments && !payloadAvailable;
}

const draftWrites = new Map<string, Promise<void>>();

export function afterDraftWrites(key: string, write: () => Promise<void>): Promise<void> {
	const next = (draftWrites.get(key) ?? Promise.resolve()).catch(() => {}).then(write);
	draftWrites.set(key, next);
	void next.finally(() => { if (draftWrites.get(key) === next) draftWrites.delete(key); }).catch(() => {});
	return next;
}

export async function readPendingSend(key: string): Promise<PendingSendRecord | null> {
	const raw = await AsyncStorage.getItem(key);
	if (!raw) return null;
	const saved = JSON.parse(raw) as PendingSendRecord;
	if (!saved.id || typeof saved.draftText !== "string" || typeof saved.text !== "string" || typeof saved.hasAttachments !== "boolean" ||
		(saved.kind !== undefined && saved.kind !== "send" && saved.kind !== "steer") ||
		(saved.typedDraft !== undefined && typeof saved.typedDraft !== "string") ||
		(saved.excerpts !== undefined && (!Array.isArray(saved.excerpts) || saved.excerpts.length > 8 || !saved.excerpts.every(isChatExcerpt))) ||
		(saved.providerSwitch !== undefined && (typeof saved.providerSwitch?.targetHarness !== "string" ||
			(saved.providerSwitch.model !== undefined && typeof saved.providerSwitch.model !== "string")))) {
		throw new Error("The previous message's retry record is damaged. Discard it before sending another message.");
	}
	return saved;
}

/**
 * A send's draft identities: what was sent (`draftText`, which a new send of
 * the same draft must match to be a retry), and, where the composer's draft
 * differs from it, the draft as typed (`typedDraft`).
 */
export function pendingDraft(sent: string, typed: string | undefined, excerpts?: readonly ChatExcerpt[]): Pick<PendingSendRecord, "draftText" | "typedDraft"> {
	const draftText = excerptDraftText(sent, excerpts);
	return typed === undefined || typed === sent ? { draftText } : { draftText, typedDraft: excerptDraftText(typed, excerpts) };
}

/** The draft a retried send was typed as, for the composer to match exactly. */
export function retriedDraft(record: Pick<PendingSendRecord, "draftText" | "typedDraft">): string {
	return record.typedDraft ?? record.draftText;
}

export async function reservePendingSend(key: string, candidate: PendingSendRecord): Promise<PendingSendRecord> {
	const existing = await readPendingSend(key);
	if (existing) {
		if ((existing.kind ?? "send") !== (candidate.kind ?? "send")) {
			throw new Error("Resolve the previous message before sending another one.");
		}
		if (existing.draftText !== candidate.draftText) {
			throw new Error("Resolve the previous message before sending another one.");
		}
		// The same text sent to another agent or model is a different request:
		// reusing the record would deliver the earlier switch while the composer
		// shows the new pick. Retry keeps the original; a new send must not.
		if (switchTarget(existing.providerSwitch) !== switchTarget(candidate.providerSwitch)) {
			throw new Error("Resolve the previous message first. It was sent to a different agent or model.");
		}
		if (candidate.hasAttachments) {
			throw new Error("Resolve the previous message first. Retrying with new attachments could silently discard them.");
		}
		return existing;
	}
	// The record lets a send cut short by the app closing be retried under the
	// same id after it reopens. A phone whose storage is full or failing cannot
	// keep it, and that must not stop the message: it still goes, and this
	// session's retry (which keeps the send in memory) still works. Only the
	// retry after a relaunch is lost.
	try {
		await AsyncStorage.setItem(key, JSON.stringify(candidate));
	} catch { /* sent without the relaunch record */ }
	return candidate;
}

/** A switch's target and model as one comparable value; none for a plain send. */
function switchTarget(providerSwitch: ProviderSwitchInput | undefined): string {
	return providerSwitch ? `${providerSwitch.targetHarness}\u0000${providerSwitch.model ?? ""}` : "";
}

export async function clearPendingSend(key: string, id: string): Promise<void> {
	if ((await readPendingSend(key))?.id === id) await AsyncStorage.removeItem(key);
}
