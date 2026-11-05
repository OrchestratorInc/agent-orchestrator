// Uploads a local folder into an empty GitHub repository AO Cloud created for
// it (POST /orgs/{orgId}/github/repositories), so a cloud workspace can hold
// folders from this machine. The control plane answers that request with a
// push path on itself: a push-only git smart HTTP endpoint that takes the
// WorkOS bearer token and only accepts pushes that create branches.
//
// The person's folder is never changed. A folder that is its own git
// repository with a commit pushes its checked-out commit. Any other folder is
// imported: a commit is made in a private git directory under the app's own
// state, with the folder as its work tree (so its .gitignore files apply),
// pushed, and the git directory removed.
//
// The token goes only to the configured control plane: the push path is joined
// with the configured control plane here, never with a URL the renderer names,
// and the token travels to git in its
// environment (never on the command line), scoped to that URL, with redirects
// off and every credential prompt disabled.

import { execFile } from "node:child_process";
import { randomUUID } from "node:crypto";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import type { ControlPlaneUrlResolver } from "./cloud-control-plane-url";
import { parseCloudBaseUrl } from "./cloud-cp-proxy";

export const CLOUD_PUSH_FOLDER_CHANNEL = "cloud:pushFolderToRepository";

/** An import larger than this is refused before anything is written. */
export const MAX_FOLDER_IMPORT_BYTES = 1.5 * 1024 * 1024 * 1024;

/** The commit identity of an import when git has none configured. */
export const FALLBACK_COMMIT_NAME = "AO";
export const FALLBACK_COMMIT_EMAIL = "noreply@aoagents.dev";

const GIT_STEP_TIMEOUT_MS = 10 * 60_000;
const GIT_PUSH_TIMEOUT_MS = 30 * 60_000;
const GIT_MAX_BUFFER = 64 * 1024 * 1024;

export interface PushFolderInput {
	/** The local folder, an absolute path. */
	folder: string;
	/** The repository's push path from the control plane, relative to it (pushPath). */
	pushPath: string;
	/** The branch to create; the repository's default branch. */
	branch: string;
}

export type PushFolderErrorCode =
	| "invalid_request"
	| "signed_out"
	| "folder_unavailable"
	| "folder_too_large"
	| "folder_has_repositories"
	| "git_unavailable"
	| "import_failed"
	| "push_failed"
	| "push_forbidden"
	| "branch_exists";

export type PushFolderResult =
	| { status: "pushed"; branch: string; commit: string; source: "head" | "import" }
	| { status: "error"; code: PushFolderErrorCode; message: string };

export interface GitRunOptions {
	cwd?: string;
	env: NodeJS.ProcessEnv;
	timeout: number;
}

export type GitRunner = (args: string[], options: GitRunOptions) => Promise<{ stdout: string; stderr: string }>;

export interface PushFolderDeps {
	/** The configured control plane, the only server the token is sent to. */
	controlPlaneUrl: ControlPlaneUrlResolver;
	getToken: () => Promise<string | null>;
	/** Where import git directories are made; under ~/.ao. */
	tmpRoot: () => string;
	/** The environment git runs with (the user's shell PATH). */
	env?: () => NodeJS.ProcessEnv;
	homeDir?: string;
	git?: GitRunner;
	/**
	 * The remote git pushes to for the resolved push URL. Tests point it at a
	 * local bare repository; the app always pushes to the URL itself.
	 */
	remoteFor?: (pushUrl: string) => string;
	maxImportBytes?: number;
}

class PushFolderError extends Error {
	constructor(readonly code: PushFolderErrorCode, message: string) {
		super(message);
	}
}

const defaultGit: GitRunner = (args, options) =>
	new Promise((resolve, reject) => {
		execFile("git", args, { cwd: options.cwd, env: options.env, timeout: options.timeout, maxBuffer: GIT_MAX_BUFFER, encoding: "utf8" }, (error, stdout, stderr) => {
			if (error) {
				reject(Object.assign(error, { stdout, stderr }));
				return;
			}
			resolve({ stdout, stderr });
		});
	});

