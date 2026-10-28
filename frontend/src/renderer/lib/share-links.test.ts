import { describe, expect, it } from "vitest";
import { parseShareLink, shareLinkUrl } from "./share-links";

const ORG = "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b";

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

	it("rejects anything that does not name one share", () => {
		for (const text of [
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
