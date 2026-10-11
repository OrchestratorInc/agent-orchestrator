import type { ChatExcerpt } from "./types";

/** Selected snippets retain their durable transcript provenance until send. */
export interface ResponseQuote { id: string; text: string; source?: Omit<ChatExcerpt, "text"> }
export interface ResponseQuoteRequest extends ResponseQuote { composerKey: string }

const PREFIX = "ao.quoted-draft.v1:";

export function isChatExcerpt(value: unknown): value is ChatExcerpt {
	if (!value || typeof value !== "object") return false;
	const item = value as ChatExcerpt;
	return typeof item.conversationId === "string" && Boolean(item.conversationId) && typeof item.messageId === "string" && Boolean(item.messageId) && Number.isSafeInteger(item.revision) && item.revision >= 0 && typeof item.text === "string" && Boolean(item.text.trim());
}

export function responseReplyText(text: string, quotes: readonly ResponseQuote[]): string {
	return text.trim() || (quotes.length ? `Use the attached ${quotes.length} chat excerpt(s) as context` : "");
}

export function responseExcerpts(quotes: readonly ResponseQuote[]): ChatExcerpt[] {
	if (quotes.length > 8) throw new Error("Attach at most 8 selections to one message.");
	return quotes.map((quote) => {
		if (!isChatExcerpt({ ...quote.source, text: quote.text })) throw new Error("Reselect this older quote before sending so its source can be verified.");
		return { ...quote.source!, text: quote.text };
	});
}

/** Keep retries tied to both the reply and the exact selections, like desktop. */
export function excerptDraftText(text: string, excerpts?: readonly ChatExcerpt[]): string {
	return excerpts?.length ? JSON.stringify({ text, excerpts }) : text;
}

export function encodeResponseDraft(text: string, quotes: readonly ResponseQuote[]): string {
	return quotes.length ? PREFIX + JSON.stringify({ text, quotes }) : text;
}

export function decodeResponseDraft(value: string | null): { text: string; quotes: ResponseQuote[] } {
	if (value?.startsWith(PREFIX)) {
		try {
			const draft = JSON.parse(value.slice(PREFIX.length));
			if (typeof draft.text === "string" && Array.isArray(draft.quotes) && draft.quotes.every((quote: unknown) => typeof quote === "object" && quote !== null && "id" in quote && typeof quote.id === "string" && "text" in quote && typeof quote.text === "string")) return draft;
		} catch { /* Keep an unrecognized draft readable. */ }
	}
	return { text: value ?? "", quotes: [] };
}
