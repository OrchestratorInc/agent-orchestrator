import { describe, expect, it } from "vitest";
import { DEFAULT_SITE_POLICY, evaluateUrl, hostMatches, parseHostList, parsePolicy, trustedInputAllowed } from "./policy";

describe("site policy", () => {
	it("matches hosts and subdomains, not lookalikes", () => {
		expect(hostMatches("paypal.com", "paypal.com")).toBe(true);
		expect(hostMatches("www.paypal.com", "paypal.com")).toBe(true);
		expect(hostMatches("www.paypal.com", "*.paypal.com")).toBe(true);
		expect(hostMatches("notpaypal.com", "paypal.com")).toBe(false);
		expect(hostMatches("anything.test", "*")).toBe(true);
		expect(hostMatches("x.test", " ")).toBe(false);
	});

	it("blocks denied hosts and non-web pages", () => {
		expect(evaluateUrl(DEFAULT_SITE_POLICY, "https://www.paypal.com/checkout")).toMatchObject({ allowed: false });
		expect(evaluateUrl(DEFAULT_SITE_POLICY, "https://accounts.google.com/signin")).toMatchObject({ allowed: false });
		expect(evaluateUrl(DEFAULT_SITE_POLICY, "chrome://settings")).toMatchObject({ allowed: false });
		expect(evaluateUrl(DEFAULT_SITE_POLICY, "file:///etc/passwd")).toMatchObject({ allowed: false });
		expect(evaluateUrl(DEFAULT_SITE_POLICY, "https://example.com/")).toEqual({ allowed: true });
		expect(evaluateUrl(DEFAULT_SITE_POLICY, "about:blank")).toEqual({ allowed: true });
	});

	it("enforces a non-empty allow list", () => {
		const policy = { ...DEFAULT_SITE_POLICY, allow: ["example.com"] };
		expect(evaluateUrl(policy, "https://docs.example.com/")).toEqual({ allowed: true });
		expect(evaluateUrl(policy, "https://other.test/")).toMatchObject({ allowed: false });
	});

	it("chooses the input tier per site", () => {
		const policy = { ...DEFAULT_SITE_POLICY, syntheticInput: ["bank.test"] };
		expect(trustedInputAllowed(policy, "https://shop.test/")).toBe(true);
		expect(trustedInputAllowed(policy, "https://www.bank.test/")).toBe(false);
		expect(trustedInputAllowed({ ...policy, trustedInput: false }, "https://shop.test/")).toBe(false);
	});

	it("parses stored policies defensively", () => {
		expect(parsePolicy(undefined)).toEqual(DEFAULT_SITE_POLICY);
		expect(parsePolicy({ deny: ["a.test", 3, ""], allow: "x", trustedInput: false })).toEqual({
			deny: ["a.test"],
			allow: [],
			syntheticInput: [],
			trustedInput: false,
		});
		expect(parseHostList(" A.test\nb.test, c.test \n\n")).toEqual(["a.test", "b.test", "c.test"]);
	});
});
