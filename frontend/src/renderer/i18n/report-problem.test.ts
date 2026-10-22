import { describe, expect, it } from "vitest";
import { appCatalogs } from "./all-messages";

describe("Report a problem translations", () => {
	it.each(Object.entries(appCatalogs))("has the report action and feedback in %s", (_, catalog) => {
		for (const key of ["report.composerBusy", "report.prepareFailed", "report.timedOut", "report.tooLarge", "report.terminalFailed", "report.desktopRequired", "report.runCommand", "report.action", "report.description", "report.opening", "report.failed", "report.linkFailed", "report.github", "report.discord", "report.email"] as const) {
			expect(catalog[key]).toBeTruthy();
			expect(catalog[key]).not.toMatch(/\bAO\b|ao triage/);
		}
		expect(Object.keys(catalog).filter((key) => key.startsWith("report.triage"))).toEqual([]);
	});
});

it.each(Object.entries(appCatalogs).filter(([locale]) => locale !== "en"))("translates report feedback in %s", (_, catalog) => {
 for (const key of ["report.action", "report.description", "report.opening", "report.failed", "report.linkFailed", "report.composerBusy", "report.prepareFailed", "report.timedOut", "report.tooLarge", "report.terminalFailed", "report.desktopRequired", "report.runCommand"] as const) {
  expect(catalog[key]).not.toBe(appCatalogs.en[key]);
 }
});
