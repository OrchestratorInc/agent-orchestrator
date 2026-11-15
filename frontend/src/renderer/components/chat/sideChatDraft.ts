import type { components } from "../../../api/schema";
import type { ChatDraftAttachment, ChatDraftExcerptReference } from "../../lib/chat-drafts";

export type SideDeliveryReceipt = {
	clientMessageId: string;
	request: components["schemas"]["SendSideQuestionRequest"];
	submitted: {
		text: string;
		attachments: ChatDraftAttachment[];
		references: ChatDraftExcerptReference[];
	};
};

export type SideChatDraft = {
	version: 1;
	text: string;
	attachments: ChatDraftAttachment[];
	references: ChatDraftExcerptReference[];
	pendingDelivery?: SideDeliveryReceipt;
	pendingCommand?: {
		creationKey: string;
		destinationId?: string;
		accepted?: boolean;
		cancelRequested?: boolean;
		receipt: SideDeliveryReceipt;
	};
	lastHandoffDelivery?: SideDeliveryReceipt;
};

export function emptySideChatDraft(): SideChatDraft {
	return { version: 1, text: "", attachments: [], references: [] };
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
	}
	return { ...emptySideChatDraft(), text: raw };
}
