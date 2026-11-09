import { describe, expect, it } from "vitest";
import { OPEN_SESSION_DEEP_LINK_CHANNEL, parseSessionDeepLink } from "./deep-links";

describe("parseSessionDeepLink", () => {
	it("parses a valid session link", () => {
		expect(parseSessionDeepLink("ao-app://sessions/proj-1/sess-9")).toEqual({
			projectId: "proj-1",
			sessionId: "sess-9",
		});
	});

	it("percent-decodes both segments", () => {
		expect(parseSessionDeepLink("ao-app://sessions/my%2Fproject/sess%20ion")).toEqual({
			projectId: "my/project",
			sessionId: "sess ion",
		});
	});

	it("accepts a trailing slash", () => {
		expect(parseSessionDeepLink("ao-app://sessions/proj-1/sess-9/")).toEqual({
			projectId: "proj-1",
			sessionId: "sess-9",
		});
	});

	it("rejects billing links (they keep their own handling)", () => {
		expect(parseSessionDeepLink("ao-app://billing")).toBeNull();
	});

	it("rejects callback links (they keep their own handling)", () => {
		expect(parseSessionDeepLink("ao-app://callback?code=abc&state=def")).toBeNull();
	});

	it("rejects unknown hosts", () => {
		expect(parseSessionDeepLink("ao-app://settings/proj-1/sess-9")).toBeNull();
	});

	it("rejects non ao-app protocols", () => {
		expect(parseSessionDeepLink("https://sessions/proj-1/sess-9")).toBeNull();
	});

	it("rejects malformed URLs", () => {
		expect(parseSessionDeepLink("not a url")).toBeNull();
		expect(parseSessionDeepLink("")).toBeNull();
	});

	it("rejects links without exactly two path segments", () => {
		expect(parseSessionDeepLink("ao-app://sessions")).toBeNull();
		expect(parseSessionDeepLink("ao-app://sessions/")).toBeNull();
		expect(parseSessionDeepLink("ao-app://sessions/proj-1")).toBeNull();
		expect(parseSessionDeepLink("ao-app://sessions//sess-9")).toBeNull();
		expect(parseSessionDeepLink("ao-app://sessions/proj-1/sess-9/extra")).toBeNull();
	});

	it("rejects segments that decode to empty", () => {
		expect(parseSessionDeepLink("ao-app://sessions/proj-1/%20")).toBeNull();
		expect(parseSessionDeepLink("ao-app://sessions/%20/sess-9")).toBeNull();
	});

	it("rejects invalid percent encoding", () => {
		expect(parseSessionDeepLink("ao-app://sessions/%ZZ/sess-9")).toBeNull();
	});

	it("exposes the dedicated IPC channel", () => {
		expect(OPEN_SESSION_DEEP_LINK_CHANNEL).toBe("app:open-session-deep-link");
	});
});
