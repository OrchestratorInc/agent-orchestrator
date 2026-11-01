import type { ComposerPickerCatalog } from "./composerSuggestions";
import type { ConversationMenuAction, ConversationMenuGroup } from "./conversationMenuModel";
import type { ConversationMarker } from "./timelineModel";
import type { ConversationSnapshot } from "./types";

/** The conversation's context window, token counts and account limits, kept current while the sheet is up. */
export type ConversationUsageEntry = {
	kind: "conversation-usage";
	sessionTitle: string;
	snapshot: ConversationSnapshot;
	/** Calls back at once with the latest snapshot, then on every change. */
	subscribeSnapshot(listener: (snapshot: ConversationSnapshot) => void): () => void;
};

/**
 * The composer's draft opened full screen. The editor writes every change back
 * through `onChange`, so the draft is the composer's whichever way the editor
 * closes; `onSend` asks the composer to send it once the editor has gone.
 */
export type ComposerEditorEntry = {
	kind: "composer-editor";
	text: string;
	placeholder: string;
	/** "Send", or "Queue" while a turn runs. */
	sendLabel: string;
	onChange(text: string): void;
	onSend(): void;
};

/** The conversation's actions page, kept current while it is up: a running action says so, and one that can run again comes back. */
export type ConversationActionsEntry = {
	kind: "conversation-actions";
	title: string;
	groups: readonly ConversationMenuGroup[];
	/** Calls back at once with the latest groups, then on every change. */
	subscribeGroups(listener: (groups: readonly ConversationMenuGroup[]) => void): () => void;
	onSelect(action: ConversationMenuAction): void;
};

export type ChatSheetEntry =
	| { kind: "conversation-map"; markers: ConversationMarker[]; onSelect(sequence: number): void }
	| ConversationActionsEntry
	| { kind: "composer-picker"; catalog: ComposerPickerCatalog; initialQuery?: string; truncated?: boolean; onSelect(value: string): void }
	| ConversationUsageEntry
	| ComposerEditorEntry;

const entries = new Map<string, ChatSheetEntry>();
let sequence = 0;

export function parkChatSheet<T extends ChatSheetEntry>(entry: T): string {
	const key = `chat-sheet-${++sequence}`;
	entries.set(key, entry);
	return key;
}

export function readChatSheet(key?: string): ChatSheetEntry | undefined { return key ? entries.get(key) : undefined; }
export function releaseChatSheet(key?: string): void { if (key) entries.delete(key); }
export function resetChatSheets(): void { entries.clear(); sequence = 0; }

export function chatSheetRoute(entry: ChatSheetEntry) {
	const sheetKey = parkChatSheet(entry);
	const pathname = {
		"conversation-map": "/sheets/conversation-map",
		"conversation-usage": "/sheets/conversation-usage",
		"composer-picker": "/sheets/composer-picker",
		"composer-editor": "/sheets/composer-editor",
		// A pushed page, not a sheet; it shares the hand-off all the same.
		"conversation-actions": "/conversation-actions",
	}[entry.kind];
	return { pathname, params: { sheetKey } } as const;
}
