import { describe, expect, it } from "vitest";
import { approvalLabel, approvalOptions, approvalShortLabel, resolveApprovalMode } from "./spawnApproval";

describe("spawn approval modes", () => {
	it("offers every mode in the desktop's order, with its labels", () => {
		expect(approvalOptions("claude-code")).toEqual([
			{ id: "default", label: "Use Claude permissions", shortLabel: "Ask" },
			{ id: "auto", label: "Auto", shortLabel: "Auto" },
			{ id: "accept-edits", label: "Accept edits", shortLabel: "Edits" },
			{ id: "bypass-permissions", label: "Bypass permissions", shortLabel: "Bypass" },
		]);
		expect(approvalShortLabel("bypass-permissions")).toBe("Bypass");
		expect(approvalLabel("default", "aider")).toBe("Use agent permissions");
	});

	it("leaves out the modes an agent cannot start in", () => {
		expect(approvalOptions("codex").map((option) => option.id)).toEqual(["auto", "accept-edits", "bypass-permissions"]);
		expect(approvalOptions("opencode").map((option) => option.id)).toEqual(["default", "auto", "bypass-permissions"]);
	});

	it("names the mode the worker will start in: the choice, the project's, then Auto", () => {
		expect(resolveApprovalMode({ harness: "claude-code", chosen: "accept-edits", projectMode: "default" })).toBe("accept-edits");
		expect(resolveApprovalMode({ harness: "claude-code", projectMode: "bypass-permissions" })).toBe("bypass-permissions");
		expect(resolveApprovalMode({ harness: "claude-code", projectMode: "" })).toBe("auto");
		expect(resolveApprovalMode({ harness: "claude-code", projectMode: "nonsense" })).toBe("auto");
	});

	it("falls back to Auto when the agent cannot start in the chosen or project mode", () => {
		expect(resolveApprovalMode({ harness: "codex", chosen: "default" })).toBe("auto");
		expect(resolveApprovalMode({ harness: "opencode", projectMode: "accept-edits" })).toBe("auto");
	});
});
