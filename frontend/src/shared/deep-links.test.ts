import { describe, expect, it } from "vitest";
import { OPEN_SESSION_DEEP_LINK_CHANNEL, isSessionDeepLink, normalizeSessionDeepLink } from "./deep-links";

describe("isSessionDeepLink", () => {
	it("accepts a valid session link", () => {
		expect(isSessionDeepLink("ao-app://sessions/proj-1/sess-9")).toBe(true);
	});

	it("accepts an artifact route (the shell resolves the file)", () => {
		expect(isSessionDeepLink("ao-app://sessions/proj-1/sess-9/artifacts/report%20one.md")).toBe(true);
	});

	it("accepts a trailing slash", () => {
		expect(isSessionDeepLink("ao-app://sessions/proj-1/sess-9/")).toBe(true);
	});

	it("accepts an uppercase scheme (URL parsing lowercases it; main forwards the normalized form)", () => {
		expect(isSessionDeepLink("AO-APP://sessions/proj-1/sess-9")).toBe(true);
	});

	it("rejects billing links (they keep their own handling)", () => {
		expect(isSessionDeepLink("ao-app://billing")).toBe(false);
	});

	it("rejects callback links (they keep their own handling)", () => {
		expect(isSessionDeepLink("ao-app://callback?code=abc&state=def")).toBe(false);
	});

	it("rejects unknown hosts", () => {
		expect(isSessionDeepLink("ao-app://settings/proj-1/sess-9")).toBe(false);
	});

	it("rejects non ao-app protocols", () => {
		expect(isSessionDeepLink("https://sessions/proj-1/sess-9")).toBe(false);
	});

	it("rejects malformed URLs", () => {
		expect(isSessionDeepLink("not a url")).toBe(false);
		expect(isSessionDeepLink("")).toBe(false);
	});

	it("rejects links without a target path", () => {
		expect(isSessionDeepLink("ao-app://sessions")).toBe(false);
		expect(isSessionDeepLink("ao-app://sessions/")).toBe(false);
		expect(isSessionDeepLink("ao-app://sessions/proj-1")).toBe(false);
		expect(isSessionDeepLink("ao-app://sessions//sess-9")).toBe(false);
	});

	it("exposes the dedicated IPC channel", () => {
		expect(OPEN_SESSION_DEEP_LINK_CHANNEL).toBe("app:open-session-deep-link");
	});
});

describe("normalizeSessionDeepLink", () => {
	it("passes a plain link through unchanged", () => {
		expect(normalizeSessionDeepLink("ao-app://sessions/proj-1/sess-9")).toBe("ao-app://sessions/proj-1/sess-9");
	});

	it("lowercases the scheme", () => {
		expect(normalizeSessionDeepLink("AO-APP://sessions/proj-1/sess-9")).toBe("ao-app://sessions/proj-1/sess-9");
	});

	it("strips a trailing slash", () => {
		expect(normalizeSessionDeepLink("ao-app://sessions/demo/demo-1/")).toBe("ao-app://sessions/demo/demo-1");
	});

	it("strips query and hash", () => {
		expect(normalizeSessionDeepLink("ao-app://sessions/demo/demo-1?src=dashboard")).toBe("ao-app://sessions/demo/demo-1");
		expect(normalizeSessionDeepLink("ao-app://sessions/demo/demo-1#frag")).toBe("ao-app://sessions/demo/demo-1");
	});

	it("keeps the artifact route (without trailing slash or query)", () => {
		expect(normalizeSessionDeepLink("ao-app://sessions/p/s/artifacts/report%20one.md")).toBe(
			"ao-app://sessions/p/s/artifacts/report%20one.md",
		);
	});

	it("returns null for non-links", () => {
		expect(normalizeSessionDeepLink("ao-app://billing")).toBeNull();
		expect(normalizeSessionDeepLink("not a url")).toBeNull();
	});
});
