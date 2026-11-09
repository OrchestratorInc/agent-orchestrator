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
// Files that usually hold credentials (isSecretLookingPath) never reach
// GitHub: an import leaves them out of its commit and reports which, and a
// repository folder whose history holds any is refused, since its commits
// cannot be uploaded without them. previewFolderUpload tells the app both
// before anything is uploaded.
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
export const CLOUD_PREVIEW_FOLDER_CHANNEL = "cloud:previewFolderUpload";

/** An import larger than this is refused before anything is written. */
export const MAX_FOLDER_IMPORT_BYTES = 1.5 * 1024 * 1024 * 1024;

/** GitHub refuses a push holding any file larger than this. */
export const MAX_GITHUB_FILE_BYTES = 100 * 1024 * 1024;

/** The control plane refuses a push larger than this (gitProxyMaxPush). */
export const MAX_PUSH_BYTES = 2 * 1024 * 1024 * 1024;

/** The commit identity of an import when git has none configured. */
export const FALLBACK_COMMIT_NAME = "Orchestrator";
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
	| "shallow_repository"
	| "lfs_unsupported"
	| "file_too_large"
	| "workflows_refused"
	| "git_unavailable"
	| "import_failed"
	| "push_failed"
	| "push_forbidden"
	| "branch_exists"
	| "secret_files";

export type PushFolderResult =
	/** leftOut: the secret-looking files an import did not upload (isSecretLookingPath). */
	| { status: "pushed"; branch: string; commit: string; source: "head" | "import"; leftOut: string[] }
	| { status: "error"; code: PushFolderErrorCode; message: string };

/**
 * What uploading a folder would do, before anything is uploaded: whether its
 * commit or its files go, and the secret-looking files an import leaves out.
 * A repository whose history holds secret-looking files is a secret_files error.
 */
