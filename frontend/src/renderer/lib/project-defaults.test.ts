import { beforeEach, describe, expect, it } from "vitest";
import { projectDefaultsConfig, readProjectDefaults, saveProjectDefaults } from "./project-defaults";

describe("project defaults", () => {
	beforeEach(() => window.localStorage.clear());

	it("reads nothing until onboarding saves a choice, then reads it back", () => {
		expect(readProjectDefaults()).toBeNull();
		saveProjectDefaults({ archiveOnMerge: true, review: true, reviewerAgent: "codex", assignedIssues: true });
		expect(readProjectDefaults()).toEqual({ archiveOnMerge: true, review: true, reviewerAgent: "codex", assignedIssues: true });
	});

	it("ignores stored values of the wrong shape", () => {
		window.localStorage.setItem("ao.projectDefaults", JSON.stringify({ archiveOnMerge: "yes", reviewerAgent: 4 }));
		expect(readProjectDefaults()).toEqual({ archiveOnMerge: false, review: false, reviewerAgent: undefined, assignedIssues: false });
		window.localStorage.setItem("ao.projectDefaults", "{");
		expect(readProjectDefaults()).toBeNull();
	});

	it("turns the choices into project config, falling back to the project's reviewer", () => {
		expect(projectDefaultsConfig(null)).toEqual({});
		expect(projectDefaultsConfig({ archiveOnMerge: false, review: true, assignedIssues: false }, "opencode"))
			.toEqual({ terminateOnPrMerge: false, autoReview: true, reviewers: [{ harness: "opencode" }] });
		expect(projectDefaultsConfig({ archiveOnMerge: true, review: false, reviewerAgent: "codex", assignedIssues: false }))
			.toEqual({ terminateOnPrMerge: true });
	});
});
