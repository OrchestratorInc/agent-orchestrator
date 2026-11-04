import { describe, expect, it } from "vitest";
import type { CloudCpProject } from "./cloud-cp";
import { cloudProjectSettingsDraft, cloudProjectSettingsPatch } from "./cloud-project-settings";

const project = (config: CloudCpProject["config"]): CloudCpProject => ({
	id: "p", orgId: "o", displayName: "P", repositoryUrl: "https://github.com/acme/app", defaultBranch: "main",
	createdAt: "now", updatedAt: "now", config,
});

describe("cloud project settings draft", () => {
	it("reads only role permissions and ignores a pinned role agent", () => {
		const draft = cloudProjectSettingsDraft(project({
			worker: { agent: "codex", agentConfig: { model: "gpt", permissions: "auto" } },
			orchestrator: { agent: "cursor" },
			workerAgent: "codex",
		}));
		expect(draft.workerPermissions).toBe("auto");
		expect(draft.orchestratorPermissions).toBe("");
		expect(draft).not.toHaveProperty("worker");
		expect(draft).not.toHaveProperty("orchestrator");
		expect(cloudProjectSettingsPatch(draft, cloudProjectSettingsDraft(project({})))).toEqual({
			config: { worker: { agentConfig: { permissions: "" } } },
		});
	});

	it("sends only the permissions of a role that changed, never an agent or null", () => {
		const before = cloudProjectSettingsDraft(project({}));
		expect(cloudProjectSettingsPatch(before, cloudProjectSettingsDraft(project({})))).toEqual({});
		const patch = cloudProjectSettingsPatch(before, { ...before, orchestratorPermissions: "bypass-permissions" });
		expect(patch).toEqual({ config: { orchestrator: { agentConfig: { permissions: "bypass-permissions" } } } });
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