export type PushFolderPreview =
	| { status: "ok"; source: "head" | "import"; leftOut: string[] }
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
	/**
	 * Folders holding Orchestrator's own state: the data directory the app
	 * resolves (AO_DATA_DIR) and its Electron profile. A folder inside one, or
	 * holding one, is never uploaded. Defaults to ~/.ao.
	 */
	appStateDirs?: () => string[];
	git?: GitRunner;
	/**
	 * The remote git pushes to for the resolved push URL. Tests point it at a
	 * local bare repository; the app always pushes to the URL itself.
	 */
	remoteFor?: (pushUrl: string) => string;
	maxImportBytes?: number;
	maxFileBytes?: number;
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
		throw new PushFolderError("invalid_request", "The repository's push address is not an Orchestrator Cloud repository.");
	}
	const base = parseCloudBaseUrl(controlPlane);
	const basePath = base.pathname.replace(/\/+$/, "");
	const url = new URL(base.origin + basePath + pushPath);
	if (url.origin !== base.origin || url.pathname !== basePath + pushPath) {
		throw new PushFolderError("invalid_request", "The repository's push address is not on the Orchestrator Cloud control plane.");
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
	// The deciding line is not always among the last few git prints.
	const stderr = typeof (error as { stderr?: unknown })?.stderr === "string" ? (error as { stderr: string }).stderr : message;
	const status = /returned error: (\d{3})/.exec(stderr)?.[1];
	// git answers a 401 by asking for credentials, which it may not here, so it
	// never says "returned error: 401" for one.
	if (status === "401" || /could not read (Username|Password)|Authentication failed/i.test(stderr)) {
		return new PushFolderError("signed_out", "Your Orchestrator Cloud session expired. Sign in again, then try again.");
	}
	// git reports a 404 on its first request as "repository '…' not found".
	if (status === "403" || status === "404" || /repository '[^']*' not found/i.test(stderr)) {
		return new PushFolderError("push_forbidden", `Orchestrator Cloud did not let you upload ${folderName} to its repository. Check that the Orchestrator GitHub App still has access to it, then try again.`);
	}
	if (/refusing to allow a GitHub App to create or update workflow/i.test(stderr)) {
		return new PushFolderError("workflows_refused", `GitHub refused ${folderName} because it holds GitHub Actions workflows (.github/workflows), which the Orchestrator GitHub App may not add. Remove them from what you upload, or add the repository from GitHub instead.`);
	}
	if (/GH001|exceeds GitHub's file size limit/i.test(stderr)) {
		return new PushFolderError("file_too_large", `GitHub refused ${folderName} because it holds a file larger than 100 MB. Remove it from what you upload (or its history), then try again.`);
	}
	// The control plane refuses updating a branch that exists ("already
	// exists"); git refuses on its own first when the branch holds a commit it
	// doesn't have ("fetch first").
	if (/already exists|\[rejected\].*\((fetch first|non-fast-forward)\)/i.test(stderr)) {
		return new PushFolderError("branch_exists", `The repository for ${folderName} already has a ${branch} branch, and Orchestrator only uploads a folder into an empty repository. If an earlier upload finished, remove ${folderName} and add its repository from GitHub instead.`);
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
	// The token must not travel over unverified TLS or into a trace.
	delete env.GIT_SSL_NO_VERIFY;
	delete env.GIT_CURL_VERBOSE;
	for (const key of Object.keys(env)) if (key.startsWith("GIT_TRACE")) delete env[key];
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

/** Whether this platform's default file system ignores case (macOS, Windows). */
const CASE_INSENSITIVE_FS = process.platform === "win32" || process.platform === "darwin";

function samePath(a: string, b: string): boolean {
	return CASE_INSENSITIVE_FS ? a.toLowerCase() === b.toLowerCase() : a === b;
}

/**
 * Whether child is parent or inside it. Case is ignored only where the file
 * system ignores it, so on Linux /data/AO is not inside /data/ao.
 */
export function pathWithin(child: string, parent: string, caseInsensitive = CASE_INSENSITIVE_FS): boolean {
	const fold = (value: string) => (caseInsensitive ? value.toLowerCase() : value);
	const relative = path.relative(fold(path.resolve(parent)), fold(path.resolve(child)));
	return relative === "" || (relative !== ".." && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative));
}

/**
 * Files a folder upload never puts on GitHub, because they usually hold
 * credentials. Matched on each file's path inside the folder, at any depth,
 * ignoring case:
 * - environment files: .env and .env.*, except .env.example, .env.sample and
 *   .env.template, which hold names, not values;
 * - private keys and certificate bundles: *.pem, *.key, *.p12, *.pfx, and the
 *   SSH keys id_rsa, id_dsa, id_ecdsa and id_ed25519 (their .pub halves are kept);
 * - tool credentials: .npmrc, .netrc, .pypirc, .git-credentials;
 * - cloud credentials: credentials.json, client_secret*.json, service-account*.json;
 * - anything inside a .ssh, .aws or .gnupg folder.
 */
const SECRET_FILE_NAMES = new Set([".env", ".npmrc", ".netrc", ".pypirc", ".git-credentials", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "credentials.json"]);
const SECRET_FILE_EXTENSIONS = [".pem", ".key", ".p12", ".pfx"];
const KEPT_ENV_FILES = new Set([".env.example", ".env.sample", ".env.template"]);
const SECRET_FOLDERS = new Set([".ssh", ".aws", ".gnupg"]);

export function isSecretLookingPath(relativePath: string): boolean {
	const segments = relativePath.replace(/\\/g, "/").split("/").filter((segment) => segment !== "").map((segment) => segment.toLowerCase());
	const name = segments[segments.length - 1];
	if (name === undefined) return false;
	if (segments.slice(0, -1).some((segment) => SECRET_FOLDERS.has(segment))) return true;
	if (/[\\/]$/.test(relativePath)) return SECRET_FOLDERS.has(name);
	if (SECRET_FILE_NAMES.has(name)) return true;
	if (name.startsWith(".env.")) return !KEPT_ENV_FILES.has(name);
	if (SECRET_FILE_EXTENSIONS.some((extension) => name.endsWith(extension))) return true;
	return /^(?:client_secret.*|service-account.*)\.json$/.test(name);
}

