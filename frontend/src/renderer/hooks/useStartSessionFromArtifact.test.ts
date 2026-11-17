import { describe, expect, it } from "vitest";
import { artifactFilePath } from "./useStartSessionFromArtifact";

describe("artifactFilePath", () => {
	it("joins in the directory's own style", () => {
		expect(artifactFilePath("/Users/me/.ao/data/artifacts/ao-7", "img/chart.png")).toBe("/Users/me/.ao/data/artifacts/ao-7/img/chart.png");
		expect(artifactFilePath("/Users/me/.ao/data/artifacts/ao-7/", "report.md")).toBe("/Users/me/.ao/data/artifacts/ao-7/report.md");
		expect(artifactFilePath("C:\\Users\\me\\.ao\\artifacts\\ao-7", "img/chart.png")).toBe("C:\\Users\\me\\.ao\\artifacts\\ao-7\\img\\chart.png");
	});
});