/** A branch name git and the control plane both take. */
export function isValidPushBranch(branch: string): boolean {
	if (branch === "" || branch.length > 255) return false;
	if (!/^[A-Za-z0-9._/-]+$/.test(branch)) return false;
	if (branch.startsWith("-") || branch.startsWith("/") || branch.endsWith("/") || branch.endsWith(".") || branch.endsWith(".lock")) return false;
	return !branch.split("/").some((part) => part === "" || part.startsWith("."));
}

const PUSH_PATH = /^\/api\/cloud\/v1\/orgs\/[A-Za-z0-9_-]+\/github\/git\/[A-Za-z0-9-]+\/[A-Za-z0-9_.-]+\.git$/;

/**
 * The URL git pushes to: the repository's push path (from the control plane,
 * relative to it) on the configured control plane, resolved the way the CP
 * proxy resolves API paths. A path that is not a repository's git endpoint,
 * or that would leave the control plane once resolved, throws.
 */
export function resolvePushUrl(pushPath: string, controlPlane: string): URL {
	if (!PUSH_PATH.test(pushPath) || pushPath.split("/").some((segment) => segment === "." || segment === "..")) {
		throw new PushFolderError("invalid_request", "The repository's push address is not an AO Cloud repository.");
	}
	const base = parseCloudBaseUrl(controlPlane);
	const basePath = base.pathname.replace(/\/+$/, "");
	const url = new URL(base.origin + basePath + pushPath);
	if (url.origin !== base.origin || url.pathname !== basePath + pushPath) {
		throw new PushFolderError("invalid_request", "The repository's push address is not on the AO Cloud control plane.");
	}
	return url;
}

function gitMessage(error: unknown): string {
	const stderr = typeof (error as { stderr?: unknown })?.stderr === "string" ? (error as { stderr: string }).stderr : "";
	const lines = stderr.split(/\r?\n/).map((line) => line.trim()).filter((line) => line !== "" && !line.startsWith("hint:"));
	if (lines.length > 0) return lines.slice(-4).join("\n");
	if ((error as { killed?: boolean })?.killed) return "git took too long and was stopped.";
	return error instanceof Error ? error.message : String(error);
}

/**
 * What a failed push means. The control plane answers a refused request with
 * an HTTP status git reports as "returned error: N", and refuses updating a
 * branch that exists in git's own push report, saying it "already exists": it
 * only lets a folder into an empty repository.
 */
function pushRefusal(error: unknown, folderName: string, branch: string): PushFolderError {
	const message = gitMessage(error);
	const status = /returned error: (\d{3})/.exec(message)?.[1];
	if (status === "401") return new PushFolderError("signed_out", "Your AO Cloud session expired. Sign in again, then try again.");
	if (status === "403" || status === "404") {
		return new PushFolderError("push_forbidden", `AO Cloud did not let you upload ${folderName} to its repository. Check that the AO GitHub App still has access to it, then try again.`);
	}
	if (/already exists/i.test(message)) {
		return new PushFolderError("branch_exists", `The repository for ${folderName} already has a ${branch} branch, and AO only uploads a folder into an empty repository. If an earlier upload finished, remove ${folderName} and add its repository from GitHub instead.`);
	}
	return new PushFolderError("push_failed", `Could not upload ${folderName}: ${message}`);
}

function redact(message: string, token: string | null): string {
	return token ? message.split(token).join("***") : message;
}

/** Whether `git --version` output names a git that reads config from GIT_CONFIG_COUNT (2.31+). */
export function gitSupportsConfigEnv(versionOutput: string): boolean {
	const match = /git version (\d+)\.(\d+)/.exec(versionOutput);
	if (!match) return false;
	const [major, minor] = [Number(match[1]), Number(match[2])];
	return major > 2 || (major === 2 && minor >= 31);
}

