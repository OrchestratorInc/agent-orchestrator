import { beforeEach, describe, expect, it } from "vitest";
import { projectDefaultsConfig, readProjectDefaults, remoteProjectDefaults, saveProjectDefaults, withChosenAgents } from "./project-defaults";

describe("project defaults", () => {
	beforeEach(() => window.localStorage.clear());

	it("reads nothing until onboarding saves a choice, then reads it back", () => {
		expect(readProjectDefaults()).toBeNull();
		saveProjectDefaults({ archiveOnMerge: true, review: true, reviewerAgent: "codex", assignedIssues: true, fixChecks: false });
		expect(readProjectDefaults()).toEqual({ archiveOnMerge: true, review: true, reviewerAgent: "codex", assignedIssues: true, fixChecks: false, fixReviewComments: true });
	});

	it("keeps what earlier pages saved when a later one saves its own", () => {
		saveProjectDefaults({ orchestratorAgent: "codex", workerAgent: "claude-code" });
		expect(readProjectDefaults()).toMatchObject({ orchestratorAgent: "codex", workerAgent: "claude-code", archiveOnMerge: true, fixChecks: true });
		saveProjectDefaults({ archiveOnMerge: false, fixReviewComments: false });
		expect(readProjectDefaults()).toMatchObject({ orchestratorAgent: "codex", workerAgent: "claude-code", archiveOnMerge: false, fixReviewComments: false });
	});

	it("ignores stored values of the wrong shape", () => {
		window.localStorage.setItem("ao.projectDefaults", JSON.stringify({ archiveOnMerge: "yes", reviewerAgent: 4 }));
		expect(readProjectDefaults()).toEqual({ archiveOnMerge: false, review: false, reviewerAgent: undefined, assignedIssues: false, fixChecks: true, fixReviewComments: true, orchestratorAgent: undefined, workerAgent: undefined });
		window.localStorage.setItem("ao.projectDefaults", "{");
		expect(readProjectDefaults()).toBeNull();
	});

	it("turns the choices into project config, falling back to the project's reviewer", () => {
		expect(projectDefaultsConfig(null)).toEqual({});
		expect(projectDefaultsConfig({ archiveOnMerge: false, review: true, assignedIssues: false, fixChecks: true, fixReviewComments: false }, "opencode"))
			.toEqual({ terminateOnPrMerge: false, autoInjectCI: true, autoInjectReview: false, autoReview: true, reviewers: [{ harness: "opencode" }] });
		expect(projectDefaultsConfig({ archiveOnMerge: true, review: false, reviewerAgent: "codex", assignedIssues: false, fixChecks: false, fixReviewComments: true }))
			.toEqual({ terminateOnPrMerge: true, autoInjectCI: false, autoInjectReview: true });
	});

	it("starts projects on the chosen agents while they are ready, otherwise the ranked pick", () => {
		const ranked = { orchestratorAgent: "claude-code", workerAgent: "claude-code", reviewerAgent: "claude-code" };
		const agents = [{ id: "claude-code", effectiveReadiness: "ready" }, { id: "codex", effectiveReadiness: "ready" }, { id: "amp", effectiveReadiness: "not_ready" }];
		const chosen = { ...readProjectDefaults(), archiveOnMerge: true, review: true, assignedIssues: false, fixChecks: true, fixReviewComments: true };
		expect(withChosenAgents(ranked, agents, null)).toEqual(ranked);
		expect(withChosenAgents(ranked, agents, { ...chosen, orchestratorAgent: "codex", workerAgent: "amp" }))
			.toEqual({ orchestratorAgent: "codex", workerAgent: "claude-code", reviewerAgent: "claude-code" });
	});

	it("keeps a remote project on its host's reviewer, carrying over the rest", () => {
		const local = { archiveOnMerge: false, review: true, reviewerAgent: "codex", assignedIssues: false, fixChecks: false, fixReviewComments: true, orchestratorAgent: "codex", workerAgent: "codex" };
		expect(remoteProjectDefaults(null)).toBeNull();
		expect(projectDefaultsConfig(remoteProjectDefaults(local), "claude-code"))
			.toEqual({ terminateOnPrMerge: false, autoInjectCI: false, autoInjectReview: true, autoReview: true, reviewers: [{ harness: "claude-code" }] });
	});
});
