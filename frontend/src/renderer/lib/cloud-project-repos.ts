import type { CloudCpAgentStartsIn, CloudCpProject, CloudCpProjectRepo } from "./cloud-cp/types";

export const STANDALONE_REPOSITORY_HOST = "scratch.ao.local";
export const STANDALONE_PROJECT_SOURCE = "standalone";
export const MAX_PROJECT_ADDITIONAL_REPOS = 10;
export const MAX_PROJECT_RULES_BYTES = 16 * 1024;

export const CLOUD_WORKSPACE_LAYOUT = "workspace";
export const MAX_WORKSPACE_REPOSITORIES = MAX_PROJECT_ADDITIONAL_REPOS + 1;

const RESERVED_REPO_DIR_NAMES = ["repository", ".ao"];

type ConfigLike = Record<string, unknown> | null | undefined;

export function isStandaloneCloudProject(project: Pick<CloudCpProject, "repositoryUrl" | "config"> | undefined): boolean {
	if (!project) return false;
	if ((project.config as ConfigLike)?.source === STANDALONE_PROJECT_SOURCE) return true;
	try {
		return new URL(project.repositoryUrl).hostname.toLowerCase() === STANDALONE_REPOSITORY_HOST;
	} catch {
		return false;
	}
}

export function isCloudWorkspaceProject(project: Pick<CloudCpProject, "config"> | undefined): boolean {
	return (project?.config as ConfigLike)?.layout === CLOUD_WORKSPACE_LAYOUT;
}

export function projectRepositoryUrls(project: Pick<CloudCpProject, "repositoryUrl" | "config">): string[] {
	return [project.repositoryUrl, ...projectAdditionalRepos(project.config).map((repo) => repo.url)].filter((url) => url.trim() !== "");
}

export function gitHubRepositoryNameFromFolder(folderName: string): string {
	let name = folderName.trim().replace(/[^A-Za-z0-9._-]+/g, "-").replace(/^[-.]+|-+$/g, "").slice(0, 100);
	while (/[-.]$/.test(name) || /\.git$/i.test(name)) name = name.replace(/\.git$/i, "").replace(/[-.]+$/g, "");
	return name === "" ? "repo" : name;
}

export function isValidGitHubRepositoryName(name: string): boolean {
	return /^[A-Za-z0-9._-]{1,100}$/.test(name) && !name.startsWith(".") && name !== "." && name !== ".." && !/\.git$/i.test(name);
}

export function workspaceRepositoriesProblem(urls: readonly string[]): ReturnType<typeof additionalReposProblem> {
	const [main, ...rest] = urls.filter((url) => url.trim() !== "");
	if (main === undefined) return undefined;
	if (repoDirName(main).startsWith(".")) return { kind: "hidden", url: main, folder: repoDirName(main) };
	if (RESERVED_REPO_DIR_NAMES.includes(repoDirName(main).toLowerCase())) return { kind: "reserved", url: main, folder: repoDirName(main) };
	const problem = additionalReposProblem(main, rest);
	return problem?.kind === "main" ? { kind: "duplicate", url: problem.url } : problem;
}

export function repoDirName(repoUrl: string): string {
	let trimmed = repoUrl.trim().replace(/^\/+|\/+$/g, "");
	if (trimmed.endsWith(".git")) trimmed = trimmed.slice(0, -4);
	const name = (trimmed.split("/").pop() ?? "").replace(/[^a-zA-Z0-9._-]/g, "-");
	return name === "" || name === "." || name === ".." ? "repo" : name;
}

export function gitHubFullName(repoUrl: string): string | undefined {
	try {
		const url = new URL(repoUrl.trim());
		if (url.protocol !== "https:" || !/^(www\.)?github\.com$/i.test(url.hostname)) return undefined;
		const parts = url.pathname.replace(/^\/+|\/+$/g, "").split("/");
		if (parts.length !== 2 || !parts[0] || !parts[1]) return undefined;
		return `${parts[0]}/${parts[1].replace(/\.git$/, "")}`;
	} catch {
		return undefined;
	}
}

export function canonicalGitHubUrl(fullName: string): string {
	return `https://github.com/${fullName}`;
}

export function sameRepository(left: string, right: string): boolean {
	const a = gitHubFullName(left) ?? left.trim();
	const b = gitHubFullName(right) ?? right.trim();
	return a.toLowerCase() === b.toLowerCase();
}

