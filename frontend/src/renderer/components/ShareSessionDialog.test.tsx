import { describe, expect, it } from "vitest";
import { appI18n } from "../i18n";
import { shareLinkLabel, shareLinkRequest } from "./ShareSessionDialog";

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

	it("names a whole-project link and what it lets people do", () => {
		expect(shareLinkLabel({ role: "editor", sessionId: "s1" }, t)).toBe("Write to the agent");
		expect(shareLinkLabel({ role: "editor" }, t)).toBe("Whole project · Start sessions");
		expect(shareLinkLabel({ role: "viewer" }, t)).toBe("Whole project · Watch");
	});
});
