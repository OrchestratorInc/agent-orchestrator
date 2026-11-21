import { describe, expect, it } from "vitest";
import { defaultReviewerHarness, inheritedReviewerConfig, reviewerChoices, reviewerSelectionChanged, reviewerSwitchSelection, reviewerSwitchWarning } from "./reviewerControls";

describe("mobile reviewer controls", () => {
	it("allows authorized and auth-unknown reviewers but disables unavailable agents", () => {
		const choices = reviewerChoices({
			supported: [{ id: "codex", label: "Codex" }, { id: "claude-code", label: "Claude" }, { id: "aider", label: "Aider" }],
			installed: [{ id: "codex", label: "Codex", authStatus: "authorized" }, { id: "claude-code", label: "Claude", authStatus: "unknown" }],
			authorized: [{ id: "codex", label: "Codex" }],
		});
		expect(choices.map(({ id, selectable, status }) => ({ id, selectable, status }))).toEqual([
			{ id: "codex", selectable: true, status: "" },
			{ id: "claude-code", selectable: true, status: "Auth unknown" },
			{ id: "aider", selectable: false, status: "Needs install" },
		]);
	});

	it("clears the override to inherit the project default", () => {
		expect(reviewerSwitchSelection("", {})).toEqual({});
	});

	it("keeps model and mode on a reviewer switch", () => {
		expect(reviewerSwitchSelection("codex", { model: "gpt-5", mode: "plan", effort: "" })).toEqual({
			harness: "codex",
			agentConfig: { model: "gpt-5", mode: "plan" },
		});
	});

	it("distinguishes reviewer changes from selecting the current settings", () => {
		expect(reviewerSelectionChanged("codex", { model: "gpt-5", effort: "" }, "codex", { model: "gpt-5" })).toBe(false);
		expect(reviewerSelectionChanged("codex", { model: "gpt-5" }, "codex", { model: "gpt-6" })).toBe(true);
		expect(reviewerSelectionChanged("codex", {}, "claude-code", {})).toBe(true);
	});

	it("only warns when switching an active review", () => {
		expect(reviewerSwitchWarning(false)).toBeUndefined();
		expect(reviewerSwitchWarning(true)).toContain("cancels its running review");
		expect(reviewerSwitchWarning(true)).toContain("reviewer settings");
	});
	it("resolves the project default reviewer like desktop", () => {
		expect(defaultReviewerHarness([{ harness: "codex" }, { harness: "claude-code" }], "claude-code")).toBe("codex");
		expect(defaultReviewerHarness([], "opencode")).toBe("opencode");
		expect(defaultReviewerHarness(undefined, "codex")).toBe("codex");
		expect(defaultReviewerHarness(undefined, "aider")).toBe("claude-code");
		expect(defaultReviewerHarness(undefined, undefined)).toBe("claude-code");
	});
});

describe("inheritedReviewerConfig", () => {
	it("inherits the project's reviewer settings only for that reviewer", () => {
		const config = { reviewers: [{ harness: "codex", agentConfig: { model: "gpt-5", effort: "high" } }] };
		expect(inheritedReviewerConfig(config, "codex")).toEqual({ model: "gpt-5", effort: "high" });
		expect(inheritedReviewerConfig(config, "claude-code")).toEqual({});
	});

	it("inherits the default worker's model and effort when it reviews as itself", () => {
		const config = { agentConfig: { model: "opus", effort: "low" }, worker: { agent: "claude-code", agentConfig: { effort: "max" } } };
		expect(inheritedReviewerConfig(config, "claude-code")).toEqual({ model: "opus", effort: "max" });
		expect(inheritedReviewerConfig(config, "codex")).toEqual({});
		expect(inheritedReviewerConfig({ worker: { agent: "cursor", agentConfig: { model: "auto" } } }, "cursor")).toEqual({});
	});

	it("inherits nothing without a project config", () => {
		expect(inheritedReviewerConfig(undefined, "claude-code")).toEqual({});
	});
});
