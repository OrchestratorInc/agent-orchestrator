import { describe, expect, it } from "vitest";
import { createProjectConfig } from "../routes/_shell";

describe("createProjectConfig", () => {
	it("sends no agent fields and no tracker intake by default", () => {
		expect(createProjectConfig({})).toEqual({});
	});

	it("keeps the default branch and tracker intake without any agent fields", () => {
		expect(
			createProjectConfig({
				defaultBranch: "trunk",
				trackerIntake: { enabled: true, provider: "github", assignee: "octocat" },
			}),
		).toEqual({
			defaultBranch: "trunk",
			trackerIntake: { enabled: true, provider: "github", assignee: "octocat" },
		});
	});
});