/** The environment every git step runs with: no prompts, no system or user credential helpers asking. */
function quietGitEnv(base: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
	const env: NodeJS.ProcessEnv = { ...base, GIT_TERMINAL_PROMPT: "0", GIT_ASKPASS: "", SSH_ASKPASS: "", GCM_INTERACTIVE: "never" };
	// A caller's own config-by-environment would shift the indexes below.
	for (const key of Object.keys(env)) {
		if (key === "GIT_CONFIG_COUNT" || key.startsWith("GIT_CONFIG_KEY_") || key.startsWith("GIT_CONFIG_VALUE_")) delete env[key];
	}
	delete env.GIT_DIR;
	delete env.GIT_WORK_TREE;
	delete env.GIT_INDEX_FILE;
	return env;
}

/** git config passed through the environment, which unlike -c stays out of the process list. */
function withGitConfig(env: NodeJS.ProcessEnv, entries: Array<[string, string]>): NodeJS.ProcessEnv {
	const next: NodeJS.ProcessEnv = { ...env, GIT_CONFIG_COUNT: String(entries.length) };
	entries.forEach(([key, value], index) => {
		next[`GIT_CONFIG_KEY_${index}`] = key;
		next[`GIT_CONFIG_VALUE_${index}`] = value;
	});
	return next;
}

function samePath(a: string, b: string): boolean {
	return process.platform === "win32" || process.platform === "darwin" ? a.toLowerCase() === b.toLowerCase() : a === b;
}

/** The folder, resolved, when it is a directory that may be uploaded. */
async function resolveFolder(folder: string, homeDir: string | undefined): Promise<string> {
	if (!path.isAbsolute(folder)) throw new PushFolderError("invalid_request", "The folder must be an absolute path.");
	let real: string;
	try {
		real = await fs.realpath(folder);
		if (!(await fs.stat(real)).isDirectory()) throw new Error("not a directory");
	} catch {
		throw new PushFolderError("folder_unavailable", `${folder} is not a folder this app can read.`);
	}
	if (path.parse(real).root === real) throw new PushFolderError("folder_unavailable", "A whole drive cannot be uploaded. Choose a project folder.");
	if (homeDir) {
		const home = await fs.realpath(homeDir).catch(() => homeDir);
		if (samePath(real, home)) throw new PushFolderError("folder_unavailable", "Your home folder cannot be uploaded. Choose a project folder.");
		const aoState = path.join(home, ".ao");
		if (samePath(real, aoState) || real.toLowerCase().startsWith(`${aoState.toLowerCase()}${path.sep}`)) {
			throw new PushFolderError("folder_unavailable", "A folder inside AO's own data cannot be uploaded.");
		}
	}
	return real;
}