function shownFiles(files: readonly string[]): string {
	return files.slice(0, 5).join(", ") + (files.length > 5 ? `, and ${files.length - 5} more` : "");
}

function secretFilesError(folderName: string, files: readonly string[]): PushFolderError {
	return new PushFolderError("secret_files", `${folderName}'s history holds files that look like secrets (${shownFiles(files)}), so Orchestrator does not upload it: they would reach GitHub. Remove them from its history, or push it to GitHub yourself and add its repository from GitHub.`);
}

/** The folder, resolved, when it is a directory that may be uploaded. */
async function resolveFolder(folder: string, homeDir: string | undefined, appStateDirs: readonly string[]): Promise<string> {
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
	}
	for (const stateDir of appStateDirs) {
		const realState = await fs.realpath(stateDir).catch(() => path.resolve(stateDir));
		if (pathWithin(real, realState) || pathWithin(realState, real)) {
			throw new PushFolderError("folder_unavailable", "A folder that is or holds Orchestrator's own data cannot be uploaded. Choose a project folder.");
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

/** Whether a .gitattributes file stores paths in Git LFS, which this push cannot carry. */
function usesLfs(attributes: string): boolean {
	return /\bfilter\s*=\s*lfs\b/.test(attributes);
}

function largeFileError(folderName: string, file: string): PushFolderError {
	return new PushFolderError("file_too_large", `${folderName} holds ${file}, which is larger than GitHub's 100 MB file limit. Remove it from what you upload, then try again.`);
}

function lfsError(folderName: string): PushFolderError {
	return new PushFolderError("lfs_unsupported", `${folderName} stores files in Git LFS, which Orchestrator cannot upload yet. Push it to GitHub yourself, then add its repository from GitHub.`);
}

/**
 * Refuses a repository folder whose commit GitHub would refuse or that would
 * arrive incomplete: a shallow clone (no history to push), submodules (only
 * links would arrive), Git LFS (only pointers would arrive), a file over
 * GitHub's limit, or more than the control plane takes.
 */
async function checkRepositoryHead(folder: string, git: GitRunner, env: NodeJS.ProcessEnv, maxFileBytes: number): Promise<void> {
	const name = path.basename(folder);
	const shallow = (await git(["-C", folder, "rev-parse", "--is-shallow-repository"], { env, timeout: 15_000 })).stdout.trim();
	if (shallow === "true") {
		throw new PushFolderError("shallow_repository", `${name} is a shallow clone, so its history cannot be uploaded. Run "git fetch --unshallow" in it, then try again.`);
	}
	const { stdout } = await git(["-C", folder, "ls-tree", "-r", "-l", "-z", "HEAD"], { env, timeout: GIT_STEP_TIMEOUT_MS });
	const submodules: string[] = [];
	for (const entry of stdout.split("\0")) {
		const tab = entry.indexOf("\t");
		if (tab < 0) continue;
		const [mode, , object, size] = entry.slice(0, tab).split(/\s+/);
		const file = entry.slice(tab + 1);
		if (mode === "160000") {
			submodules.push(file);
			continue;
		}
		if (Number(size) > maxFileBytes) throw largeFileError(name, file);
		if (path.posix.basename(file) === ".gitattributes" && usesLfs((await git(["-C", folder, "cat-file", "blob", object], { env, timeout: 15_000 })).stdout)) {
			throw lfsError(name);
		}
	}
	if (submodules.length > 0) {
		const shown = submodules.slice(0, 3).join(", ") + (submodules.length > 3 ? `, and ${submodules.length - 3} more` : "");
		throw new PushFolderError("folder_has_repositories", `${name} holds submodules (${shown}), whose files would not be uploaded. Add each of them to the workspace on its own.`);
	}
	try {
		// git 2.38+; older git just skips this check.
		const usage = Number((await git(["-C", folder, "rev-list", "--objects", "--disk-usage", "HEAD"], { env, timeout: GIT_STEP_TIMEOUT_MS })).stdout.trim());
		if (usage > MAX_PUSH_BYTES) {
			throw new PushFolderError("folder_too_large", `${name}'s history is larger than the 2 GB Orchestrator Cloud uploads. Add its repository from GitHub instead.`);
		}
	} catch (error) {
		if (error instanceof PushFolderError) throw error;
	}
}

/** The secret-looking files anywhere in the history a repository folder would upload. */
async function committedSecretFiles(folder: string, git: GitRunner, env: NodeJS.ProcessEnv): Promise<string[]> {
	const { stdout } = await git(["-C", folder, "log", "--format=", "--name-only", "--no-renames", "-z", "HEAD"], { env, timeout: GIT_STEP_TIMEOUT_MS });
	return [...new Set(stdout.split(/[\0\n]/).filter((file) => file !== "" && isSecretLookingPath(file)))].sort();
}

/** The files an import would consider: everything in the folder git does not ignore. */
async function listImportFiles(folder: string, gitDir: string, git: GitRunner, env: NodeJS.ProcessEnv): Promise<string[]> {
	const { stdout } = await git(["--git-dir", gitDir, "--work-tree", folder, "ls-files", "-z", "--others", "--exclude-standard"], { env, timeout: GIT_STEP_TIMEOUT_MS });
	return stdout.split("\0").filter((file) => file !== "");
}

interface ImportScan {
	/** Bytes of the files an import would commit: everything git does not ignore. */
	size: number;
	/** The newest modification time among them, so an unchanged folder imports to the same commit. */
	newestMs: number;
}

/** Looks over the files an import commits, refusing what GitHub would refuse. */
async function scanImport(folder: string, files: readonly string[], limit: number, maxFileBytes: number): Promise<ImportScan> {
	const name = path.basename(folder);
	const scan: ImportScan = { size: 0, newestMs: (await fs.stat(folder)).mtimeMs };
	for (const file of files) {
		const full = path.join(folder, file);
		try {
			const stat = await fs.lstat(full);
			if (stat.size > maxFileBytes) throw largeFileError(name, file);
			scan.size += stat.size;
			scan.newestMs = Math.max(scan.newestMs, stat.mtimeMs);
		} catch (error) {
			if (error instanceof PushFolderError) throw error;
			// Gone since it was listed: git add skips it too.
		}
		if (scan.size > limit) return scan;
		// Checked before git add, which would run the LFS filter and keep only pointers.
		if (path.basename(file) === ".gitattributes" && usesLfs(await fs.readFile(full, "utf8").catch(() => ""))) throw lfsError(name);
	}
	return scan;
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

/**
 * Commits the folder in gitDir with the folder as its work tree, all but its
 * secret-looking files; returns the commit and the files left out.
 */
async function importFolder(folder: string, gitDir: string, git: GitRunner, env: NodeJS.ProcessEnv, limit: number, maxFileBytes: number): Promise<{ commit: string; leftOut: string[] }> {
	const scoped = ["--git-dir", gitDir, "--work-tree", folder];
	await git(["init", "--quiet", "--bare", gitDir], { env, timeout: GIT_STEP_TIMEOUT_MS });
	const listed = await listImportFiles(folder, gitDir, git, env);
	const leftOut = listed.filter(isSecretLookingPath);
	const kept = listed.filter((file) => !isSecretLookingPath(file));
	const { size, newestMs } = await scanImport(folder, kept, limit, maxFileBytes);
	if (size > limit) {
		throw new PushFolderError("folder_too_large", `${path.basename(folder)} holds more than ${Math.round(limit / (1024 * 1024 * 1024) * 10) / 10} GB of files git does not ignore. Add the large files to its .gitignore, or make it a git repository and add it as a repository.`);
	}
	if (kept.length > 0) {
		// Exactly the files kept, named literally: never "add --all", which
		// would take the secret-looking ones too.
		const pathspecs = path.join(gitDir, "ao-import-paths");
		await fs.writeFile(pathspecs, `${kept.join("\0")}\0`);
		await git([...scoped, "add", `--pathspec-from-file=${pathspecs}`, "--pathspec-file-nul"], { env: { ...env, GIT_LITERAL_PATHSPECS: "1" }, timeout: GIT_STEP_TIMEOUT_MS });
	}
	// git adds a repository inside the folder as a bare link to its commit, so
	// its files would never reach the cloud.
	const embedded = await embeddedRepositories(gitDir, folder, git, env);
	if (embedded.length > 0) {
		const shown = embedded.slice(0, 3).join(", ") + (embedded.length > 3 ? `, and ${embedded.length - 3} more` : "");
		throw new PushFolderError("folder_has_repositories", `${path.basename(folder)} holds git repositories of its own (${shown}), whose files would not be uploaded. Add each of them to the workspace on its own, or add them to ${path.basename(folder)}'s .gitignore.`);
	}
	// Dated by the folder's newest change, so a retry of an unchanged folder
	// (say after a push whose reply was lost) makes the same commit and finds
	// it already uploaded.
	const date = `@${Math.floor(newestMs / 1000)} +0000`;
	const dated = { ...env, GIT_AUTHOR_DATE: date, GIT_COMMITTER_DATE: date };
	const identity = (await hasConfiguredIdentity(folder, git, env))
		? dated
		: { ...dated, GIT_AUTHOR_NAME: FALLBACK_COMMIT_NAME, GIT_AUTHOR_EMAIL: FALLBACK_COMMIT_EMAIL, GIT_COMMITTER_NAME: FALLBACK_COMMIT_NAME, GIT_COMMITTER_EMAIL: FALLBACK_COMMIT_EMAIL };
	// No signing prompt and no hooks: this commit is AO's, made in its own git directory.
	await git([...scoped, "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "--allow-empty", "-m", `Import ${path.basename(folder)}`], { env: identity, timeout: GIT_STEP_TIMEOUT_MS });
	return { commit: (await git(["--git-dir", gitDir, "rev-parse", "HEAD"], { env, timeout: 15_000 })).stdout.trim(), leftOut };
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
		if (!controlPlane) throw new PushFolderError("invalid_request", "No Orchestrator Cloud control plane is configured.");
		const remoteUrl = resolvePushUrl(pushPath, controlPlane).toString();
		const realFolder = await resolveFolder(folder, deps.homeDir ?? os.homedir(), appStateDirs(deps));
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
		const maxFileBytes = deps.maxFileBytes ?? MAX_GITHUB_FILE_BYTES;
		const pushArgs: string[] = [];
		let leftOut: string[] = [];
		if (commit) {
			try {
				await checkRepositoryHead(realFolder, git, env, maxFileBytes);
				const secrets = await committedSecretFiles(realFolder, git, env);
				if (secrets.length > 0) throw secretFilesError(path.basename(realFolder), secrets);
			} catch (error) {
				if (error instanceof PushFolderError) throw error;
				throw new PushFolderError("import_failed", `Could not prepare ${path.basename(realFolder)}: ${gitMessage(error)}`);
			}
			pushArgs.push("-C", realFolder);
		} else {
			await fs.mkdir(deps.tmpRoot(), { recursive: true });
			gitDir = path.join(deps.tmpRoot(), `import-${randomUUID()}`);
			try {
				({ commit, leftOut } = await importFolder(realFolder, gitDir, git, env, deps.maxImportBytes ?? MAX_FOLDER_IMPORT_BYTES, maxFileBytes));
			} catch (error) {
				if (error instanceof PushFolderError) throw error;
				throw new PushFolderError("import_failed", `Could not prepare ${path.basename(realFolder)}: ${gitMessage(error)}`);
			}
			pushArgs.push("--git-dir", gitDir);
		}

		token = await deps.getToken();
		if (token === null || token === "") throw new PushFolderError("signed_out", "No Orchestrator Cloud session is available. Sign in and try again.");
		const remote = deps.remoteFor?.(remoteUrl) ?? remoteUrl;
		const pushEnv = withGitConfig(env, [
			// Sent only to this repository's URL, and never on to a redirect. The
			// empty value first drops any extraHeader the user's own config adds,
			// which would send a second Authorization header.
			[`http.${remoteUrl}.extraHeader`, ""],
			[`http.${remoteUrl}.extraHeader`, `Authorization: Bearer ${token}`],
			["http.followRedirects", "false"],
			[`http.${remoteUrl}.sslVerify`, "true"],
			["credential.helper", ""],
			["core.askPass", ""],
		]);
		try {
			await git([...pushArgs, "push", "--quiet", "--no-verify", remote, `${commit}:refs/heads/${branch}`], { env: pushEnv, timeout: GIT_PUSH_TIMEOUT_MS });
		} catch (error) {
			throw pushRefusal(error, path.basename(realFolder), branch);
		}
		return { status: "pushed", branch, commit: commit ?? "", source, leftOut };
	} catch (error) {
		if (error instanceof PushFolderError) return { status: "error", code: error.code, message: redact(error.message, token) };
		return { status: "error", code: "invalid_request", message: redact(error instanceof Error ? error.message : String(error), token) };
	} finally {
		if (gitDir) await fs.rm(gitDir, { recursive: true, force: true }).catch(() => undefined);
	}
}

function appStateDirs(deps: Pick<PushFolderDeps, "appStateDirs" | "homeDir">): string[] {
	return deps.appStateDirs?.() ?? [path.join(deps.homeDir ?? os.homedir(), ".ao")];
}

/**
 * What uploading a folder would do (PushFolderPreview), without uploading:
 * the same checks pushFolderToRepository makes first, and the files an import
 * would leave out. Nothing in the folder changes.
 */
export async function previewFolderUpload(input: unknown, deps: PushFolderDeps): Promise<PushFolderPreview> {
	const git = deps.git ?? defaultGit;
	const env = quietGitEnv(deps.env?.() ?? process.env);
	let gitDir: string | undefined;
	try {
		const folder = (typeof input === "object" && input !== null ? (input as Record<string, unknown>).folder : undefined);
		if (typeof folder !== "string") throw new PushFolderError("invalid_request", "Not a folder to upload.");
		const realFolder = await resolveFolder(folder, deps.homeDir ?? os.homedir(), appStateDirs(deps));
		try {
			await git(["--version"], { env, timeout: 15_000 });
		} catch {
			throw new PushFolderError("git_unavailable", "git is not installed or not on PATH. Install git, then try again.");
		}
		if (await ownRepositoryHead(realFolder, git, env)) {
			const secrets = await committedSecretFiles(realFolder, git, env);
			if (secrets.length > 0) throw secretFilesError(path.basename(realFolder), secrets);
			return { status: "ok", source: "head", leftOut: [] };
		}
		await fs.mkdir(deps.tmpRoot(), { recursive: true });
		gitDir = path.join(deps.tmpRoot(), `preview-${randomUUID()}`);
		await git(["init", "--quiet", "--bare", gitDir], { env, timeout: GIT_STEP_TIMEOUT_MS });
		const leftOut = (await listImportFiles(realFolder, gitDir, git, env)).filter(isSecretLookingPath);
		return { status: "ok", source: "import", leftOut };
	} catch (error) {
		if (error instanceof PushFolderError) return { status: "error", code: error.code, message: error.message };
		return { status: "error", code: "import_failed", message: error instanceof Error ? error.message : String(error) };
	} finally {
		if (gitDir) await fs.rm(gitDir, { recursive: true, force: true }).catch(() => undefined);
	}
}

/** How old an import directory must be before it is taken as left by a crash. */
const STALE_IMPORT_MS = 2 * GIT_PUSH_TIMEOUT_MS;

/**
 * Import directories left by a crash; they hold only copies of uploaded files.
 * A recent one may be another running app's (dev beside packaged share ~/.ao)
 * and is left alone.
 */
export async function clearFolderImports(tmpRoot: string, now = Date.now()): Promise<void> {
	let entries: string[];
	try {
		entries = await fs.readdir(tmpRoot);
	} catch {
		return;
	}
	await Promise.all(entries.filter((entry) => entry.startsWith("import-") || entry.startsWith("preview-")).map(async (entry) => {
		const full = path.join(tmpRoot, entry);
		const stat = await fs.lstat(full).catch(() => null);
		if (stat && now - stat.mtimeMs > STALE_IMPORT_MS) await fs.rm(full, { recursive: true, force: true }).catch(() => undefined);
	}));
}

/**
 * Folders the person picked in this app's folder dialog. Only these may be
 * uploaded, so a renderer cannot name any folder on the disk.
 */
const chosenFolders = new Set<string>();

export function rememberChosenFolder(folder: string): void {
	chosenFolders.add(path.resolve(folder));
}

export function isChosenFolder(folder: string): boolean {
	return chosenFolders.has(path.resolve(folder));
}

/**
 * Refuses a call from anything but a window's main frame, where the bridge
 * lives, as the share flow does.
 */
function fromMainFrame(event: unknown): boolean {
	const { senderFrame } = (event ?? {}) as { senderFrame?: { parent: unknown } | null };
	return !(senderFrame === null || (senderFrame && senderFrame.parent !== null));
}

export interface CloudFolderPushIpcDeps extends PushFolderDeps {
	ready?: () => Promise<void>;
	/** Whether the request comes from the app's own shell window. */
	trustedSender?: (sender: unknown) => boolean;
	/** Whether the folder is one the person picked (isChosenFolder). */
	chosenFolder?: (folder: string) => boolean;
}

export function registerCloudFolderPushIpc(ipcMain: { handle(channel: string, handler: (event: unknown, input: unknown) => unknown): void }, deps: CloudFolderPushIpcDeps): void {
	// Both answer only the app's own window, for a folder picked in its dialog.
	const refusal = (event: unknown, input: unknown): { status: "error"; code: "invalid_request"; message: string } | undefined => {
		const sender = (event as { sender?: unknown } | null)?.sender;
		if (!fromMainFrame(event) || (deps.trustedSender && !deps.trustedSender(sender))) {
			return { status: "error", code: "invalid_request", message: "Uploading folders is only available from the app window." };
		}
		const folder = (input as { folder?: unknown } | null)?.folder;
		if (deps.chosenFolder && (typeof folder !== "string" || !deps.chosenFolder(folder))) {
			return { status: "error", code: "invalid_request", message: "Choose the folder to upload in the folder dialog first." };
		}
		return undefined;
	};
	ipcMain.handle(CLOUD_PUSH_FOLDER_CHANNEL, async (event, input): Promise<PushFolderResult> => {
		const refused = refusal(event, input);
		if (refused) return refused;
		await deps.ready?.();
		return pushFolderToRepository(input, deps);
	});
	ipcMain.handle(CLOUD_PREVIEW_FOLDER_CHANNEL, async (event, input): Promise<PushFolderPreview> => {
		const refused = refusal(event, input);
		if (refused) return refused;
		await deps.ready?.();
		return previewFolderUpload(input, deps);
	});
}
