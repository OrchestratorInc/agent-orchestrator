import { describe, expect, it } from "vitest";
import { fileRoute, filesRoute } from "./navigation";

describe("Files navigation ownership", () => {
	it("keeps existing Local links pinned to the paired machine", () => {
		expect(filesRoute("worker", "mac", "Task")).toEqual({ pathname: "/files/[sessionId]", params: { sessionId: "worker", hostId: "mac", title: "Task" } });
		expect(fileRoute("worker", "src/a.ts", "mac", "src/old.ts")).toEqual({ pathname: "/files/[sessionId]/file", params: { sessionId: "worker", path: "src/a.ts", mode: "diff", previousPath: "src/old.ts", hostId: "mac" } });
	});

	it("carries the Cloud account lease without leaking the active Local host", () => {
		const owner = { source: { kind: "cloud" as const, id: "org" }, scopeKey: "production/account/org" };
		expect(filesRoute("worker", "wrong-mac", "Task", owner)).toEqual({ pathname: "/files/[sessionId]", params: { sessionId: "worker", source: "cloud", sourceId: "org", scopeKey: "production/account/org", title: "Task" } });
		expect(fileRoute("worker", "README.md", "wrong-mac", undefined, owner)).toEqual({ pathname: "/files/[sessionId]/file", params: { sessionId: "worker", path: "README.md", mode: "diff", previousPath: undefined, source: "cloud", sourceId: "org", scopeKey: "production/account/org" } });
	});
	it("retains the selected sibling repository in Cloud file links", () => {
		const owner = { source: { kind: "cloud" as const, id: "org" }, scopeKey: "lease", repository: "shared-api" };
		expect(fileRoute("worker", "src/a.ts", undefined, undefined, owner).params).toMatchObject({ source: "cloud", sourceId: "org", scopeKey: "lease", repository: "shared-api", path: "src/a.ts" });
	});
});
