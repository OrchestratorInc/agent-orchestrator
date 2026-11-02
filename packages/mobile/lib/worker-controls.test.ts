import { describe, expect, it } from "vitest";
import {
	ALL_WORKER_ENVIRONMENTS,
	ALL_WORKER_PROJECTS,
	filterWorkersByEnvironmentAndProject,
	filterWorkersByProject,
	spawnProjectParam,
	workerEnvironmentOptions,
	workerProjectForEnvironment,
	workerProjectLabel,
	workerProjectOptions,
	workerSearchPresentation,
	chipRevealOffset,
	showsEnvironmentFilter,
	sortWorkerEnvironments,
} from "./worker-controls";
import { hostedProjectKey } from "./hostedRows";

describe("filterWorkersByProject", () => {
	it("keeps every worker when All projects is selected", () => {
		const workers = [{ projectId: "alpha" }, { projectId: "beta" }];
		expect(filterWorkersByProject(workers, "all")).toEqual(workers);
	});

	it("keeps only workers from the locally selected project", () => {
		const workers = [{ projectId: "alpha" }, { projectId: "beta" }, { projectId: "alpha" }];
		expect(filterWorkersByProject(workers, "beta")).toEqual([{ projectId: "beta" }]);
	});

	it("does not include another machine's workers when project IDs match", () => {
		const projects = [
			{ id: "same", name: "App", hostId: "a", hostName: "Laptop" },
			{ id: "same", name: "App", hostId: "b", hostName: "Desktop" },
		];
		const workers = [{ projectId: "same", hostId: "a" }, { projectId: "same", hostId: "b" }];
		expect(filterWorkersByProject(workers, hostedProjectKey(projects[1]), projects)).toEqual([workers[1]]);
		expect(spawnProjectParam(hostedProjectKey(projects[1]), projects)).toEqual({ projectId: "same", hostId: "b" });
		expect(workerProjectOptions(projects).map((item) => item.label)).toEqual(["All projects", "App · Laptop", "App · Desktop"]);
	});
});

describe("environment worker filtering", () => {
	const projects = [
		{ id: "same", name: "App", hostId: "mac", hostName: "Studio Mac" },
		{ id: "same", name: "App", hostId: "dev", hostName: "Dev" },
	];
	const workers = [
		{ id: "mac-worker", projectId: "same", hostId: "mac" },
		{ id: "dev-worker", projectId: "same", hostId: "dev" },
		{ id: "dev-other", projectId: "other", hostId: "dev" },
	];

	it("keeps only workers from the selected machine before applying the project filter", () => {
		expect(filterWorkersByEnvironmentAndProject(workers, "dev", ALL_WORKER_PROJECTS, projects)).toEqual([
			workers[1],
			workers[2],
		]);
		expect(filterWorkersByEnvironmentAndProject(workers, "dev", hostedProjectKey(projects[1]), projects)).toEqual([
			workers[1],
		]);
	});

	it("builds human-readable environment choices and scopes project choices to the selected machine", () => {
		expect(workerEnvironmentOptions([
			{ id: "mac", name: "Studio Mac" },
			{ id: "dev", name: "Dev" },
		])).toEqual([
			{ id: ALL_WORKER_ENVIRONMENTS, label: "All environments" },
			{ id: "mac", label: "Studio Mac" },
			{ id: "dev", label: "Dev" },
		]);
		expect(workerProjectOptions(projects, "dev")).toEqual([
			{ id: ALL_WORKER_PROJECTS, label: "All projects" },
			{ id: hostedProjectKey(projects[1]), label: "App" },
		]);
	});

	it("resolves an incompatible project in the same environment selection", () => {
		expect(workerProjectForEnvironment(projects, "dev", hostedProjectKey(projects[0]))).toBe(ALL_WORKER_PROJECTS);
		expect(workerProjectForEnvironment(projects, "dev", hostedProjectKey(projects[1]))).toBe(hostedProjectKey(projects[1]));
		expect(workerProjectForEnvironment(projects, "dev", ALL_WORKER_PROJECTS)).toBe(ALL_WORKER_PROJECTS);
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

describe("sortWorkerEnvironments", () => {
	it("orders machines by name regardless of case, then by id", () => {
		const sorted = sortWorkerEnvironments([
			{ id: "h4", name: "Work laptop" },
			{ id: "h2", name: "macBook Pro" },
			{ id: "h3", name: "Linux box" },
			{ id: "h1", name: "Linux box" },
		]);
		expect(sorted.map((environment) => environment.id)).toEqual(["h1", "h3", "h2", "h4"]);
	});
});

describe("showsEnvironmentFilter", () => {
	it("needs at least two machines", () => {
		expect(showsEnvironmentFilter([])).toBe(false);
		expect(showsEnvironmentFilter([{ id: "h1", name: "Studio" }])).toBe(false);
		expect(showsEnvironmentFilter([{ id: "h1", name: "Studio" }, { id: "h2", name: "Laptop" }])).toBe(true);
	});
});

describe("chipRevealOffset", () => {
	const view = { offset: 100, width: 300, inset: 16 };
	it("leaves a fully visible chip alone", () => {
		expect(chipRevealOffset({ x: 150, width: 40 }, view)).toBeNull();
	});
	it("scrolls back so a chip cut off on the left shows with its inset", () => {
		expect(chipRevealOffset({ x: 90, width: 40 }, view)).toBe(74);
		expect(chipRevealOffset({ x: 10, width: 40 }, view)).toBe(0);
	});
	it("scrolls forward so a chip cut off on the right ends one inset from the edge", () => {
		expect(chipRevealOffset({ x: 380, width: 60 }, view)).toBe(156);
	});
});