function nonEmptyRepos(value: unknown): CloudCpProjectRepo[] {
	if (!Array.isArray(value)) return [];
	return value.flatMap((item) => {
		if (!item || typeof item !== "object") return [];
		const { url, branch } = item as { url?: unknown; branch?: unknown };
		if (typeof url !== "string" || url.trim() === "") return [];
		return [{ url: url.trim(), ...(typeof branch === "string" && branch.trim() !== "" ? { branch: branch.trim() } : {}) }];
	});
}

export function projectAdditionalRepos(config: ConfigLike): CloudCpProjectRepo[] {
	if (config && Array.isArray(config.repos)) return nonEmptyRepos(config.repos);
	const coder = config?.coder;
	if (coder && typeof coder === "object") return nonEmptyRepos((coder as { extraRepos?: unknown }).extraRepos);
	return [];
}

export function projectAgentStartsIn(config: ConfigLike): CloudCpAgentStartsIn {
	return config?.agentStartsIn === "parent" ? "parent" : "repository";
}

export function projectConfigText(config: ConfigLike, key: "workingDirectory" | "agentRules" | "orchestratorRules"): string {
	const value = config?.[key];
	return typeof value === "string" ? value : "";
}

export function additionalReposProblem(mainUrl: string | undefined, urls: readonly string[]): { kind: "duplicate" | "main" | "folder" | "reserved" | "hidden" | "limit"; url: string; folder?: string } | undefined {
	const filled = urls.filter((url) => url.trim() !== "");
	if (filled.length > MAX_PROJECT_ADDITIONAL_REPOS) return { kind: "limit", url: filled[MAX_PROJECT_ADDITIONAL_REPOS] ?? "" };
	const folders = new Map<string, string>();
	for (const reserved of RESERVED_REPO_DIR_NAMES) folders.set(reserved, "");
	if (mainUrl) folders.set(repoDirName(mainUrl).toLowerCase(), mainUrl);
	const seen = new Set<string>();
	for (const url of filled) {
		const key = (gitHubFullName(url) ?? url).toLowerCase();
		if (mainUrl && sameRepository(url, mainUrl)) return { kind: "main", url };
		if (seen.has(key)) return { kind: "duplicate", url };
		seen.add(key);
		const folder = repoDirName(url);
		if (folder.startsWith(".")) return { kind: "hidden", url, folder };
		const owner = folders.get(folder.toLowerCase());
		if (owner !== undefined) return { kind: owner === "" ? "reserved" : "folder", url, folder };
		folders.set(folder.toLowerCase(), url);
	}
	return undefined;
}

export type ScriptStepDraft = { command: string; folder: string; timeout?: string };

export const DEFAULT_SETUP_STEP_TIMEOUT_MINUTES = 30;
export const DEFAULT_CLOUD_CLEANUP_STEP_TIMEOUT_MINUTES = 10;
export const MIN_SCRIPT_STEP_TIMEOUT_MINUTES = 1;
export const MAX_SCRIPT_STEP_TIMEOUT_MINUTES = 120;

const repoScriptCommandPattern = /^\(\ncd -- '\.\.\/([A-Za-z0-9_.-]+)' \|\| exit 1\n([\s\S]*)\n\)$/;

const timedScriptCommandPattern = /^# ao-timeout: ([0-9]{1,4})m\r?\n/;

export function repoScriptCommand(folder: string, command: string): string {
	return `(\ncd -- '../${folder}' || exit 1\n${command}\n)`;
}

export function parseRepoScriptCommand(stored: string): ScriptStepDraft {
	const match = repoScriptCommandPattern.exec(stored);
	return match ? { folder: match[1] ?? "", command: match[2] ?? "" } : { folder: "", command: stored };
}

export function timedScriptCommand(minutes: number | undefined, command: string): string {
	if (minutes === undefined || minutes <= 0) return command;
	return `# ao-timeout: ${minutes}m\n${command}`;
}

export function parseTimedScriptCommand(stored: string): { minutes?: number; command: string } {
	const match = timedScriptCommandPattern.exec(stored);
	if (!match) return { command: stored };
	return { minutes: Number(match[1]), command: stored.slice(match[0].length) };
}

