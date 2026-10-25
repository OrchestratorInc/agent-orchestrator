import { describe, expect, it } from "vitest";
import type { CloudCpWorkspaceReviewResponse } from "./cloud-cp";
import { cloudWorkspaceFilePath } from "./cloud-workspace-file-path";

const review = {
	workspaceRoot: "/home/coder/repo",
	files: [{ path: "src/panel.tsx" }, { path: "docs/notes.txt" }, { path: "src/shared.ts" }, { path: "lib/shared.ts" }],
	truncated: false,
} as CloudCpWorkspaceReviewResponse;

describe("cloudWorkspaceFilePath", () => {
	it.each([
		["/home/coder/repo/src/panel.tsx:12", "src/panel.tsx"],
		["file:///home/coder/repo/src/panel.tsx#L7", "src/panel.tsx"],
		["notes.txt", "docs/notes.txt"],
		["./docs/notes.txt", "docs/notes.txt"],
	])("resolves %s against Cloud metadata", (reference, path) => {
		expect(cloudWorkspaceFilePath(reference, review)).toBe(path);
	});
	it.each([
		"/outside/src/panel.tsx", "/home/coder/repository/src/panel.tsx", "/home/coder/repo/../secret",
		"/home/coder/repo/src/../../secret", "../secret", "src/../secret", "shared.ts",
		"file://other/home/coder/repo/src/panel.tsx", "https://other/src/panel.tsx", "//other/src/panel.tsx",
	])("does not guess or erase security boundaries for %s", (reference) => {
		expect(cloudWorkspaceFilePath(reference, review)).toBe(reference);
	});
	it("does not infer absolute roots or uniqueness from incomplete metadata", () => {
		expect(cloudWorkspaceFilePath("/home/coder/repo/src/panel.tsx", { ...review, workspaceRoot: undefined })).toBe("/home/coder/repo/src/panel.tsx");
		expect(cloudWorkspaceFilePath("notes.txt", { ...review, truncated: true })).toBe("notes.txt");
	});
});
