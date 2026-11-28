import { readdirSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { routeKey, SURFACES, surfaceForSegments } from "./surfaces";

describe("surfaceForSegments", () => {
	it("maps expo-router segments to a surface", () => {
		expect(surfaceForSegments(["(tabs)", "(workers)"])).toBe("board");
		expect(surfaceForSegments(["(tabs)", "(projects)", "projects"])).toBe("projects");
		expect(surfaceForSegments(["session", "[id]"])).toBe("session");
		expect(surfaceForSegments(["files", "[sessionId]", "file"])).toBe("file_viewer");
		expect(surfaceForSegments(["settings", "machine", "[id]", "edit"])).toBe("settings_machine");
		expect(surfaceForSegments(["sheets", "model"])).toBe("model_picker");
	});

	it("names an unknown route other", () => {
		expect(surfaceForSegments(["something-new"])).toBe("other");
	});

	// Fails when a route file is added without a surface, so new screens show up
	// in usage instead of folding into `other`.
	it("has a surface for every route file", () => {
		const app = join(__dirname, "..", "..", "app");
		const routes = readdirSync(app, { recursive: true, encoding: "utf8" })
			.filter((file) => /\.tsx$/.test(file) && !/(^|\/)_layout\.tsx$/.test(file) && !/\.test\.tsx$/.test(file))
			.map((file) => file.replace(/\.tsx$/, "").split("/"));
		expect(routes.length).toBeGreaterThan(40);
		const unmapped = routes.filter((segments) => surfaceForSegments(segments) === "other").map((segments) => routeKey(segments));
		expect(unmapped).toEqual([]);
	});

	it("only uses ids from the closed list", () => {
		for (const surface of SURFACES) expect(surface).toMatch(/^[a-z_]+$/);
	});
});
