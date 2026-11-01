import { describe, expect, it } from "vitest";
import type { CloudCpProject } from "./cloud-cp";
import { cloudProjectSettingsDraft, cloudProjectSettingsPatch } from "./cloud-project-settings";

const project = (config: CloudCpProject["config"]): CloudCpProject => ({
	id: "p", orgId: "o", displayName: "P", repositoryUrl: "https://github.com/acme/app", defaultBranch: "main",
	createdAt: "now", updatedAt: "now", config,
});

describe("cloud project settings draft", () => {
	it("names Claude Code for a role without an agent without sending it until the role changes", () => {
		const before = cloudProjectSettingsDraft(project({}));
		expect(before.worker.agent).toBe("claude-code");
		expect(before.orchestrator.agent).toBe("claude-code");
		expect(cloudProjectSettingsPatch(before, cloudProjectSettingsDraft(project({})))).toEqual({});
	});

	it("sends workers-request-review and the whole issue intake rule when they change", () => {
		const before = cloudProjectSettingsDraft(project({ trackerIntake: { enabled: false, assignee: "octo" } }));
		expect(before.intake).toEqual({ enabled: false, repo: "", assignee: "octo" });
		const after = { ...before, workersRequestReview: true, intake: { ...before.intake, enabled: true, repo: " acme/app " } };
		expect(cloudProjectSettingsPatch(before, after)).toEqual({
			config: { workersRequestReview: true, trackerIntake: { enabled: true, repo: "acme/app", assignee: "octo" } },
		});
	});
});
