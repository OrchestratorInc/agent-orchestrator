import { describe, expect, it } from "vitest";
import { classifyRoute, decodeResumeRoute, encodeResumeRoute, restoreAction, RESUME_MAX_AGE_MS } from "./updateResume";

describe("classifyRoute", () => {
	it("keeps a session page with its machine, without repeating the id in the query", () => {
		expect(classifyRoute("/session/abc", ["session", "[id]"], { id: "abc", hostId: "h_1" })).toEqual({
			kind: "page",
			href: "/session/abc?hostId=h_1",
		});
	});

	it("keeps plain pages, including Settings", () => {
		expect(classifyRoute("/settings", ["settings"], {})).toEqual({ kind: "page", href: "/settings" });
		expect(classifyRoute("/notifications", ["notifications"], {})).toEqual({ kind: "page", href: "/notifications" });
	});

	it("encodes query values and repeats array params", () => {
		expect(classifyRoute("/review/r1", ["review", "[id]"], { id: "r1", tag: ["a b", "c"] })).toEqual({
			kind: "page",
			href: "/review/r1?tag=a%20b&tag=c",
		});
	});

	it("treats the board as nothing to reopen", () => {
		expect(classifyRoute("/", ["(tabs)", "(workers)"], {})).toEqual({ kind: "root" });
	});

	it("leaves sheets, onboarding and pairing to the page underneath", () => {
		expect(classifyRoute("/sheets/model", ["sheets", "model"], {})).toEqual({ kind: "transient" });
		expect(classifyRoute("/onboarding", ["onboarding"], {})).toEqual({ kind: "transient" });
		expect(classifyRoute("/pair", ["pair"], {})).toEqual({ kind: "transient" });
	});
});

describe("resume record", () => {
	const now = 1_000_000;

	it("round-trips a fresh page", () => {
		expect(decodeResumeRoute(encodeResumeRoute("/session/abc?hostId=h_1", now), now + 3_000)).toBe(
			"/session/abc?hostId=h_1",
		);
	});

	it("ignores a page left over from a restart long ago", () => {
		expect(decodeResumeRoute(encodeResumeRoute("/settings", now), now + RESUME_MAX_AGE_MS + 1)).toBeNull();
	});

	it("ignores missing, malformed, future-dated or non-path records", () => {
		expect(decodeResumeRoute(null, now)).toBeNull();
		expect(decodeResumeRoute("not json", now)).toBeNull();
		expect(decodeResumeRoute(JSON.stringify({ href: "/settings" }), now)).toBeNull();
		expect(decodeResumeRoute(encodeResumeRoute("/settings", now + 10), now)).toBeNull();
		expect(decodeResumeRoute(encodeResumeRoute("https://example.com", now), now)).toBeNull();
	});
});

describe("restoreAction", () => {
	it("pushes the page over the board", () => {
		expect(restoreAction("/", "/", "/session/abc?hostId=h_1")).toBe("push");
	});

	it("replaces a stale deep-linked page the reload came back on", () => {
		expect(restoreAction("/session/old", "/session/old", "/session/abc?hostId=h_1")).toBe("replace");
	});

	it("does nothing when the reload already landed on the page", () => {
		expect(restoreAction("/settings/theme", "/settings/theme", "/settings/theme")).toBe("none");
		expect(restoreAction("/session/abc", "/session/abc", "/session/abc?hostId=h_1")).toBe("none");
	});

	it("never takes the user off a page they moved to after the reload", () => {
		expect(restoreAction("/settings", "/", "/session/abc?hostId=h_1")).toBe("drop");
		expect(restoreAction("/session/other", "/session/old", "/session/abc?hostId=h_1")).toBe("drop");
	});
});
