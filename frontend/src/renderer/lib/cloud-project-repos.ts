// A cloud project's repositories and layout, read from its config the way the
// control plane does (cloud/internal/domain/project_repos.go):
// - the main repository is the project's repositoryUrl;
// - additional repositories are config.repos, or the legacy coder dev kit's
//   config.coder.extraRepos when config.repos is absent;
// - every session clones them side by side, each additional one into the
//   folder repoDirName gives it beside the main checkout;
// - a standalone project has no repository (a scratch.ao.local placeholder).

import type { CloudCpAgentStartsIn, CloudCpProject, CloudCpProjectRepo } from "./cloud-cp/types";

export const STANDALONE_REPOSITORY_HOST = "scratch.ao.local";
export const STANDALONE_PROJECT_SOURCE = "standalone";
export const MAX_PROJECT_ADDITIONAL_REPOS = 10;
export const MAX_PROJECT_RULES_BYTES = 16 * 1024;

/** Folders beside the main checkout that AO itself uses. */
const RESERVED_REPO_DIR_NAMES = ["repository", ".ao"];

type ConfigLike = Record<string, unknown> | null | undefined;

/** True for a project created without a repository. */
export function isStandaloneCloudProject(project: Pick<CloudCpProject, "repositoryUrl" | "config"> | undefined): boolean {
	if (!project) return false;
	if ((project.config as ConfigLike)?.source === STANDALONE_PROJECT_SOURCE) return true;
	try {
		return new URL(project.repositoryUrl).hostname.toLowerCase() === STANDALONE_REPOSITORY_HOST;
	} catch {
		return false;
	}
}

/**
 * The folder a repository checks out into beside the main repository:
 * "https://github.com/owner/ChartSnip" gives "ChartSnip". Mirrors the control
 * plane's domain.RepoDirName.
 */
export function repoDirName(repoUrl: string): string {
	let trimmed = repoUrl.trim().replace(/^\/+|\/+$/g, "");
	if (trimmed.endsWith(".git")) trimmed = trimmed.slice(0, -4);
	const name = (trimmed.split("/").pop() ?? "").replace(/[^a-zA-Z0-9._-]/g, "-");
	return name === "" || name === "." || name === ".." ? "repo" : name;
}

/** owner/name of a GitHub repository URL, or undefined when it is not one. */
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

/** https://github.com/<owner>/<name>, the form the control plane stores. */
export function canonicalGitHubUrl(fullName: string): string {
	return `https://github.com/${fullName}`;
}

/** Whether two repository URLs name the same GitHub repository. */
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

/** The project's additional repositories (config.repos, else the legacy config.coder.extraRepos). */
export function projectAdditionalRepos(config: ConfigLike): CloudCpProjectRepo[] {
	if (config && Array.isArray(config.repos)) return nonEmptyRepos(config.repos);
	const coder = config?.coder;
	if (coder && typeof coder === "object") return nonEmptyRepos((coder as { extraRepos?: unknown }).extraRepos);
	return [];
}

/** Where the project's agent starts; "repository" (the main checkout) unless set to "parent". */
export function projectAgentStartsIn(config: ConfigLike): CloudCpAgentStartsIn {
	return config?.agentStartsIn === "parent" ? "parent" : "repository";
}

/** A string config value, trimmed; "" when absent. */
export function projectConfigText(config: ConfigLike, key: "workingDirectory" | "agentRules" | "orchestratorRules"): string {
	const value = config?.[key];
	return typeof value === "string" ? value : "";
}

/**
 * Checks a set of additional repositories against the main one the way the
 * control plane's NormalizeProjectRepos does: no duplicates, not the main
 * repository, and each in its own folder. Returns the first problem, if any.
 */
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
		// A folder starting with "." would land among dotfiles such as ~/.ssh.
		if (folder.startsWith(".")) return { kind: "hidden", url, folder };
		const owner = folders.get(folder.toLowerCase());
		if (owner !== undefined) return { kind: owner === "" ? "reserved" : "folder", url, folder };
		folders.set(folder.toLowerCase(), url);
	}
	return undefined;
}

/**
 * One Setup or Cleanup step as the editor holds it. folder is the repository
 * folder the step runs in ("" for the main repository), as repoDirName gives it.
 * timeout is the step's own time limit in minutes as typed ("" or absent for
 * the default); see scriptStepTimeoutMinutes.
 */
export type ScriptStepDraft = { command: string; folder: string; timeout?: string };

/**
 * A step's time limit, in whole minutes, mirroring the control plane's
 * domain.ScriptStepTimeout bounds and defaults: a Setup step without its own
 * limit runs for 30 minutes (local and Cloud), a Cloud Cleanup step for 10. A
 * local Cleanup step without one has no limit.
 */
export const DEFAULT_SETUP_STEP_TIMEOUT_MINUTES = 30;
export const DEFAULT_CLOUD_CLEANUP_STEP_TIMEOUT_MINUTES = 10;
export const MIN_SCRIPT_STEP_TIMEOUT_MINUTES = 1;
export const MAX_SCRIPT_STEP_TIMEOUT_MINUTES = 120;

// The stored form of a step in another repository, written by the control
// plane's domain.RepoScriptCommand: the command run in a subshell from that
// repository's folder beside the main checkout. Keeping a plain string means a
// control plane or desktop release without per-repository steps still runs
// and keeps the step.
const repoScriptCommandPattern = /^\(\ncd -- '\.\.\/([A-Za-z0-9_.-]+)' \|\| exit 1\n([\s\S]*)\n\)$/;

// The stored form of a step with its own time limit (domain.TimedScriptCommand,
// in the control plane and the local daemon): a first line that is a shell
// comment, so a release without per-step limits keeps and runs the step.
const timedScriptCommandPattern = /^# ao-timeout: ([0-9]{1,4})m\r?\n/;

