import { describe, expect, it } from "vitest";
import {
	additionalReposProblem,
	isStandaloneCloudProject,
	parseRepoScriptCommand,
	parseScriptSteps,
	projectAdditionalRepos,
	projectAgentStartsIn,
	repoDirName,
	repoScriptCommand,
	scriptStepFolders,
	serializeScriptSteps,
	workingDirectoryProblem,
} from "./cloud-project-repos";

const main = "https://github.com/acme/alice";

describe("cloud project repositories", () => {
	it("reads config.repos, falling back to the legacy coder extraRepos", () => {
		expect(projectAdditionalRepos({ repos: [{ url: "https://github.com/acme/11x" }, { url: " " }] })).toEqual([{ url: "https://github.com/acme/11x" }]);
		expect(projectAdditionalRepos({ coder: { extraRepos: [{ url: "https://github.com/acme/old", branch: "dev" }] } })).toEqual([{ url: "https://github.com/acme/old", branch: "dev" }]);
		// An explicit empty list wins over the legacy one.
		expect(projectAdditionalRepos({ repos: [], coder: { extraRepos: [{ url: "https://github.com/acme/old" }] } })).toEqual([]);
		expect(projectAdditionalRepos(undefined)).toEqual([]);
	});

	it("names folders like the control plane", () => {
		expect(repoDirName("https://github.com/acme/ChartSnip")).toBe("ChartSnip");
		expect(repoDirName("https://github.com/acme/app.git")).toBe("app");
		expect(repoDirName("https://github.com/acme/")).toBe("acme");
	});

	it("finds the first problem with a set of additional repositories", () => {
		expect(additionalReposProblem(main, ["https://github.com/acme/11x", ""])).toBeUndefined();
		expect(additionalReposProblem(main, ["https://github.com/ACME/alice"])?.kind).toBe("main");
		expect(additionalReposProblem(main, ["https://github.com/acme/11x", "https://github.com/acme/11x"])?.kind).toBe("duplicate");
		expect(additionalReposProblem(main, ["https://github.com/other/alice"])).toMatchObject({ kind: "folder", folder: "alice" });
		expect(additionalReposProblem(main, ["https://github.com/acme/repository"])?.kind).toBe("reserved");
		expect(additionalReposProblem(main, ["https://github.com/acme/.aws"])?.kind).toBe("hidden");
		expect(additionalReposProblem(main, Array.from({ length: 11 }, (_, i) => `https://github.com/acme/r${i}`))?.kind).toBe("limit");
	});

	it("round-trips Setup steps as plain strings, wrapping a step in another repository", () => {
		const wrapped = "(\ncd -- '../11x' || exit 1\nmake up\n)";
		expect(repoScriptCommand("11x", "make up")).toBe(wrapped);
		const steps = parseScriptSteps(["npm ci", wrapped, { command: "make db", repository: "https://github.com/acme/voice-calling-ai.git" }, 5], main);
		expect(steps).toEqual([
			{ command: "npm ci", folder: "" },
			{ command: "make up", folder: "11x" },
			{ command: "make db", folder: "voice-calling-ai" },
		]);
		expect(serializeScriptSteps(steps ?? [])).toEqual(["npm ci", wrapped, "(\ncd -- '../voice-calling-ai' || exit 1\nmake db\n)"]);
		// A multi-line command survives the round trip, and the main repository's own folder is the main repository.
		expect(parseRepoScriptCommand(repoScriptCommand("11x", "a\n\nb)\n"))).toEqual({ folder: "11x", command: "a\n\nb)\n" });
		expect(parseScriptSteps([repoScriptCommand("alice", "ls")], main)).toEqual([{ command: "ls", folder: "" }]);
		expect(scriptStepFolders({ postCreate: [wrapped], preRemove: ["ls"] }, main)).toEqual(new Set(["11x"]));
	});

	it("rejects working folders the control plane rejects", () => {
		for (const ok of ["", "  ", "~/work/{repo}", "$HOME/work", "/srv/work/{repo}"]) expect(workingDirectoryProblem(ok)).toBeUndefined();
		expect(workingDirectoryProblem("~")).toBe("form");
		expect(workingDirectoryProblem("$HOME")).toBe("form");
		expect(workingDirectoryProblem("/")).toBe("form");
		expect(workingDirectoryProblem("work")).toBe("form");
		expect(workingDirectoryProblem("~/work/../x")).toBe("segments");
		expect(workingDirectoryProblem("~/work/{name}")).toBe("placeholder");
		expect(workingDirectoryProblem("~/work/$USER")).toBe("placeholder");
		expect(workingDirectoryProblem("~/a\tb")).toBe("characters");
		expect(workingDirectoryProblem(`/${"a".repeat(512)}`)).toBe("length");
	});

	it("recognizes standalone projects and where the agent starts", () => {
		expect(isStandaloneCloudProject({ repositoryUrl: "https://scratch.ao.local/p", config: {} })).toBe(true);
		expect(isStandaloneCloudProject({ repositoryUrl: "https://github.com/acme/alice", config: { source: "standalone" } })).toBe(true);
		expect(isStandaloneCloudProject({ repositoryUrl: "https://github.com/acme/alice", config: {} })).toBe(false);
		expect(projectAgentStartsIn({ agentStartsIn: "parent" })).toBe("parent");
		expect(projectAgentStartsIn({ agentStartsIn: "elsewhere" })).toBe("repository");
	});
});
