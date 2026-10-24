import { describe, expect, it } from "vitest";
import { resolvePreviewRoute } from "./route";
import { previewRoute, legacyPreviewRoute } from "./navigation";

describe("preview route ownership", () => {
	it("keeps colliding Cloud and Local session IDs distinct", () => {
		expect(resolvePreviewRoute({ id: "same", source: "cloud", sourceId: "org", scopeKey: "deployment/account/org" })).toEqual({ kind: "found", id: "same", source: { kind: "cloud", id: "org" }, scopeKey: "deployment/account/org" });
		expect(resolvePreviewRoute({ id: "same", hostId: "host" })).toEqual({ kind: "found", id: "same", source: { kind: "local", id: "host" } });
	});
	it.each([
		{ id: "same" }, { id: ["same"], hostId: "host" }, { id: " ", hostId: "host" },
		{ id: "same", source: "cloud", sourceId: "org" }, { id: "same", source: "cloud", sourceId: "org", scopeKey: ["scope"] },
		{ id: "same", source: "cloud", sourceId: "org", scopeKey: "scope", hostId: "host" },
		{ id: "same", source: "local", sourceId: "other", hostId: "host" }, { id: "same", source: "other", sourceId: "org" },
		{ id: "same", source: "cloud", sourceId: " ", scopeKey: "scope" }, { id: "same", hostId: ["host"] },
	])("rejects unqualified or conflicting source %j", (params) => expect(resolvePreviewRoute(params)).toEqual({ kind: "invalid" }));
	it("carries every owner field through navigation and the legacy redirect", () => {
		const route = previewRoute("same", undefined, "Page", { source: { kind: "cloud", id: "org" }, scopeKey: "scope" });
		expect(route).toEqual({ pathname: "/preview/[id]", params: { id: "same", source: "cloud", sourceId: "org", scopeKey: "scope", title: "Page" } });
		expect(legacyPreviewRoute(route.params)).toEqual(route);
		expect(legacyPreviewRoute({ id: "same", source: "cloud", sourceId: "org" })).toBeNull();
		expect(legacyPreviewRoute({ id: "same", hostId: "host", title: "Page" })?.params).toMatchObject({ id: "same", source: "local", sourceId: "host", hostId: "host", title: "Page" });
	});
});
