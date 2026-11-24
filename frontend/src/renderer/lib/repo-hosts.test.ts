import { describe, expect, it } from "vitest";
import { LOCAL_HOST } from "./hosts";
import { hostsWithRepo, projectsWithRepo } from "./repo-hosts";
import type { WorkspaceSummary } from "../types/workspace";

const project = (id: string, repoKey: string, hostId?: string) => ({ id, name: id, repoKey, hostId, sessions: [] }) as unknown as WorkspaceSummary;

describe("projectsWithRepo", () => {
	const local = [project("web", "github.com/o/web"), project("api", "github.com/o/api")];
	const remote = [project("web", "github.com/o/web", "h-b"), project("web-2", "github.com/o/web", "h-b"), project("web", "github.com/o/web", "h-c")];

	it("lists every project for the repository, this computer first", () => {
		expect(projectsWithRepo(local, remote, "github.com/o/web").map((p) => `${p.hostId}:${p.projectId}`)).toEqual([
			`${LOCAL_HOST}:web`,
			"h-b:web",
			"h-b:web-2",
			"h-c:web",
		]);
	});

	it("hostsWithRepo keeps the first project per computer", () => {
		expect(hostsWithRepo(local, remote, "github.com/o/web").map((p) => `${p.hostId}:${p.projectId}`)).toEqual([`${LOCAL_HOST}:web`, "h-b:web", "h-c:web"]);
	});

	it("finds nothing without a repository key", () => {
		expect(projectsWithRepo(local, remote, undefined)).toEqual([]);
	});
});
