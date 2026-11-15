import { describe, expect, it } from "vitest";
import { artifactCountBucket, artifactFileType, artifactOwner, artifactSource } from "./artifact-telemetry";

describe("artifact telemetry buckets", () => {
	it("buckets a file by its extension, never returning the extension itself", () => {
		expect(artifactFileType("report.html")).toBe("html");
		expect(artifactFileType("docs/Plan.MD")).toBe("markdown");
		expect(artifactFileType("shots/a.png")).toBe("image");
		expect(artifactFileType("data/rows.csv")).toBe("data");
		expect(artifactFileType("notes.weird-ext")).toBe("other");
		expect(artifactFileType("Makefile")).toBe("other");
		expect(artifactFileType(".env")).toBe("other");
		expect(artifactFileType("app.icon/icon.json", true)).toBe("bundle");
		expect(artifactFileType("report.constructor")).toBe("other");
		expect(artifactFileType("report.__proto__")).toBe("other");
	});

	it("bands artifact counts", () => {
		expect([0, 1, 2, 5, 6, 20, 21, 50, 51, 200, 201].map(artifactCountBucket)).toEqual([
			"0", "1", "2-5", "2-5", "6-20", "6-20", "21-50", "21-50", "51-200", "51-200", "200+",
		]);
	});

	it("names where the session runs", () => {
		expect(artifactSource({})).toBe("local");
		expect(artifactSource({ hostId: "host-1" })).toBe("remote_host");
		expect(artifactSource({ cloud: { orgId: "org" }, hostId: "host-1" })).toBe("cloud");
	});

	it("compares cloud owners without exposing either account", () => {
		expect(artifactOwner({ createdByUserId: "u1" }, "u1")).toBe("self");
		expect(artifactOwner({ createdByUserId: "u1" }, "u2")).toBe("teammate");
		expect(artifactOwner({ createdByUserId: "u1" }, undefined)).toBeUndefined();
		expect(artifactOwner({}, "u1")).toBeUndefined();
		expect(artifactOwner(undefined, "u1")).toBeUndefined();
	});
});
