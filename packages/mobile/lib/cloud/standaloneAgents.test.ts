import { describe, expect, it } from "vitest";
import { mergeCloudProjectLists, standaloneAgentsOffered, upsertCloudProject } from "./standaloneAgents";
import type { CloudOffering, CloudProject } from "./types";

const project = (id: string, extra: Partial<CloudProject> = {}) => ({ id, displayName: id, ...extra }) as CloudProject;
const offering = (features: string[]) => ({ features }) as CloudOffering;

describe("Cloud standalone agents", () => {
	it("are offered only where the control plane lists the feature", () => {
		expect(standaloneAgentsOffered(offering(["share", "standalone_agents"]))).toBe(true);
		expect(standaloneAgentsOffered(offering(["share"]))).toBe(false);
		expect(standaloneAgentsOffered(undefined)).toBe(false);
		expect(standaloneAgentsOffered({} as CloudOffering)).toBe(false);
	});

	it("join the projects once each, after them", () => {
		const agent = project("a1", { standaloneAgent: true });
		expect(mergeCloudProjectLists([project("p1")], [agent]).map((item) => item.id)).toEqual(["p1", "a1"]);
		// A control plane without agents lists every project in both reads.
		expect(mergeCloudProjectLists([project("p1"), project("p2")], [project("p1"), project("p2")]).map((item) => item.id)).toEqual(["p1", "p2"]);
	});

	it("take a just-created project into the list, replacing one it already holds", () => {
		const listed = [project("p1")];
		expect(upsertCloudProject(listed, project("a1")).map((item) => item.id)).toEqual(["p1", "a1"]);
		expect(upsertCloudProject(listed, project("p1", { displayName: "renamed" }))).toEqual([project("p1", { displayName: "renamed" })]);
	});
});
