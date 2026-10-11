import { describe, expect, it } from "vitest";
import { decodeResponseDraft, encodeResponseDraft, responseReplyText } from "./responseQuotes";

describe("selected response quotes", () => {
	const quotes = [{ id: "first", text: "Heading 🧑🏽‍💻\n\n• First\n• Second\n  code();  " }];
	it("preserves the selected Unicode, bullets, whitespace and blank lines in the sent quote", () => {
		expect(responseReplyText("  Explain this  ", quotes)).toBe("> Heading 🧑🏽‍💻\n> \n> • First\n> • Second\n>   code();  \n\nExplain this");
	});
	it("allows a quote-only reply and multiple snippets in selection order", () => {
		expect(responseReplyText("", [...quotes, { id: "second", text: "Another excerpt" }])).toBe(responseReplyText("", quotes) + "\n\n> Another excerpt");
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
