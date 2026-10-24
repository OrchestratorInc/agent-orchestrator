import { describe, expect, it } from "vitest";
import { resolveFilesRoute } from "./route";

describe("Files route source", () => {
	it("retains an explicit Local host even when Cloud shares the session id", () => {
		expect(resolveFilesRoute({ sessionId: "same", hostId: "mac" })).toEqual({ kind: "found", sessionId: "same", source: { kind: "local", id: "mac" } });
	});
	it("requires a Cloud account lease instead of guessing the current account", () => {
		expect(resolveFilesRoute({ sessionId: "same", source: "cloud", sourceId: "org" })).toEqual({ kind: "invalid" });
		expect(resolveFilesRoute({ sessionId: "same", source: "cloud", sourceId: "org", scopeKey: "lease" })).toEqual({ kind: "found", sessionId: "same", source: { kind: "cloud", id: "org" }, scopeKey: "lease" });
	});
	it("keeps a sibling repository explicit and normalizes the main repository selection", () => {
		expect(resolveFilesRoute({ sessionId: "same", source: "cloud", sourceId: "org", scopeKey: "lease", repository: "shared-api" })).toMatchObject({ kind: "found", repository: "shared-api" });
		expect(resolveFilesRoute({ sessionId: "same", source: "cloud", sourceId: "org", scopeKey: "lease", repository: "" })).toEqual({ kind: "found", sessionId: "same", source: { kind: "cloud", id: "org" }, scopeKey: "lease" });
	});
	it.each(["../secret", "a/b", "a\\b", "..", ".", " ", "\t", "nul\0repo"])("rejects unsafe sibling repository %j before rendering a workspace", (repository) => {
		expect(resolveFilesRoute({ sessionId: "same", source: "cloud", sourceId: "org", scopeKey: "lease", repository })).toEqual({ kind: "invalid" });
	});
	it.each([
		{ sessionId: "same" },
		{ sessionId: ["same", "other"], hostId: "mac" },
		{ sessionId: "same", hostId: ["mac"] },
		{ sessionId: "same", hostId: "mac", source: "cloud", sourceId: "org", scopeKey: "lease" },
		{ sessionId: "same", hostId: "mac", source: "local", sourceId: "other-mac" },
		{ sessionId: "same", source: "cloud", sourceId: ["org"], scopeKey: "lease" },
		{ sessionId: "same", source: "cloud", sourceId: "org", scopeKey: ["lease"] },
		{ sessionId: "same", source: "cloud", sourceId: "org", scopeKey: " " },
		{ sessionId: "same", source: "cloud", sourceId: "org", scopeKey: "lease", repository: ["shared-api"] },
		{ sessionId: "same", hostId: "mac", repository: "shared-api" },
	])("never falls through a malformed or conflicting route to the active host: %j", (params) => {
		expect(resolveFilesRoute(params)).toEqual({ kind: "invalid" });
	});
});
