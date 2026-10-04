import { describe, expect, it } from "vitest";
import { canSubmitSpawn, initialSpawnDestination, spawnRequestIsCurrent } from "./spawnDestination";

const local = { kind: "local" as const, id: "desktop-1" };
const cloud = { kind: "cloud" as const, id: "org-1" };

describe("spawn destination", () => {
	it("prefills a project route but requires a choice for a generic two-host spawn", () => {
		expect(initialSpawnDestination(undefined, [local, cloud])).toBeNull();
		expect(initialSpawnDestination(cloud, [local, cloud])).toEqual(cloud);
		expect(initialSpawnDestination(undefined, [local])).toEqual(local);
		expect(initialSpawnDestination({ kind: "local", id: "old-desktop" }, [local, cloud])).toBeNull();
	});
	it("restores the last spawn host when it is still available", () => {
		expect(initialSpawnDestination(undefined, [local, cloud], cloud)).toEqual(cloud);
		expect(initialSpawnDestination(local, [local, cloud], cloud)).toEqual(local);
		expect(initialSpawnDestination(undefined, [local, cloud], { kind: "cloud", id: "old-org" })).toBeNull();
	});
	it("cannot submit into a missing or stale destination", () => {
		expect(canSubmitSpawn(null, "project", "codex", () => undefined)).toBe(false);
		expect(canSubmitSpawn(local, "project", "codex", () => undefined)).toBe(false);
		expect(canSubmitSpawn(local, "project", "codex", () => ({ kind: "local" }))).toBe(true);
		expect(canSubmitSpawn(local, "", "codex", () => ({ kind: "local" }))).toBe(false);
	});
	it("rejects late catalog and model results after switching hosts", () => {
		expect(spawnRequestIsCurrent(1, 2, local, cloud)).toBe(false);
		expect(spawnRequestIsCurrent(2, 2, local, cloud)).toBe(false);
		expect(spawnRequestIsCurrent(2, 2, cloud, cloud)).toBe(true);
	});
});
