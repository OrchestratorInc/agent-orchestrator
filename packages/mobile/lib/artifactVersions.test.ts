import { describe, expect, it } from "vitest";
import { formatVersionTime, parseVersion, parseVersionSummaries, restoredNote, versionAuthor, type ArtifactVersionSummary } from "./artifactVersions";

describe("parseVersionSummaries", () => {
	it("keeps well-formed versions in the daemon's order", () => {
		expect(parseVersionSummaries([
			{ version: 3, size: 120, source: "user", restoredFrom: 1, harness: "codex", fileFingerprint: "abc", createdAt: "2026-10-28T10:00:00Z" },
			{ version: 2, size: 80, source: "agent", createdAt: "2026-10-28T09:00:00Z" },
		])).toEqual([
			{ version: 3, size: 120, source: "user", restoredFrom: 1, harness: "codex", createdAt: "2026-10-28T10:00:00Z" },
			{ version: 2, size: 80, source: "agent", createdAt: "2026-10-28T09:00:00Z" },
		]);
	});

	it("drops versions without a usable number", () => {
		expect(parseVersionSummaries([{ version: 0 }, { version: 1.5 }, { version: "2" }, null, { version: 4, source: "robot" }])).toEqual([
			{ version: 4, size: 0, source: "agent", createdAt: "" },
		]);
		expect(parseVersionSummaries(undefined)).toEqual([]);
	});

	it("reads a version's content and whether it was cut short", () => {
		expect(parseVersion({ version: 2, createdAt: "x", content: "# Plan", truncated: true })).toEqual({ version: 2, size: 0, source: "agent", createdAt: "x", content: "# Plan", truncated: true });
		expect(parseVersion({ version: 2 })?.truncated).toBe(false);
	});
});

describe("formatVersionTime", () => {
	const now = new Date(2026, 9, 28, 21, 0);

	it("names today and yesterday, and dates anything older", () => {
		expect(formatVersionTime(new Date(2026, 9, 28, 20, 31).toISOString(), now, "en-US")).toBe("Today, 8:31 PM");
		expect(formatVersionTime(new Date(2026, 9, 27, 9, 5).toISOString(), now, "en-US")).toBe("Yesterday, 9:05 AM");
		expect(formatVersionTime(new Date(2026, 9, 11, 9, 5).toISOString(), now, "en-US")).toBe("Oct 11, 9:05 AM");
	});

	it("adds the year for another year, and nothing for a bad date", () => {
		expect(formatVersionTime(new Date(2025, 11, 31, 9, 5).toISOString(), now, "en-US")).toBe("Dec 31, 2025, 9:05 AM");
		expect(formatVersionTime("not a date", now, "en-US")).toBe("");
	});
});

describe("restoredNote", () => {
	const now = new Date(2026, 9, 28, 21, 0);
	const versions: ArtifactVersionSummary[] = [
		{ version: 3, size: 1, source: "user", restoredFrom: 1, createdAt: new Date(2026, 9, 28, 20, 40).toISOString() },
		{ version: 2, size: 1, source: "agent", createdAt: new Date(2026, 9, 28, 20, 35).toISOString() },
		{ version: 1, size: 1, source: "agent", createdAt: new Date(2026, 9, 28, 20, 31).toISOString() },
	];

	it("says which version a restore came from", () => {
		expect(restoredNote(versions[0]!, versions, now, "en-US")).toBe("Restored from Today, 8:31 PM");
		expect(restoredNote(versions[1]!, versions, now, "en-US")).toBeUndefined();
	});

	it("still says it was a restore once the source version is pruned", () => {
		expect(restoredNote({ ...versions[0]!, restoredFrom: 9 }, versions, now, "en-US")).toBe("Restored from an earlier version");
	});
});

describe("versionAuthor", () => {
	it("credits the user, the recorded agent, or the session's agent", () => {
		expect(versionAuthor({ version: 1, size: 0, source: "user", createdAt: "" }, "codex")).toEqual({ kind: "user" });
		expect(versionAuthor({ version: 1, size: 0, source: "agent", harness: "claude-code", createdAt: "" }, "codex")).toEqual({ kind: "agent", harness: "claude-code" });
		expect(versionAuthor({ version: 1, size: 0, source: "agent", createdAt: "" }, "codex")).toEqual({ kind: "agent", harness: "codex" });
	});
});
