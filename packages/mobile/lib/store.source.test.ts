import { describe, expect, it } from "vitest";
import { acceptSourceResult, sourceMatchesCurrent } from "./environment/boardSelection";

describe("source request identity", () => {
	it("rejects an older request from the same Cloud organization", () => {
		expect(acceptSourceResult({
			requested: { source: { kind: "cloud", id: "org-1" }, generation: 1 },
			current: { source: { kind: "cloud", id: "org-1" }, generation: 2 },
		})).toBe(false);
	});

	it("rejects a result from a previously paired desktop", () => {
		expect(acceptSourceResult({
			requested: { source: { kind: "local", id: "mac-1" }, generation: 1 },
			current: { source: { kind: "local", id: "mac-2" }, generation: 1 },
		})).toBe(false);
	});

	it("accepts the current request only", () => {
		const current = { source: { kind: "local" as const, id: "mac-2" }, generation: 2 };
		expect(acceptSourceResult({ requested: current, current })).toBe(true);
	});
});

describe("source action boundary", () => {
	it("never rebinds a Local action to a newly paired desktop", () => {
		expect(sourceMatchesCurrent({ kind: "local", id: "mac-1" }, { localId: "mac-2", cloudId: "org-1" })).toBe(false);
	});
	it("never lets a Cloud ref invoke a Local action", () => {
		expect(sourceMatchesCurrent({ kind: "cloud", id: "org-1" }, { localId: "mac-1", cloudId: "org-1" }, "local")).toBe(false);
	});
});
