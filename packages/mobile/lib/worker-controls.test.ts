import { describe, expect, it } from "vitest";
import {
	ALL_WORKER_PROJECTS,
	filterWorkersByProject,
	spawnProjectParam,
	workerProjectLabel,
	workerProjectOptions,
	workerSearchPresentation,
	scopedWorkerProjectOptions,
	boardWorkerKey,
} from "./worker-controls";
import type { Scoped } from "./environment/scopedBoard";
import type { ProjectInfo, DashboardSession } from "./api";

describe("filterWorkersByProject", () => {
	it("keeps every worker when All projects is selected", () => {
		const workers = [{ projectId: "alpha" }, { projectId: "beta" }];
		expect(filterWorkersByProject(workers, "all")).toEqual(workers);
	});

	it("keeps only workers from the locally selected project", () => {
		const workers = [{ projectId: "alpha" }, { projectId: "beta" }, { projectId: "alpha" }];
		expect(filterWorkersByProject(workers, "beta")).toEqual([{ projectId: "beta" }]);
	});
});

describe("workerSearchPresentation", () => {
	it("stays expanded while a non-empty query is visible", () => {
		expect(workerSearchPresentation(false, "compiler")).toBe("expanded");
	});

	it("collapses only when search is closed and empty", () => {
		expect(workerSearchPresentation(false, "")).toBe("collapsed");
		expect(workerSearchPresentation(true, "")).toBe("expanded");
	});
});

describe("workerProjectLabel", () => {
	it("uses a stable fallback if a selected project was removed", () => {
		expect(workerProjectLabel([{ id: "alpha", name: "Alpha" }], "missing")).toBe("All projects");
		expect(workerProjectLabel([{ id: "alpha", name: "Alpha" }], "alpha")).toBe("Alpha");
	});
});

describe("workerProjectOptions", () => {
	it("puts the reset option before every available project", () => {
		expect(
			workerProjectOptions([
				{ id: "alpha", name: "Alpha" },
				{ id: "beta", name: "Beta" },
			]),
		).toEqual([
			{ id: "all", label: "All projects" },
			{ id: "alpha", label: "Alpha" },
			{ id: "beta", label: "Beta" },
		]);
	});
});

describe("spawnProjectParam", () => {
	it("carries a concrete Workers project filter into the spawn sheet", () => {
		expect(spawnProjectParam("project-42")).toEqual({ projectId: "project-42" });
	});

	it("leaves project selection open when Workers shows every project", () => {
		expect(spawnProjectParam(ALL_WORKER_PROJECTS)).toBeUndefined();
	});
});

describe("combined Worker filters", () => {
	const local = { kind: "local", id: "mac" } as const;
	const cloud = { kind: "cloud", id: "org" } as const;
	const projects = [
		{ source: local, value: { id: "same", name: "Shared" } },
		{ source: cloud, value: { id: "same", name: "Shared" } },
	] as Scoped<ProjectInfo>[];
	it("qualifies identical project IDs and filters by environment", () => {
		const all = scopedWorkerProjectOptions(projects, "all");
		expect(all).toHaveLength(3);
		expect(all[1].id).not.toBe(all[2].id);
		expect(all[1].label).toContain("Local");
		expect(all[2].label).toContain("Cloud");
		expect(scopedWorkerProjectOptions(projects, "cloud")).toHaveLength(2);
	});
	it("gives same-ID workers distinct row identities", () => {
		const a = { source: local, value: { id: "same" } } as Scoped<DashboardSession>;
		const b = { source: cloud, value: { id: "same" } } as Scoped<DashboardSession>;
		expect(boardWorkerKey(a)).not.toBe(boardWorkerKey(b));
	});
});
