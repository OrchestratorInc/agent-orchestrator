import { describe, expect, it } from "vitest";
import type { DashboardSession, OrchestratorLink } from "../api";
import { conversationProjectLabel } from "./conversationHeaderModel";

const worker = (projectId: string) => ({ id: "w-1", projectId }) as DashboardSession;
const orchestrator = (projectName: string) => ({ id: "o-1", projectId: "repo", projectName }) as OrchestratorLink;
const projects = [{ id: "repo", name: "meetyou" }];

describe("conversationProjectLabel", () => {
	it("names a worker's project", () => {
		expect(conversationProjectLabel(worker("repo"), projects)).toBe("meetyou");
	});

	it("reads Standalone for a worker without a project", () => {
		expect(conversationProjectLabel(worker(""), projects)).toBe("Standalone");
	});

	// Before projects load, or after the project is deleted: the id, not a bare dot.
	it("falls back to the project id when the project does not resolve", () => {
		expect(conversationProjectLabel(worker("gone"), projects)).toBe("gone");
		expect(conversationProjectLabel(worker("repo"), [])).toBe("repo");
	});

	it("names an orchestrator's project, or its id when the name is missing", () => {
		expect(conversationProjectLabel(orchestrator("meetyou"), [])).toBe("meetyou");
		expect(conversationProjectLabel(orchestrator(""), [])).toBe("repo");
	});
});
