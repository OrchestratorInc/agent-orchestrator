import { describe, expect, it } from "vitest";
import { canOpenInUserBrowser, classifySiteResponse } from "./browser-site-block";

describe("classifySiteResponse", () => {
	it("recognises a Cloudflare challenge in any header casing", () => {
		expect(classifySiteResponse({ url: "https://dash.cloudflare.com/login", statusCode: 403, headers: { "CF-Mitigated": ["challenge"] } })).toEqual({
			kind: "challenge",
			host: "dash.cloudflare.com",
		});
	});

	it("treats any other Cloudflare mitigation as a block", () => {
		expect(classifySiteResponse({ url: "https://x.test/", statusCode: 403, headers: { "cf-mitigated": "block" } })).toEqual({
			kind: "blocked",
			block: { reason: "blocked", host: "x.test" },
		});
	});

	it("recognises DataDome and Akamai refusals only when they refuse", () => {
		expect(classifySiteResponse({ url: "https://shop.test/", statusCode: 403, headers: { "x-datadome": ["protected"] } })).toMatchObject({ kind: "blocked" });
		expect(classifySiteResponse({ url: "https://shop.test/", statusCode: 403, headers: { "X-DD-B": ["1"] } })).toMatchObject({ kind: "blocked" });
		// DataDome tags normal pages too.
		expect(classifySiteResponse({ url: "https://shop.test/", statusCode: 200, headers: { "x-datadome": ["protected"] } })).toEqual({ kind: "page", host: "shop.test" });
		expect(classifySiteResponse({ url: "https://bank.test/", statusCode: 403, headers: { Server: ["AkamaiGHost"] } })).toMatchObject({ kind: "blocked" });
		expect(classifySiteResponse({ url: "https://bank.test/", statusCode: 200, headers: { Server: ["AkamaiGHost"] } })).toEqual({ kind: "page", host: "bank.test" });
	});

	it("recognises Google refusing an embedded browser, and nothing else at Google", () => {
		expect(classifySiteResponse({ url: "https://accounts.google.com/o/oauth2/v2/auth?error=disallowed_useragent", statusCode: 200 })).toEqual({
			kind: "blocked",
			block: { reason: "signin", host: "accounts.google.com" },
		});
		expect(classifySiteResponse({ url: "https://accounts.google.com/signin/oauth", statusCode: 403 })).toMatchObject({ block: { reason: "signin" } });
		expect(classifySiteResponse({ url: "https://accounts.google.com/v3/signin/identifier", statusCode: 200 })).toEqual({ kind: "page", host: "accounts.google.com" });
		expect(classifySiteResponse({ url: "https://evil.test/?q=disallowed_useragent", statusCode: 200 })).toEqual({ kind: "page", host: "evil.test" });
	});

	it("leaves ordinary errors and non-web pages alone", () => {
		expect(classifySiteResponse({ url: "https://site.test/private", statusCode: 403 })).toEqual({ kind: "page", host: "site.test" });
		expect(classifySiteResponse({ url: "https://site.test/", statusCode: 429 })).toEqual({ kind: "page", host: "site.test" });
		expect(classifySiteResponse({ url: "file:///etc/hosts", statusCode: 200 })).toBeUndefined();
		expect(classifySiteResponse({ url: "not a url", statusCode: 200 })).toBeUndefined();
	});
});

describe("canOpenInUserBrowser", () => {
	it("hands over ordinary web pages only", () => {
		expect(canOpenInUserBrowser("https://example.com/a?b=1")).toBe(true);
		expect(canOpenInUserBrowser("http://localhost:3000/")).toBe(true);
		expect(canOpenInUserBrowser(`https://ao-preview-${"a".repeat(32)}.localhost:4123/`)).toBe(false);
		expect(canOpenInUserBrowser("about:blank")).toBe(false);
		expect(canOpenInUserBrowser("javascript:alert(1)")).toBe(false);
		expect(canOpenInUserBrowser("")).toBe(false);
	});
});
