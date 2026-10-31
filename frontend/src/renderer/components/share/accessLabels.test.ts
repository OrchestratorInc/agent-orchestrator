import { describe, expect, it } from "vitest";
import { appI18n } from "../../i18n";
import { capLabel, defaultCap, effectiveAccessText, listNames, roleLabel } from "./accessLabels";

const t = appI18n.t.bind(appI18n);

describe("access labels", () => {
	it("names writing by scope, as the link dialog always has", () => {
		expect(roleLabel(t, "viewer", "session")).toBe("Watch");
		expect(roleLabel(t, "editor", "session")).toBe("Write to the agent");
		expect(roleLabel(t, "editor", "project")).toBe("Start sessions");
		expect(capLabel(t, "trusted")).toBe("Trusted");
		expect(capLabel(t, undefined)).toBe("Standard");
	});

	it("defaults the limit the way links do", () => {
		expect(defaultCap("viewer")).toBe("read-only");
		expect(defaultCap("editor")).toBe("standard");
	});

	it("says where a member's access comes from only when it isn't this team", () => {
		expect(effectiveAccessText(t, { role: "member", source: "workspace" }, "team-a", "session")).toEqual({ level: "Full access", source: "workspace member" });
		expect(effectiveAccessText(t, { role: "editor", modeCap: "standard", source: "team", teamId: "team-a", teamName: "Platform" }, "team-a", "session")).toEqual({ level: "Write to the agent" });
		expect(effectiveAccessText(t, { role: "viewer", source: "team", teamId: "team-b", teamName: "Data infra" }, "team-a", "session")).toEqual({ level: "Watch", source: "via Data infra" });
		expect(effectiveAccessText(t, { role: "editor", modeCap: "trusted", source: "link" }, "team-a", "session")).toEqual({ level: "Trusted", source: "shared directly" });
		expect(effectiveAccessText(t, { role: "viewer", source: "project" }, "team-a", "session")).toEqual({ level: "Watch", source: "from the whole project" });
		expect(effectiveAccessText(t, {}, "team-a", "session")).toEqual({ level: "No access" });
	});

	it("lists names in the person's language and folds the rest", () => {
		const more = (count: number) => t("share.summary.more", { count });
		expect(listNames(["Ben"], "en", more)).toBe("Ben");
		expect(listNames(["Ben", "Dana"], "en", more)).toBe("Ben and Dana");
		expect(listNames(["Ben", "Dana", "Eli"], "en", more)).toBe("Ben, Dana, and Eli");
		expect(listNames(["Ben", "Dana", "Eli", "Farah", "Omar"], "en", more)).toBe("Ben, Dana, Eli, and 2 more");
	});
});
