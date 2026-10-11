/** Selected snippets remain separate from the editable reply until send. */
export interface ResponseQuote { id: string; text: string }
export interface ResponseQuoteRequest extends ResponseQuote { composerKey: string }

const PREFIX = "ao.quoted-draft.v1:";

export function responseReplyText(text: string, quotes: readonly ResponseQuote[]): string {
	return [...quotes.map((quote) => quote.text.split("\n").map((line) => `> ${line}`).join("\n")), text.trim()].filter(Boolean).join("\n\n");
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
