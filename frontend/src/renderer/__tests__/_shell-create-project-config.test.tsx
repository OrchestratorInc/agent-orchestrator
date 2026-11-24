import { describe, expect, it } from "vitest";
import { createProjectConfig } from "../routes/_shell";

describe("createProjectConfig", () => {
	it("persists selected worker and orchestrator agents without tracker intake by default", () => {
		expect(
			createProjectConfig({
				workerAgent: "codex",
				orchestratorAgent: "claude-code",
			}),
		).toEqual({
			worker: { agent: "codex" },
			orchestrator: { agent: "claude-code" },
		});
	});

	it("preserves tracker intake alongside selected agent defaults", () => {
		expect(
			createProjectConfig({
				workerAgent: "cursor",
				orchestratorAgent: "opencode",
				trackerIntake: { enabled: true, provider: "github", assignee: "octocat" },
			}),
		).toEqual({
			worker: { agent: "cursor" },
			orchestrator: { agent: "opencode" },
			trackerIntake: { enabled: true, provider: "github", assignee: "octocat" },
		});
	});

	it("adds how agents finish their work, as chosen in onboarding", () => {
		expect(
			createProjectConfig(
				{ workerAgent: "codex", orchestratorAgent: "claude-code" },
				{ archiveOnMerge: true, review: true, reviewerAgent: "claude-code", assignedIssues: false },
			),
		).toEqual({
			worker: { agent: "codex" },
			orchestrator: { agent: "claude-code" },
			terminateOnPrMerge: true,
			autoReview: true,
			reviewers: [{ harness: "claude-code" }],
		});
		expect(
			createProjectConfig(
				{ workerAgent: "codex", orchestratorAgent: "claude-code", reviewerAgent: "opencode" },
				{ archiveOnMerge: false, review: false, assignedIssues: false },
			),
		).toEqual({
			worker: { agent: "codex" },
			orchestrator: { agent: "claude-code" },
			reviewers: [{ harness: "opencode" }],
			terminateOnPrMerge: false,
		});
	});
});
