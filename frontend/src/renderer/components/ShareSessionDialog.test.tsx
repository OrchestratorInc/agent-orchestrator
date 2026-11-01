import { describe, expect, it } from "vitest";
import { appI18n } from "../i18n";
import { createdShareLinkUrl, shareLinkLabel, shareLinkRequest } from "./ShareSessionDialog";

const t = appI18n.t.bind(appI18n);

describe("share links", () => {
	it("shares one session, or the whole project without naming a session", () => {
		expect(shareLinkRequest({ scope: "session", access: "editor", modeCap: "trusted", expiry: "24", sessionId: "s1" })).toEqual({
			sessionId: "s1", role: "editor", accessScope: "anyone", interaction: "interact", modeCap: "trusted", expiresInHours: 24,
		});
		expect(shareLinkRequest({ scope: "project", access: "editor", modeCap: "standard", expiry: "never", sessionId: "s1" })).toEqual({
			role: "editor", accessScope: "anyone", interaction: "interact", modeCap: "standard",
		});
	});

	it("keeps a watch-only link read-only whatever mode was picked", () => {
		expect(shareLinkRequest({ scope: "project", access: "viewer", modeCap: "trusted", expiry: "168", sessionId: "s1" })).toMatchObject({
			role: "viewer", interaction: "view", modeCap: "read-only", expiresInHours: 168,
		});
	});

	it("copies the short link when the control plane minted a code, else the token link", () => {
		const org = "3f2a1b4c-5d6e-4f70-8a9b-0c1d2e3f4a5b";
		expect(createdShareLinkUrl("https://cp.example.test", org, { code: "k7Hq2x9PmB4w", token: "tok" })).toBe("https://cp.example.test/s/k7Hq2x9PmB4w");
		expect(createdShareLinkUrl("https://cp.example.test", org, { token: "tok" })).toBe(`https://cp.example.test/app/share#${org}/tok`);
		expect(createdShareLinkUrl("https://cp.example.test", org, {})).toBeUndefined();
	});

	it("names a whole-project link and what it lets people do", () => {
		expect(shareLinkLabel({ role: "editor", sessionId: "s1" }, t)).toBe("Write to the agent");
		expect(shareLinkLabel({ role: "editor" }, t)).toBe("Whole project · Start sessions");
		expect(shareLinkLabel({ role: "viewer" }, t)).toBe("Whole project · Watch");
	});
});
