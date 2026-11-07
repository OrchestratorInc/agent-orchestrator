import type { ComposerMention } from "./cloud-team-collab";

// A draft is saved as text (chat-drafts); the people and teams its chips
// name are kept beside it here, per draft, so a restored draft (a remount,
// switching between Agent and Aside) shows them as chips again and a send
// still carries them as typed tokens.

const PREFIX = "ao.chatDraftMentions:";

function storage(): Storage | undefined {
	try {
		return typeof window === "undefined" ? undefined : window.localStorage;
	} catch {
		return undefined;
	}
}

function valid(value: unknown): value is ComposerMention {
	if (!value || typeof value !== "object") return false;
	const mention = value as Partial<ComposerMention>;
	return (mention.kind === "person" || mention.kind === "team") && typeof mention.id === "string" && typeof mention.name === "string";
}

export function readDraftMentions(scopeKey: string): ComposerMention[] {
	try {
		const saved = JSON.parse(storage()?.getItem(PREFIX + scopeKey) ?? "[]") as unknown;
		return Array.isArray(saved) ? saved.filter(valid) : [];
	} catch {
		return [];
	}
}

export function writeDraftMentions(scopeKey: string, mentions: readonly ComposerMention[]): void {
	try {
		if (mentions.length === 0) storage()?.removeItem(PREFIX + scopeKey);
		else storage()?.setItem(PREFIX + scopeKey, JSON.stringify(mentions));
	} catch {
		// Without it a restored draft keeps its text; its chips read as text.
	}
}
