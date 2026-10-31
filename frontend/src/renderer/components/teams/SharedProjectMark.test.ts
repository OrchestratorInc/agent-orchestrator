import { describe, expect, it } from "vitest";
import { appI18n } from "../../i18n";
import { sharedProjectLines } from "./SharedProjectMark";

const t = appI18n.t.bind(appI18n);

describe("sharedProjectLines", () => {
	it("speaks for a whole project share", () => {
		expect(sharedProjectLines({ wholeProject: { byName: "Riya Sen", viaTeamName: "Design guild" }, sessions: { s1: { byName: "Kai", viaTeamName: "Mobile" } } }, t, "en"))
			.toEqual(["Shared by Riya Sen", "Through Design guild"]);
	});

	it("names every sharer and team when sessions came separately", () => {
		expect(sharedProjectLines({ byName: "Asha Rao", viaTeamName: "Platform", sessions: { s1: { byName: "Asha Rao", viaTeamName: "Platform" }, s2: { byName: "Asha Rao", viaTeamName: "Checkout" }, s3: { byName: "Omar Haddad" } } }, t, "en"))
			.toEqual(["Shared by Asha Rao and Omar Haddad", "Through Platform and Checkout"]);
	});

	it("says only that it's shared when nobody is named", () => {
		expect(sharedProjectLines({ sessions: { s1: {} } }, t, "en")).toEqual(["Shared with you"]);
	});
});