/** The folder's checked-out commit when the folder is the top of its own repository. */
async function ownRepositoryHead(folder: string, git: GitRunner, env: NodeJS.ProcessEnv): Promise<string | undefined> {
	try {
		const top = (await git(["-C", folder, "rev-parse", "--show-toplevel"], { env, timeout: 15_000 })).stdout.trim();
		// A folder inside another repository is imported on its own, not as that repository.
		const realTop = await fs.realpath(top);
		if (!samePath(realTop, folder)) return undefined;
		return (await git(["-C", folder, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"], { env, timeout: 15_000 })).stdout.trim() || undefined;
	} catch {
		return undefined;
	}
}

/** Bytes of the files an import would commit: everything git does not ignore. */
async function importSize(folder: string, gitDir: string, git: GitRunner, env: NodeJS.ProcessEnv, limit: number): Promise<number> {
	const { stdout } = await git(["--git-dir", gitDir, "--work-tree", folder, "ls-files", "-z", "--others", "--exclude-standard"], { env, timeout: GIT_STEP_TIMEOUT_MS });
	let total = 0;
	for (const name of stdout.split("\0")) {
		if (name === "") continue;
		try {
			total += (await fs.lstat(path.join(folder, name))).size;
		} catch {
			// Gone since it was listed: git add skips it too.
		}
		if (total > limit) return total;
	}
	return total;
}

/** Paths an import staged as links to other repositories (gitlinks), not as files. */
async function embeddedRepositories(gitDir: string, folder: string, git: GitRunner, env: NodeJS.ProcessEnv): Promise<string[]> {
	const { stdout } = await git(["--git-dir", gitDir, "--work-tree", folder, "ls-files", "--stage", "-z"], { env, timeout: GIT_STEP_TIMEOUT_MS });
	return stdout.split("\0").filter((entry) => entry.startsWith("160000 ")).map((entry) => entry.slice(entry.indexOf("\t") + 1));
}

async function hasConfiguredIdentity(folder: string, git: GitRunner, env: NodeJS.ProcessEnv): Promise<boolean> {
	const value = async (key: string) => {
		try {
			return (await git(["-C", folder, "config", "--get", key], { env, timeout: 15_000 })).stdout.trim();
		} catch {
			return "";
		}
	};
	return (await value("user.name")) !== "" && (await value("user.email")) !== "";
}

/** Commits the folder in gitDir with the folder as its work tree; returns the commit. */
async function importFolder(folder: string, gitDir: string, git: GitRunner, env: NodeJS.ProcessEnv, limit: number): Promise<string> {
	const scoped = ["--git-dir", gitDir, "--work-tree", folder];
	await git(["init", "--quiet", "--bare", gitDir], { env, timeout: GIT_STEP_TIMEOUT_MS });
	const size = await importSize(folder, gitDir, git, env, limit);
	if (size > limit) {
		throw new PushFolderError("folder_too_large", `${path.basename(folder)} holds more than ${Math.round(limit / (1024 * 1024 * 1024) * 10) / 10} GB of files git does not ignore. Add the large files to its .gitignore, or make it a git repository and add it as a repository.`);
	}
	await git([...scoped, "add", "--all"], { env, timeout: GIT_STEP_TIMEOUT_MS });
	// git adds a repository inside the folder as a bare link to its commit, so
	// its files would never reach the cloud.
	const embedded = await embeddedRepositories(gitDir, folder, git, env);
	if (embedded.length > 0) {
		const shown = embedded.slice(0, 3).join(", ") + (embedded.length > 3 ? `, and ${embedded.length - 3} more` : "");
		throw new PushFolderError("folder_has_repositories", `${path.basename(folder)} holds git repositories of its own (${shown}), whose files would not be uploaded. Add each of them to the workspace on its own, or add them to ${path.basename(folder)}'s .gitignore.`);
	}
	const identity = (await hasConfiguredIdentity(folder, git, env))
		? env
		: { ...env, GIT_AUTHOR_NAME: FALLBACK_COMMIT_NAME, GIT_AUTHOR_EMAIL: FALLBACK_COMMIT_EMAIL, GIT_COMMITTER_NAME: FALLBACK_COMMIT_NAME, GIT_COMMITTER_EMAIL: FALLBACK_COMMIT_EMAIL };
	// No signing prompt and no hooks: this commit is AO's, made in its own git directory.
	await git([...scoped, "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "--allow-empty", "-m", `Import ${path.basename(folder)}`], { env: identity, timeout: GIT_STEP_TIMEOUT_MS });
	return (await git(["--git-dir", gitDir, "rev-parse", "HEAD"], { env, timeout: 15_000 })).stdout.trim();
}

export async function pushFolderToRepository(input: unknown, deps: PushFolderDeps): Promise<PushFolderResult> {
	const git = deps.git ?? defaultGit;
	const env = quietGitEnv(deps.env?.() ?? process.env);
	let token: string | null = null;
	let gitDir: string | undefined;
	try {
		const { folder, pushPath, branch } = (typeof input === "object" && input !== null ? input : {}) as Record<string, unknown>;
		if (typeof folder !== "string" || typeof pushPath !== "string" || typeof branch !== "string" || !isValidPushBranch(branch)) {
			throw new PushFolderError("invalid_request", "Not a folder to upload.");
		}
		const controlPlane = await deps.controlPlaneUrl();
		if (!controlPlane) throw new PushFolderError("invalid_request", "No AO Cloud control plane is configured.");
		const remoteUrl = resolvePushUrl(pushPath, controlPlane).toString();
		const realFolder = await resolveFolder(folder, deps.homeDir ?? os.homedir());
		let version: string;
		try {
			version = (await git(["--version"], { env, timeout: 15_000 })).stdout;
		} catch {
			throw new PushFolderError("git_unavailable", "git is not installed or not on PATH. Install git, then try again.");
		}
		// The token reaches git through GIT_CONFIG_COUNT, which git reads from 2.31.
		if (!gitSupportsConfigEnv(version)) {
			throw new PushFolderError("git_unavailable", `Uploading a folder needs git 2.31 or newer; this computer has ${version.trim() || "an older git"}. Update git, then try again.`);
		}

		let commit = await ownRepositoryHead(realFolder, git, env);
		const source = commit ? "head" : "import";
		const pushArgs: string[] = [];
		if (commit) {
			pushArgs.push("-C", realFolder);
		} else {
			await fs.mkdir(deps.tmpRoot(), { recursive: true });
			gitDir = path.join(deps.tmpRoot(), `import-${randomUUID()}`);
			try {
				commit = await importFolder(realFolder, gitDir, git, env, deps.maxImportBytes ?? MAX_FOLDER_IMPORT_BYTES);
			} catch (error) {
				if (error instanceof PushFolderError) throw error;
				throw new PushFolderError("import_failed", `Could not prepare ${path.basename(realFolder)}: ${gitMessage(error)}`);
			}
			pushArgs.push("--git-dir", gitDir);
		}

		token = await deps.getToken();
		if (token === null || token === "") throw new PushFolderError("signed_out", "No AO Cloud session is available. Sign in and try again.");
		const remote = deps.remoteFor?.(remoteUrl) ?? remoteUrl;
		const pushEnv = withGitConfig(env, [
			// Sent only to this repository's URL, and never on to a redirect. The
			// empty value first drops any extraHeader the user's own config adds,
			// which would send a second Authorization header.
			[`http.${remoteUrl}.extraHeader`, ""],
			[`http.${remoteUrl}.extraHeader`, `Authorization: Bearer ${token}`],
			["http.followRedirects", "false"],
			["credential.helper", ""],
			["core.askPass", ""],
		]);
		try {
			await git([...pushArgs, "push", "--quiet", "--no-verify", remote, `${commit}:refs/heads/${branch}`], { env: pushEnv, timeout: GIT_PUSH_TIMEOUT_MS });
		} catch (error) {
			throw pushRefusal(error, path.basename(realFolder), branch);
		}
		return { status: "pushed", branch, commit: commit ?? "", source };
	} catch (error) {
		if (error instanceof PushFolderError) return { status: "error", code: error.code, message: redact(error.message, token) };
		return { status: "error", code: "invalid_request", message: redact(error instanceof Error ? error.message : String(error), token) };
	} finally {
		if (gitDir) await fs.rm(gitDir, { recursive: true, force: true }).catch(() => undefined);
	}
}

/** Import directories left by a crash; they hold only copies of uploaded files. */
export async function clearFolderImports(tmpRoot: string): Promise<void> {
	await fs.rm(tmpRoot, { recursive: true, force: true });
}

/**
 * Refuses a call from anything but a window's main frame, where the bridge
 * lives, as the share flow does.
 */
function fromMainFrame(event: unknown): boolean {
	const { senderFrame } = (event ?? {}) as { senderFrame?: { parent: unknown } | null };
	return !(senderFrame === null || (senderFrame && senderFrame.parent !== null));
}

export function registerCloudFolderPushIpc(ipcMain: { handle(channel: string, handler: (event: unknown, input: unknown) => unknown): void }, deps: PushFolderDeps & { ready?: () => Promise<void> }): void {
	ipcMain.handle(CLOUD_PUSH_FOLDER_CHANNEL, async (event, input) => {
		if (!fromMainFrame(event)) return { status: "error", code: "invalid_request", message: "Uploading folders is only available from the app window." } satisfies PushFolderResult;
		await deps.ready?.();
		return pushFolderToRepository(input, deps);
	});
}
