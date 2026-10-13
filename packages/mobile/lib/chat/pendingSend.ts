import AsyncStorage from "@react-native-async-storage/async-storage";

import type { ChatExcerpt, TurnSettings } from "./types";
import { isChatExcerpt } from "./responseQuotes";

export type PendingSendRecord = {
	id: string;
	draftText: string;
	text: string;
	hasAttachments: boolean;
	excerpts?: ChatExcerpt[];
	/** Absent on records saved by older builds, where every delivery was a send. */
	kind?: "send" | "steer";
	/** A restored Cloud steer must never target a different active turn. */
	targetTurnId?: string;
	settings?: TurnSettings;
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
		(saved.targetTurnId !== undefined && typeof saved.targetTurnId !== "string") ||
		(saved.settings !== undefined && (!saved.settings || typeof saved.settings !== "object" || Object.entries(saved.settings).some(([key, value]) => !["model", "reasoningEffort", "approvalMode"].includes(key) || typeof value !== "string"))) ||
		(saved.excerpts !== undefined && (!Array.isArray(saved.excerpts) || saved.excerpts.length > 8 || !saved.excerpts.every(isChatExcerpt)))) {
		throw new Error("The previous message's retry record is damaged. Discard it before sending another message.");
	}
	return saved;
}

async function reserveUnlocked(key: string, candidate: PendingSendRecord): Promise<PendingSendRecord> {
	const existing = await readPendingSend(key);
	if (existing) {
		if ((existing.kind ?? "send") !== (candidate.kind ?? "send")) {
			throw new Error("Resolve the previous message before sending another one.");
		}
		if (existing.draftText !== candidate.draftText) {
			throw new Error("Resolve the previous message before sending another one.");
		}
		if (candidate.hasAttachments) {
			throw new Error("Resolve the previous message first. Retrying with new attachments could silently discard them.");
		}
		return existing;
	}
	await AsyncStorage.setItem(key, JSON.stringify(candidate));
	return candidate;
}
export async function reservePendingSend(key: string, candidate: PendingSendRecord): Promise<PendingSendRecord> {
	let result!: PendingSendRecord;
	await afterDraftWrites(key, async () => { result = await reserveUnlocked(key, candidate); });
	return result;
}
export function cloudChatDraftKey(scopeKey: string, sessionId: string): string {
	return "ao.cloud.chat-draft.v1." + encodeURIComponent(JSON.stringify([scopeKey, sessionId]));
}

/** Explicit logout retires only the verified Cloud account's chat retry data. */
export async function clearCloudChatForAccount(baseUrl: string, userId: string): Promise<void> {
	const prefixes = ["ao.chat.pending.", "ao.cloud.chat-draft.v1.", "ao.cloud.turn-settings.v1."];
	const keys = new Set([...await AsyncStorage.getAllKeys(), ...draftWrites.keys()]);
	await Promise.all([...keys].filter((key) => {
		const prefix = prefixes.find((prefix) => key.startsWith(prefix));
		if (!prefix) return false;
		try {
			const [scopeKey] = JSON.parse(decodeURIComponent(key.slice(prefix.length)));
			const scope = JSON.parse(scopeKey);
			return Array.isArray(scope) && scope[0] === baseUrl.replace(/\/+$/, "") && scope[1] === userId;
		} catch { return false; }
	}).map((key) => afterDraftWrites(key, () => AsyncStorage.removeItem(key))));
}

export async function clearPendingSend(key: string, id: string): Promise<void> {
	await afterDraftWrites(key, async () => { if ((await readPendingSend(key))?.id === id) await AsyncStorage.removeItem(key); });
}
