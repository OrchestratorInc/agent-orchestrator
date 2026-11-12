import { describe, expect, it } from "vitest";
import { OPEN_SESSION_DEEP_LINK_CHANNEL, isSessionDeepLink } from "./deep-links";

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