export function scriptStepTimeoutMinutes(text: string | undefined): number | undefined | null {
	const value = (text ?? "").trim();
	if (value === "") return undefined;
	if (!/^[0-9]+$/.test(value)) return null;
	const minutes = Number(value);
	return minutes >= MIN_SCRIPT_STEP_TIMEOUT_MINUTES && minutes <= MAX_SCRIPT_STEP_TIMEOUT_MINUTES ? minutes : null;
}

export function parseLocalScriptStep(stored: string): ScriptStepDraft {
	const { minutes, command } = parseTimedScriptCommand(stored);
	return { command, folder: "", ...timeoutText(minutes) };
}

export function serializeLocalScriptStep(step: ScriptStepDraft): string {
	return timedScriptCommand(scriptStepTimeoutMinutes(step.timeout) ?? undefined, step.command);
}

function timeoutText(minutes: number | undefined): Pick<ScriptStepDraft, "timeout"> {
	return minutes === undefined ? {} : { timeout: String(minutes) };
}

export function parseScriptSteps(value: unknown, mainUrl?: string): ScriptStepDraft[] | undefined {
	if (!Array.isArray(value)) return undefined;
	const mainFolder = mainUrl ? repoDirName(mainUrl) : undefined;
	const normalize = (step: ScriptStepDraft): ScriptStepDraft => step.folder === mainFolder ? { ...step, folder: "" } : step;
	return value.flatMap((item): ScriptStepDraft[] => {
		if (typeof item === "string") {
			const { minutes, command } = parseTimedScriptCommand(item);
			return [normalize({ ...parseRepoScriptCommand(command), ...timeoutText(minutes) })];
		}
		if (item && typeof item === "object" && typeof (item as { command?: unknown }).command === "string") {
			const { command, repository, timeoutMinutes } = item as { command: string; repository?: unknown; timeoutMinutes?: unknown };
			const folder = typeof repository === "string" && repository.trim() !== "" ? repoDirName(repository) : "";
			return [normalize({ command, folder, ...timeoutText(typeof timeoutMinutes === "number" ? timeoutMinutes : undefined) })];
		}
		return [];
	});
}

export function serializeScriptSteps(steps: readonly ScriptStepDraft[]): string[] {
	return steps
		.filter((step) => step.command.trim() !== "")
		.map((step) => timedScriptCommand(
			scriptStepTimeoutMinutes(step.timeout) ?? undefined,
			step.folder === "" ? step.command : repoScriptCommand(step.folder, step.command),
		));
}

export function scriptStepFolders(config: ConfigLike, mainUrl?: string): Set<string> {
	const folders = new Set<string>();
	for (const key of ["postCreate", "preRemove"] as const) {
		for (const step of parseScriptSteps(config?.[key], mainUrl) ?? []) {
			if (step.folder !== "") folders.add(step.folder);
		}
	}
	return folders;
}

export const MAX_WORKING_DIRECTORY_LENGTH = 512;

export function workingDirectoryProblem(value: string, options: { standalone?: boolean } = {}): "length" | "characters" | "form" | "segments" | "aoState" | "placeholder" | "standaloneRepo" | undefined {
	const directory = value.trim();
	if (directory === "") return undefined;
	if ([...directory].length > MAX_WORKING_DIRECTORY_LENGTH) return "length";
	// eslint-disable-next-line no-control-regex
	if (/[\u0000-\u001f\u007f]/.test(directory)) return "characters";
	const prefix = ["~/", "$HOME/", "/"].find((candidate) => directory.startsWith(candidate));
	const rest = prefix === undefined ? undefined : directory.slice(prefix.length);
	if (rest === undefined || rest.replace(/^\/+|\/+$/g, "") === "") return "form";
	if (rest.split("/").some((segment) => segment === "." || segment === "..")) return "segments";
	if (rest.split("/").includes(".ao")) return "aoState";
	if (/[{}$`\\]/.test(rest.split("{repo}").join(""))) return "placeholder";
	if (options.standalone && rest.includes("{repo}")) return "standaloneRepo";
	return undefined;
}

export function normalizeWorkingDirectory(value: string): string {
	const directory = value.trim();
	const prefix = ["~/", "$HOME/", "/"].find((candidate) => directory.startsWith(candidate));
	if (prefix === undefined) return directory;
	const segments = directory.slice(prefix.length).split("/").filter((segment) => segment !== "");
	return segments.length === 0 ? directory : prefix + segments.join("/");
}

export function utf8Bytes(text: string): number {
	return new TextEncoder().encode(text).length;
}
