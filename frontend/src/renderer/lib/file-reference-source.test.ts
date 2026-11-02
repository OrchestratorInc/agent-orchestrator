import { describe, expect, it } from "vitest";
import { fileReferenceSource } from "./file-reference-source";

const sha = "a".repeat(40);
describe("single-commit reference sources", () => {
	it("records one workspace commit without changing its display source", () => {
		const source = { kind: "snapshot" as const, label: "Commit", commitShas: [sha], view: { expectedTipSha: "b".repeat(40) } };
		expect(fileReferenceSource(source)).toEqual({ source: { kind: "workspace" }, commitSha: sha });
		expect(source.kind).toBe("snapshot");
	});
	it("retains PR identity and the viewed head", () => {
		expect(fileReferenceSource({ kind: "snapshot", label: "PR", commitShas: [sha], view: { prNumber: 42, sourceUrl: "https://example.test/repo/pull/42", expectedTipSha: "b".repeat(40) } })).toEqual({ commitSha: sha, source: { kind: "pull_request", number: 42, url: "https://example.test/repo/pull/42", snapshot: "b".repeat(40) } });
	});
	it("rejects unsupported all-commit, composite, other-branch and malformed views", () => {
		for (const source of [
			{ kind: "snapshot" as const, label: "All" },
			{ kind: "snapshot" as const, label: "Many", commitShas: [sha, "b".repeat(40)] },
			{ kind: "snapshot" as const, label: "Other branch", commitShas: [sha], view: { branch: "topic" } },
			{ kind: "snapshot" as const, label: "Invalid", commitShas: ["short"] },
		]) expect(fileReferenceSource(source)).toBeUndefined();
	});
});
