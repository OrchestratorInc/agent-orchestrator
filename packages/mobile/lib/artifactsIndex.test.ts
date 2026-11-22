import { describe, expect, it } from "vitest";
import type { SessionArtifact } from "./artifacts";
import { artifactIndexRows, artifactLocationCounts, filterArtifactRows, rowsAtLocation } from "./artifactsIndex";

const file = (path: string, updatedAt: string, over: Partial<SessionArtifact> = {}): SessionArtifact => ({ path, name: path.split("/").pop()!, kind: path.endsWith(".html") ? "html" : "markdown", size: 10, updatedAt, registered: true, ...over });
const names = (id: string) => (id === "web" ? "Web app" : undefined);

describe("artifactIndexRows", () => {
	it("lists every session's deliverables together, newest first, with their session and project", () => {
		const rows = artifactIndexRows([
			{ id: "s1", hostId: "h", projectId: "web", outputType: "artifact", title: "Fix login", artifacts: [file("report.html", "2026-11-20T10:00:00Z")] },
			{ id: "s2", hostId: "h", outputType: "pr_artifact", title: "Notes", artifacts: [file("plan.md", "2026-11-21T10:00:00Z")] },
			{ id: "s3", hostId: "h", title: "Empty" },
		], (_host, id) => names(id));
		expect(rows.map((row) => [row.name, row.sessionTitle, row.projectName])).toEqual([["plan.md", "Notes", undefined], ["report.html", "Fix login", "Web app"]]);
		expect(rows[1]).toMatchObject({ sessionId: "s1", hostId: "h", path: "report.html" });
	});

	// The desktop lists a session's files only when it was started to deliver
	// artifacts (sessionArtifacts); a PR-only session's files are not listed.
	it("lists only sessions that deliver artifacts, as the desktop does", () => {
		const rows = artifactIndexRows([
			{ id: "pr", hostId: "h", outputType: "pr", title: "PR only", artifacts: [file("scratch.md", "2026-11-21T10:00:00Z")] },
			{ id: "art", hostId: "h", outputType: "artifact", title: "Report", artifacts: [file("report.html", "2026-11-20T10:00:00Z")] },
			{ id: "both", hostId: "h", outputType: "pr_artifact", title: "Both", artifacts: [file("both.md", "2026-11-19T10:00:00Z")] },
			{ id: "unknown", hostId: "h", title: "No output type", artifacts: [file("notes.md", "2026-11-18T10:00:00Z")] },
		], () => undefined);
		expect(rows.map((row) => row.sessionId)).toEqual(["art", "both"]);
	});

	it("searches the file, its session and its project", () => {
		const rows = artifactIndexRows([
			{ id: "s1", hostId: "h", projectId: "web", outputType: "artifact", title: "Fix login", artifacts: [file("report.html", "2026-11-20T10:00:00Z")] },
			{ id: "s2", hostId: "h", outputType: "pr_artifact", title: "Notes", artifacts: [file("plan.md", "2026-11-21T10:00:00Z")] },
		], (_host, id) => names(id));
		expect(filterArtifactRows(rows, "web").map((row) => row.name)).toEqual(["report.html"]);
		expect(filterArtifactRows(rows, "NOTES").map((row) => row.name)).toEqual(["plan.md"]);
		expect(filterArtifactRows(rows, " ")).toHaveLength(2);
	});
});

describe("location chips", () => {
	const rows = artifactIndexRows([
		{ id: "a", hostId: "m5", outputType: "artifact", title: "On M5", artifacts: [file("one.md", "2026-11-21T10:00:00Z"), file("two.md", "2026-11-20T10:00:00Z")] },
		{ id: "b", hostId: "cloud", outputType: "artifact", title: "In Cloud", artifacts: [file("three.md", "2026-11-19T10:00:00Z")] },
	], () => undefined);

	it("counts every location, with zero where it has none", () => {
		const counts = artifactLocationCounts(rows, [{ hostId: "m5", label: "M5" }, { hostId: "mini", label: "Mini" }, { hostId: "cloud", label: "Cloud" }]);
		expect(counts.all).toBe(3);
		expect(Object.fromEntries(counts.byHost)).toEqual({ m5: 2, mini: 0, cloud: 1 });
	});

	it("shows one location's rows, or all of them", () => {
		expect(rowsAtLocation(rows, "cloud").map((row) => row.name)).toEqual(["three.md"]);
		expect(rowsAtLocation(rows, undefined)).toHaveLength(3);
	});
});
