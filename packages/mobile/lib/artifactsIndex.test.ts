import { describe, expect, it } from "vitest";
import type { SessionArtifact } from "./artifacts";
import { artifactIndexRows, filterArtifactRows } from "./artifactsIndex";

const file = (path: string, updatedAt: string, over: Partial<SessionArtifact> = {}): SessionArtifact => ({ path, name: path.split("/").pop()!, kind: path.endsWith(".html") ? "html" : "markdown", size: 10, updatedAt, registered: true, ...over });
const names = (id: string) => (id === "web" ? "Web app" : undefined);

describe("artifactIndexRows", () => {
	it("lists every session's deliverables together, newest first, with their session and project", () => {
		const rows = artifactIndexRows([
			{ id: "s1", hostId: "h", projectId: "web", title: "Fix login", artifacts: [file("report.html", "2026-11-20T10:00:00Z")] },
			{ id: "s2", hostId: "h", title: "Notes", artifacts: [file("plan.md", "2026-11-21T10:00:00Z")] },
			{ id: "s3", hostId: "h", title: "Empty" },
		], (_host, id) => names(id));
		expect(rows.map((row) => [row.name, row.sessionTitle, row.projectName])).toEqual([["plan.md", "Notes", undefined], ["report.html", "Fix login", "Web app"]]);
		expect(rows[1]).toMatchObject({ sessionId: "s1", hostId: "h", path: "report.html" });
	});

	it("searches the file, its session and its project", () => {
		const rows = artifactIndexRows([
			{ id: "s1", hostId: "h", projectId: "web", title: "Fix login", artifacts: [file("report.html", "2026-11-20T10:00:00Z")] },
			{ id: "s2", hostId: "h", title: "Notes", artifacts: [file("plan.md", "2026-11-21T10:00:00Z")] },
		], (_host, id) => names(id));
		expect(filterArtifactRows(rows, "web").map((row) => row.name)).toEqual(["report.html"]);
		expect(filterArtifactRows(rows, "NOTES").map((row) => row.name)).toEqual(["plan.md"]);
		expect(filterArtifactRows(rows, " ")).toHaveLength(2);
	});
});
