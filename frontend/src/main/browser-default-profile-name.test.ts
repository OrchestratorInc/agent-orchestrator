import { describe, expect, it } from "vitest";
import { defaultBrowserProfileName } from "./browser-default-profile-name";

describe("defaultBrowserProfileName", () => {
	it("names the first-run profile in the user's interface language", () => {
		expect(defaultBrowserProfileName("en")).toBe("Personal");
		expect(defaultBrowserProfileName("de")).toBe("Persönlich");
		expect(defaultBrowserProfileName("ja")).toBe("個人");
	});
});