/** The stored command for a step that runs in folder beside the main checkout. */
export function repoScriptCommand(folder: string, command: string): string {
	return `(\ncd -- '../${folder}' || exit 1\n${command}\n)`;
}

/** Splits a stored step into its folder ("" for the main repository) and command. */
export function parseRepoScriptCommand(stored: string): ScriptStepDraft {
	const match = repoScriptCommandPattern.exec(stored);
	return match ? { folder: match[1] ?? "", command: match[2] ?? "" } : { folder: "", command: stored };
}

/**
 * The stored step with its own time limit; one without (undefined) is the
 * command as is and runs with its kind's default. A typed limit equal to a
 * default is kept: Setup and Cleanup have different defaults, and the limit
 * stays the step's own if a default changes.
 */
export function timedScriptCommand(minutes: number | undefined, command: string): string {
	if (minutes === undefined || minutes <= 0) return command;
	return `# ao-timeout: ${minutes}m\n${command}`;
}

/** Splits a stored step into its time limit in minutes (undefined when it sets none) and the rest. */
export function parseTimedScriptCommand(stored: string): { minutes?: number; command: string } {
	const match = timedScriptCommandPattern.exec(stored);
	if (!match) return { command: stored };
	return { minutes: Number(match[1]), command: stored.slice(match[0].length) };
}

/**
 * A typed time limit as whole minutes: undefined for "" (the default), null
 * when it is not a whole number from MIN to MAX.
 */
export function scriptStepTimeoutMinutes(text: string | undefined): number | undefined | null {
	const value = (text ?? "").trim();
	if (value === "") return undefined;
	if (!/^[0-9]+$/.test(value)) return null;
	const minutes = Number(value);
	return minutes >= MIN_SCRIPT_STEP_TIMEOUT_MINUTES && minutes <= MAX_SCRIPT_STEP_TIMEOUT_MINUTES ? minutes : null;
}

/**
 * A stored step as the editor holds it, without its repository folder (a local
 * step). Every limit it sets shows, a default's value too.
 */
export function parseLocalScriptStep(stored: string): ScriptStepDraft {
	const { minutes, command } = parseTimedScriptCommand(stored);
	return { command, folder: "", ...timeoutText(minutes) };
}

/**
 * The stored form of a local step: its command with the time limit typed for
 * it, a default's value included (a local Cleanup step without one runs
 * without a limit).
 */
export function serializeLocalScriptStep(step: ScriptStepDraft): string {
	return timedScriptCommand(scriptStepTimeoutMinutes(step.timeout) ?? undefined, step.command);
}

/** A draft's time limit for a stored one: none when the step sets none. */
function timeoutText(minutes: number | undefined): Pick<ScriptStepDraft, "timeout"> {
	return minutes === undefined ? {} : { timeout: String(minutes) };
}

/**
 * Reads stored steps. Each is a plain command, the per-repository form above,
 * either led by its own time limit, or (from an earlier build of this feature)
 * {command, repository, timeoutMinutes}. A step in the main repository's own
 * folder counts as a main repository step.
 */
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

/**
 * The stored form of the steps: a step in the main repository stays the plain
 * command, any other is wrapped to run from its folder, and a step with its own
 * time limit is led by it. Blank commands are dropped.
 */
export function serializeScriptSteps(steps: readonly ScriptStepDraft[]): string[] {
	return steps
		.filter((step) => step.command.trim() !== "")
		.map((step) => timedScriptCommand(
			scriptStepTimeoutMinutes(step.timeout) ?? undefined,
			step.folder === "" ? step.command : repoScriptCommand(step.folder, step.command),
		));
}

/** The folders the stored Setup and Cleanup steps run in, other than the main repository's. */
export function scriptStepFolders(config: ConfigLike, mainUrl?: string): Set<string> {
	const folders = new Set<string>();
	for (const key of ["postCreate", "preRemove"] as const) {
		for (const step of parseScriptSteps(config?.[key], mainUrl) ?? []) {
			if (step.folder !== "") folders.add(step.folder);
		}
	}
	return folders;
}

/** Mirrors the control plane's limit on a working directory's length. */
export const MAX_WORKING_DIRECTORY_LENGTH = 512;

/**
 * Why the control plane would reject a working directory, mirroring
 * domain.ValidateCoderWorkingDirectory on the trimmed value; undefined when it
 * is accepted ("" keeps the connection's).
 */
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
	// AO's own state (~/.ao, or the durable root's .ao) holds the worker's token.
	if (rest.split("/").includes(".ao")) return "aoState";
	if (/[{}$`\\]/.test(rest.split("{repo}").join(""))) return "placeholder";
	// A standalone project has no repository to name the folder after.
	if (options.standalone && rest.includes("{repo}")) return "standaloneRepo";
	return undefined;
}

/**
 * A working folder as the control plane stores it
 * (domain.NormalizeCoderWorkingDirectory): trimmed, without repeated or
 * trailing slashes after its ~/, $HOME/ or / prefix ("/work/{repo}/" becomes
 * "/work/{repo}"). Anything else comes back trimmed.
 */
export function normalizeWorkingDirectory(value: string): string {
	const directory = value.trim();
	const prefix = ["~/", "$HOME/", "/"].find((candidate) => directory.startsWith(candidate));
	if (prefix === undefined) return directory;
	const segments = directory.slice(prefix.length).split("/").filter((segment) => segment !== "");
	return segments.length === 0 ? directory : prefix + segments.join("/");
}

/** UTF-8 byte length, for the 16 KB rules limit. */
export function utf8Bytes(text: string): number {
	return new TextEncoder().encode(text).length;
}
