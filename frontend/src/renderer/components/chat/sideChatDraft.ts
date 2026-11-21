import type { components } from "../../../api/schema";
import type { ChatDraftAttachment, ChatDraftExcerptReference } from "../../lib/chat-drafts";

export type SideDeliveryReceipt = {
	clientMessageId: string;
	createdAt?: string;
	// Preserve handoff retry ownership if a snapshot arrives before the HTTP reply.
	handoff?: boolean;
	// A rejected frozen opening can be edited safely in a recreated draft.
	recreateRequired?: boolean;
	// Keep a known rejection or uncertain delivery actionable after a remount.
	failure?: { kind: "rejected" | "unconfirmed"; message: string };
	request: components["schemas"]["SendSideQuestionRequest"];
	submitted: {
		text: string;
		attachments: ChatDraftAttachment[];
		references: ChatDraftExcerptReference[];
	};
};

// Serialized only into the daemon's launch-scoped side store and Electron RAM.
export type SideChatDraft = {
	version: 1;
	text: string;
	attachments: ChatDraftAttachment[];
	references: ChatDraftExcerptReference[];
	pendingDelivery?: SideDeliveryReceipt;
	// Keep the accepted opening question available if provider setup needs recreation.
	openingDelivery?: SideDeliveryReceipt;
	// A source-side command owns its destination and frozen request across retries.
	pendingCommand?: {
		creationKey: string;
		destinationId?: string;
		accepted?: boolean;
		cancelRequested?: boolean;
		receipt: SideDeliveryReceipt;
	};
	// Keep the last acknowledged main-composer handoff for retries after remount.
	lastHandoffDelivery?: SideDeliveryReceipt;
};

export function emptySideChatDraft(): SideChatDraft {
	return { version: 1, text: "", attachments: [], references: [] };
}

// Before hydration, local edits contain only what the user has changed so far.
// They must not erase saved files, excerpts, or delivery ownership.
export function mergeSideChatDraft(saved: SideChatDraft, edited?: SideChatDraft): SideChatDraft {
	if (!edited) return saved;
	return {
		...saved, ...edited,
		attachments: [...new Map([...saved.attachments, ...edited.attachments].map(file => [file.path, file])).values()],
		references: [...new Map([...saved.references, ...edited.references].map(ref => [ref.id, ref])).values()],
	};
}

export function parseSideChatDraft(raw: string): SideChatDraft {
	try {
		const value: unknown = JSON.parse(raw);
		if (
			value &&
			typeof value === "object" &&
			"version" in value &&
			value.version === 1 &&
			"text" in value &&
			typeof value.text === "string" &&
			"attachments" in value &&
			Array.isArray(value.attachments) &&
			"references" in value &&
			Array.isArray(value.references)
		) {
			return value as SideChatDraft;
		}
	} catch {
		/* Earlier side drafts were plain text. */
	}
	return { ...emptySideChatDraft(), text: raw };
}
