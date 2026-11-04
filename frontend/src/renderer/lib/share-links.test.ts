import { describe, expect, it } from "vitest";
import { isShareCode, parseShareLink, shareCodeLinkUrl, shareLinkUrl } from "./share-links";

const ORG = "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b";
// Share codes are exactly 22 letters or digits.
const CODE = "k7Hq2x9PmB4wR5tY8uI0oP";
const SHORT_CODE = "k7Hq2x9PmB4w";
const LONG_CODE = `${CODE}Z`;

describe("share links", () => {
	it("shares a page on the control plane, with the share in the fragment", () => {
		const link = shareLinkUrl("https://cp.example.test/", ORG, "tok_abc-123");
		expect(link).toBe(`https://cp.example.test/app/share#${ORG}/tok_abc-123`);
		expect(new URL(link).pathname).toBe("/app/share");
		expect(parseShareLink(link)).toEqual({ orgId: ORG, token: "tok_abc-123" });
	});

	it("opens the desktop deep link the page hands over", () => {
		expect(parseShareLink(`ao-app://share/${ORG}/tok_abc-123`)).toEqual({ orgId: ORG, token: "tok_abc-123" });
	});

	it("opens the control plane's web link of the same share", () => {
		expect(parseShareLink(`https://api.example.test/share/${ORG}/tok`)).toEqual({ orgId: ORG, token: "tok" });
		expect(parseShareLink(`  http://127.0.0.1:8081/share/${ORG}/tok  `)).toEqual({ orgId: ORG, token: "tok" });
	});

	it("reads a link from before codes exactly as pasted", () => {
		expect(parseShareLink(`ao-app://share/${ORG}/tok.`)).toEqual({ orgId: ORG, token: "tok." });
		expect(parseShareLink(`https://cp.example.test/app/share#${ORG}/tok_abc-123),`)).toEqual({ orgId: ORG, token: "tok_abc-123)," });
		expect(parseShareLink(`https://cp.example.test/share/${ORG}/tok/`)).toEqual({ orgId: ORG, token: "tok" });
	});

	it("knows a share code by its 22 letters or digits", () => {
		expect(isShareCode(CODE)).toBe(true);
		for (const value of [SHORT_CODE, LONG_CODE, `${CODE.slice(0, 21)}-`, `${CODE.slice(0, 21)} `, "", undefined, null, 22]) {
			expect(isShareCode(value), String(value)).toBe(false);
		}
	});

	it("shares a short link with one code, and opens it from the web or the deep link", () => {
		const link = shareCodeLinkUrl("https://cp.example.test/", CODE);
		expect(link).toBe(`https://cp.example.test/s/${CODE}`);
		expect(parseShareLink(link)).toEqual({ code: CODE });
		expect(parseShareLink(`ao-app://share/${CODE}`)).toEqual({ code: CODE });
		expect(parseShareLink(`ao-app://share/${CODE}/`)).toEqual({ code: CODE });
		expect(parseShareLink(`https://api.aoagents.dev/s/${CODE}/`)).toEqual({ code: CODE });
		expect(parseShareLink(`  https://api.aoagents.dev/s/${CODE}/  `)).toEqual({ code: CODE });
		expect(parseShareLink(`\n\thttps://api.aoagents.dev/s/${CODE}\n`)).toEqual({ code: CODE });
		expect(parseShareLink(`https://api.aoagents.dev/s/${CODE}?utm_source=slack`)).toEqual({ code: CODE });
	});

	it("opens a short link pasted from a chat with punctuation after it", () => {
		for (const text of [
			`https://api.aoagents.dev/s/${CODE}.`,
			`https://api.aoagents.dev/s/${CODE},`,
			`https://api.aoagents.dev/s/${CODE}!`,
			`https://api.aoagents.dev/s/${CODE}?`,
			`https://api.aoagents.dev/s/${CODE}).`,
			`https://api.aoagents.dev/s/${CODE}/).`,
			`https://api.aoagents.dev/s/${CODE}"`,
			`https://api.aoagents.dev/s/${CODE}”`,
			`https://api.aoagents.dev/s/${CODE}…`,
			`https://api.aoagents.dev/s/${CODE}​`,
			`  https://api.aoagents.dev/s/${CODE}/ .  `,
			`ao-app://share/${CODE}.`,
		]) {
			expect(parseShareLink(text), text).toEqual({ code: CODE });
		}
	});

	it("opens only 22-character codes", () => {
		for (const code of [SHORT_CODE, LONG_CODE, CODE.slice(0, 21)]) {
			for (const text of [
				`ao-app://share/${code}`,
				`https://api.aoagents.dev/s/${code}`,
				`https://api.aoagents.dev/s/${code}/`,
				`  https://api.aoagents.dev/s/${code}.  `,
				`ao-app://share/${code}!`,
			]) {
				expect(parseShareLink(text), text).toBeUndefined();
			}
		}
	});

	it("rejects anything that does not name one share", () => {
		for (const text of [
			"ao-app://share/short",
			`ao-app://share/${CODE.slice(0, 11)}-${CODE.slice(12)}`,
			`https://cp.example.test/s/${CODE.slice(0, 11)}-${CODE.slice(12)}`,
			`https://cp.example.test/s/${CODE}/extra`,
			`https://cp.example.test/s/${CODE}/extra.`,
			`https://cp.example.test/s/${CODE}x.`,
			`https://cp.example.test/x/${CODE}`,
			`https://cp.example.test/share/${CODE}`,
			`https://cp.example.test/app/share#${CODE}`,
			`ao-app://billing/${CODE}`,
			"https://cp.example.test/s",
			"",
			"not a link",
			`ao-app://billing/${ORG}/tok`,
			`ao-app://share/${ORG}`,
			`ao-app://share/${ORG}/tok/extra`,
			"ao-app://share/not-an-org/tok",
			`https://example.test/sessions/${ORG}/tok`,
			`ftp://example.test/share/${ORG}/tok`,
			"https://cp.example.test/app/share",
			`https://cp.example.test/app/share#${ORG}`,
			"https://cp.example.test/app/share#not-an-org/tok",
			`https://cp.example.test/app/other#${ORG}/tok`,
		]) {
			expect(parseShareLink(text), text).toBeUndefined();
		}
	});
});
