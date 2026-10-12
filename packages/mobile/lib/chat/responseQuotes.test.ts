import { describe, expect, it } from "vitest";
import { decodeResponseDraft, encodeResponseDraft, responseReplyText, responseExcerpts, excerptDraftText } from "./responseQuotes";

describe("selected response quotes", () => {
	const source = { conversationId: "conversation", messageId: "response", revision: 1 };
	const quotes = [{ id: "first", text: "Heading 🧑🏽‍💻\n\n• First\n• Second\n  code();  ", source }];
	it("preserves the selected Unicode, bullets, whitespace and blank lines in the sent quote", () => {
		expect(responseReplyText("  Explain this  ", quotes)).toBe("Explain this");
		expect(responseExcerpts(quotes)).toEqual([{ ...source, text: quotes[0].text }]);
	});
	it("allows a quote-only reply and multiple snippets in selection order", () => {
		expect(responseReplyText("", quotes)).toBe("Use the attached 1 chat excerpt(s) as context");
		expect(responseExcerpts([...quotes, { id: "second", text: "Another excerpt", source }])).toHaveLength(2);
	});
	it("requires reselecting legacy quotes rather than silently sending unverified context", () => {
		expect(() => responseExcerpts([{ id: "old", text: "Legacy selection" }])).toThrow("Reselect");
	});
	it("includes exact source and text in the retry identity", () => {
		expect(excerptDraftText("Explain", responseExcerpts(quotes))).not.toBe(excerptDraftText("Explain", [{ ...source, text: "Other selection" }]));
		expect(excerptDraftText("Explain")).toBe("Explain");
	});
	it("restores quotes and an unchanged editable draft after navigation", () => {
		expect(decodeResponseDraft(encodeResponseDraft(" unfinished reply ", quotes))).toEqual({ text: " unfinished reply ", quotes });
	});
	it("removing the last quote leaves the existing text draft and ordinary send unchanged", () => {
		expect(encodeResponseDraft(" unfinished reply ", [])).toBe(" unfinished reply ");
		expect(responseReplyText(" unfinished reply ", [])).toBe("unfinished reply");
	});
	it.each([null, "", "plain draft", '{"text":"ordinary pasted JSON"}', 'ao.quoted-draft.v1:broken', 'ao.quoted-draft.v1:{"text":"x","quotes":[null]}'])("keeps legacy and unrecognized drafts readable: %s", (value) => {
		expect(decodeResponseDraft(value)).toEqual({ text: value ?? "", quotes: [] });
	});
});
