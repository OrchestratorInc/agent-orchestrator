import { describe, expect, it } from "vitest";
import { appCatalogs } from "./all-messages";

describe("Report a problem translations", () => {
	it.each(Object.entries(appCatalogs))("has the report action and feedback in %s", (_, catalog) => {
		for (const key of ["report.action", "report.description", "report.opening", "report.failed", "report.linkFailed", "report.github", "report.discord", "report.email"] as const) {
			expect(catalog[key]).toBeTruthy();
			expect(catalog[key]).not.toMatch(/\bAO\b|ao triage/);
		}
		expect(Object.keys(catalog).filter((key) => key.startsWith("report.triage"))).toEqual([]);
	});
});
