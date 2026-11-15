import { execFile, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import type { ControlPlaneUrlResolver } from "./cloud-control-plane-url";
import { parseCloudBaseUrl } from "./cloud-cp-proxy";

export const CLOUD_PUSH_FOLDER_CHANNEL = "cloud:pushFolderToRepository";
export const CLOUD_PREVIEW_FOLDER_CHANNEL = "cloud:previewFolderUpload";

export const MAX_FOLDER_IMPORT_BYTES = 1.5 * 1024 * 1024 * 1024;

export const MAX_GITHUB_FILE_BYTES = 100 * 1024 * 1024;

export const MAX_PUSH_BYTES = 2 * 1024 * 1024 * 1024;

export const FALLBACK_COMMIT_NAME = "Orchestrator";
export const FALLBACK_COMMIT_EMAIL = "noreply@aoagents.dev";

const GIT_STEP_TIMEOUT_MS = 10 * 60_000;
const GIT_PUSH_TIMEOUT_MS = 30 * 60_000;
const GIT_MAX_BUFFER = 64 * 1024 * 1024;

export interface PushFolderInput {
	folder: string;
	pushPath: string;
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
	| { status: "pushed"; branch: string; commit: string; source: "head" | "import"; leftOut: string[] }
	| PushFolderFailure;

export type PushFolderFailure = { status: "error"; code: PushFolderErrorCode; message: string; files?: string[] };

export type PushFolderPreview =
	| { status: "ok"; source: "head" | "import"; branch?: string; leftOut: string[] }
	| PushFolderFailure;

export interface GitRunOptions {
	cwd?: string;
	env: NodeJS.ProcessEnv;
	timeout: number;
}

export type GitRunner = (args: string[], options: GitRunOptions) => Promise<{ stdout: string; stderr: string }>;

export interface GitStreamOptions extends GitRunOptions {
	input?: string;
	encoding?: "utf8" | "latin1";
}

export type GitStreamRunner = (args: string[], options: GitStreamOptions, onChunk: (chunk: string) => void) => Promise<void>;

export interface PushFolderDeps {
	controlPlaneUrl: ControlPlaneUrlResolver;
	getToken: () => Promise<string | null>;
	tmpRoot: () => string;
	env?: () => NodeJS.ProcessEnv;
	homeDir?: string;
	appStateDirs?: () => string[];
	git?: GitRunner;
	gitStream?: GitStreamRunner;
	now?: () => number;
	remoteFor?: (pushUrl: string) => string;
	maxImportBytes?: number;
	maxFileBytes?: number;
}

class PushFolderError extends Error {
	constructor(readonly code: PushFolderErrorCode, message: string, readonly files?: string[]) {
		super(message);
	}
}

function failure(error: PushFolderError, message = error.message): PushFolderFailure {
	return { status: "error", code: error.code, message, ...(error.files ? { files: error.files } : {}) };
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

const defaultGitStream: GitStreamRunner = (args, options, onChunk) =>
	new Promise((resolve, reject) => {
		const child = spawn("git", args, { cwd: options.cwd, env: options.env, stdio: ["pipe", "pipe", "pipe"] });
		let stderr = "";
		let killed = false;
		let failed: unknown;
		const timer = setTimeout(() => {
			killed = true;
			child.kill();
		}, options.timeout);
		child.stdin.on("error", () => undefined);
		child.stdin.end(options.input ?? "");
		child.stdout.setEncoding(options.encoding ?? "utf8");
		child.stdout.on("data", (chunk: string) => {
			if (failed !== undefined) return;
			try {
				onChunk(chunk);
			} catch (error) {
				failed = error;
				child.kill();
			}
		});
		child.stderr.setEncoding("utf8");
		child.stderr.on("data", (chunk: string) => {
			stderr = (stderr + chunk).slice(-64 * 1024);
		});
		child.on("error", (error) => {
			clearTimeout(timer);
			reject(Object.assign(error, { stderr }));
		});
		child.on("close", (code, signal) => {
			clearTimeout(timer);
			if (failed !== undefined) reject(failed);
			else if (code === 0) resolve();
			else reject(Object.assign(new Error(`git exited with ${code ?? signal}`), { stderr, killed, code }));
		});
	});

export function isValidPushBranch(branch: string): boolean {
	if (branch === "" || branch.length > 255) return false;
	if (!/^[A-Za-z0-9._/-]+$/.test(branch)) return false;
	if (branch.startsWith("-") || branch.startsWith("/") || branch.endsWith("/") || branch.endsWith(".") || branch.endsWith(".lock")) return false;
	return !branch.split("/").some((part) => part === "" || part.startsWith("."));
}

const PUSH_PATH = /^\/api\/cloud\/v1\/orgs\/[A-Za-z0-9_-]+\/github\/git\/[A-Za-z0-9-]+\/[A-Za-z0-9_.-]+\.git$/;

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

function pushRefusal(error: unknown, folderName: string, branch: string): PushFolderError {
	const message = gitMessage(error);
	const stderr = typeof (error as { stderr?: unknown })?.stderr === "string" ? (error as { stderr: string }).stderr : message;
	const status = /returned error: (\d{3})/.exec(stderr)?.[1];
	if (status === "401" || /could not read (Username|Password)|Authentication failed/i.test(stderr)) {
		return new PushFolderError("signed_out", "Your Orchestrator Cloud session expired. Sign in again, then try again.");
	}
	if (status === "403" || status === "404" || /repository '[^']*' not found/i.test(stderr)) {
		return new PushFolderError("push_forbidden", `Orchestrator Cloud did not let you upload ${folderName} to its repository. Check that the Orchestrator GitHub App still has access to it, then try again.`);
	}
	if (/refusing to allow a GitHub App to create or update workflow/i.test(stderr)) {
		return new PushFolderError("workflows_refused", `GitHub refused ${folderName} because it holds GitHub Actions workflows (.github/workflows), which the Orchestrator GitHub App may not add. Remove them from what you upload, or add the repository from GitHub instead.`);
	}
	if (/GH001|exceeds GitHub's file size limit/i.test(stderr)) {
		return new PushFolderError("file_too_large", `GitHub refused ${folderName} because it holds a file larger than 100 MB. Remove it from what you upload (or its history), then try again.`);
	}
	if (/already exists|\[rejected\].*\((fetch first|non-fast-forward)\)/i.test(stderr)) {
		return new PushFolderError("branch_exists", `The repository for ${folderName} already has a ${branch} branch, and Orchestrator only uploads a folder into an empty repository. If an earlier upload finished, remove ${folderName} and add its repository from GitHub instead.`);
	}
	return new PushFolderError("push_failed", `Could not upload ${folderName}: ${message}`);
}

function redact(message: string, token: string | null): string {
	return token ? message.split(token).join("***") : message;
}

export function gitSupportsConfigEnv(versionOutput: string): boolean {
	const match = /git version (\d+)\.(\d+)/.exec(versionOutput);
	if (!match) return false;
	const [major, minor] = [Number(match[1]), Number(match[2])];
	return major > 2 || (major === 2 && minor >= 31);
}

function quietGitEnv(base: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
	const env: NodeJS.ProcessEnv = { ...base, GIT_TERMINAL_PROMPT: "0", GIT_ASKPASS: "", SSH_ASKPASS: "", GCM_INTERACTIVE: "never" };
	for (const key of Object.keys(env)) {
		if (key === "GIT_CONFIG_COUNT" || key.startsWith("GIT_CONFIG_KEY_") || key.startsWith("GIT_CONFIG_VALUE_")) delete env[key];
	}
	delete env.GIT_DIR;
	delete env.GIT_WORK_TREE;
	delete env.GIT_INDEX_FILE;
	delete env.GIT_SSL_NO_VERIFY;
	delete env.GIT_CURL_VERBOSE;
	for (const key of Object.keys(env)) if (key.startsWith("GIT_TRACE")) delete env[key];
	return env;
}

function withGitConfig(env: NodeJS.ProcessEnv, entries: Array<[string, string]>): NodeJS.ProcessEnv {
	const next: NodeJS.ProcessEnv = { ...env, GIT_CONFIG_COUNT: String(entries.length) };
	entries.forEach(([key, value], index) => {
		next[`GIT_CONFIG_KEY_${index}`] = key;
		next[`GIT_CONFIG_VALUE_${index}`] = value;
	});
	return next;
}

const CASE_INSENSITIVE_FS = process.platform === "win32" || process.platform === "darwin";

function samePath(a: string, b: string): boolean {
	return CASE_INSENSITIVE_FS ? a.toLowerCase() === b.toLowerCase() : a === b;
}

export function pathWithin(child: string, parent: string, caseInsensitive = CASE_INSENSITIVE_FS): boolean {
	const fold = (value: string) => (caseInsensitive ? value.toLowerCase() : value);
	const relative = path.relative(fold(path.resolve(parent)), fold(path.resolve(child)));
	return relative === "" || (relative !== ".." && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative));
}

const SECRET_FILE_NAMES = new Set([".env", ".netrc", ".pypirc", ".git-credentials", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "credentials.json"]);
const SECRET_FILE_EXTENSIONS = [".p12", ".pfx"];
const CONTENT_FILE_NAMES = new Set([".npmrc"]);
const CONTENT_FILE_EXTENSIONS = [".pem", ".key"];
const KEPT_ENV_FILES = new Set([".env.example", ".env.sample", ".env.template"]);
const SECRET_FOLDERS = new Set([".ssh", ".aws", ".gnupg"]);

export type SecretPathKind = "secret" | "content" | "none";

export function secretPathKind(relativePath: string): SecretPathKind {
	const segments = relativePath.replace(/\\/g, "/").split("/").filter((segment) => segment !== "").map((segment) => segment.toLowerCase());
	const name = segments[segments.length - 1];
	if (name === undefined) return "none";
	if (segments.slice(0, -1).some((segment) => SECRET_FOLDERS.has(segment))) return "secret";
	if (/[\\/]$/.test(relativePath)) return SECRET_FOLDERS.has(name) ? "secret" : "none";
	if (SECRET_FILE_NAMES.has(name)) return "secret";
	if (name.startsWith(".env.")) return KEPT_ENV_FILES.has(name) ? "none" : "secret";
	if (SECRET_FILE_EXTENSIONS.some((extension) => name.endsWith(extension))) return "secret";
	if (/^(?:client_secret.*|service-account.*)\.json$/.test(name)) return "secret";
	if (CONTENT_FILE_NAMES.has(name) || CONTENT_FILE_EXTENSIONS.some((extension) => name.endsWith(extension))) return "content";
	return "none";
}

const MAX_SECRET_CHECK_BYTES = 1024 * 1024;

const NPMRC_CREDENTIAL_KEY = /_(?:authToken|auth|password)$/i;

const NPMRC_ENV_REFERENCE = /^\$\{[A-Za-z_][A-Za-z0-9_]*\}$/;

const PEM_PRIVATE_BLOCK = /-----BEGIN [^\r\n-]*PRIVATE[^\r\n-]*-----/;

const PEM_PUBLIC_LABELS = "CERTIFICATE|TRUSTED CERTIFICATE|X509 CRL|PUBLIC KEY|RSA PUBLIC KEY|CERTIFICATE REQUEST|NEW CERTIFICATE REQUEST";

const PEM_PUBLIC_BLOCK = new RegExp(`-----BEGIN (?:${PEM_PUBLIC_LABELS})-----`);

const PEM_PUBLIC_BLOCKS = new RegExp(`-----BEGIN (${PEM_PUBLIC_LABELS})-----[\\s\\S]*?-----END \\1-----`, "g");

const BASE64_RUN = /^[A-Za-z0-9+/]{40,}={0,2}$/;

function keyMaterialWord(word: string): boolean {
	return BASE64_RUN.test(word) && /[A-Z]/.test(word) && /[a-z]/.test(word);
}

const SETTING_LINE = /^\s*(?:export\s+)?["']?([A-Za-z_][A-Za-z0-9_.-]*)["']?\s*=\s*\S/;

const CERTIFICATE_DESCRIPTIONS = new Set(["subject", "issuer", "serial", "notbefore", "notafter", "depth"]);

function notPlainText(content: string): boolean {
	return content.includes("\0") || content.startsWith("\uFFFD\uFFFD");
}

function unquoted(value: string): string {
	return value.trim().replace(/^(["'])(.*)\1$/, "$2").trim();
}

function npmrcHoldsCredential(content: string): boolean {
	for (const line of content.split(/\r\n|\r|\n/)) {
		const text = line.trim();
		if (text === "" || text.startsWith(";") || text.startsWith("#")) continue;
		const equals = text.indexOf("=");
		if (equals < 0 || !NPMRC_CREDENTIAL_KEY.test(unquoted(text.slice(0, equals)))) continue;
		const value = unquoted(text.slice(equals + 1));
		if (value !== "" && !NPMRC_ENV_REFERENCE.test(value)) return true;
	}
	return false;
}

function besidePublicBlocks(content: string): boolean {
	const rest = content.replace(PEM_PUBLIC_BLOCKS, "\n");
	return rest.split(/\r\n|\r|\n/).some((line) => {
		const setting = SETTING_LINE.exec(line);
		if (setting && !CERTIFICATE_DESCRIPTIONS.has((setting[1] ?? "").toLowerCase())) return true;
		return line.split(/\s+/).some(keyMaterialWord);
	});
}

export function contentHoldsSecret(relativePath: string, content: string): boolean {
	if (notPlainText(content) || PEM_PRIVATE_BLOCK.test(content)) return true;
	const name = relativePath.replace(/\\/g, "/").split("/").pop()?.toLowerCase() ?? "";
	if (CONTENT_FILE_NAMES.has(name)) return npmrcHoldsCredential(content);
	return !PEM_PUBLIC_BLOCK.test(content) || besidePublicBlocks(content);
}

async function folderFileHoldsSecret(folder: string, file: string): Promise<boolean> {
	try {
		const full = path.join(folder, file);
		const stat = await fs.lstat(full);
		if (!stat.isFile() || stat.size > MAX_SECRET_CHECK_BYTES) return true;
		return contentHoldsSecret(file, await fs.readFile(full, "utf8"));
	} catch {
		return true;
	}
}

function shownFiles(files: readonly string[]): string {
	return files.slice(0, 5).join(", ") + (files.length > 5 ? `, and ${files.length - 5} more` : "");
}

function secretFilesError(folderName: string, files: readonly string[]): PushFolderError {
	return new PushFolderError("secret_files", `${folderName}'s history holds files that look like secrets (${shownFiles(files)}), so Orchestrator does not upload it: they would reach GitHub. Remove them from its history, or push it to GitHub yourself and add its repository from GitHub.`, [...files]);
}

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

function unreadableRepositoryError(folder: string, error: unknown): PushFolderError {
	return new PushFolderError("import_failed", `git could not read ${path.basename(folder)} as a repository, so Orchestrator cannot tell whether to upload its commits or its files: ${gitMessage(error)}`);
}

async function ownRepositoryHead(folder: string, git: GitRunner, env: NodeJS.ProcessEnv): Promise<{ commit: string; branch?: string } | undefined> {
	try {
		await fs.lstat(path.join(folder, ".git"));
	} catch {
		return undefined;
	}
	let top: string;
	try {
		top = (await git(["-C", folder, "rev-parse", "--show-toplevel"], { env, timeout: 15_000 })).stdout.trim();
	} catch (error) {
		throw unreadableRepositoryError(folder, error);
	}
	const realTop = await fs.realpath(top).catch(() => top);
	if (!samePath(realTop, folder)) return undefined;
	let commit: string;
	try {
		commit = (await git(["-C", folder, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"], { env, timeout: 15_000 })).stdout.trim();
	} catch (error) {
		const { code, stderr } = error as { code?: unknown; stderr?: unknown };
		if (code === 1 && (typeof stderr !== "string" || stderr.trim() === "")) return undefined;
		throw unreadableRepositoryError(folder, error);
	}
	if (commit === "") return undefined;
	let branch: string | undefined;
	try {
		branch = (await git(["-C", folder, "symbolic-ref", "--quiet", "--short", "HEAD"], { env, timeout: 15_000 })).stdout.trim() || undefined;
	} catch {
	}
	return { commit, ...(branch ? { branch } : {}) };
}

function usesLfs(attributes: string): boolean {
	return /\bfilter\s*=\s*lfs\b/.test(attributes);
}

function largeFileError(folderName: string, file: string): PushFolderError {
	return new PushFolderError("file_too_large", `${folderName} holds ${file}, which is larger than GitHub's 100 MB file limit. Remove it from what you upload, then try again.`);
}

function lfsError(folderName: string): PushFolderError {
	return new PushFolderError("lfs_unsupported", `${folderName} stores files in Git LFS, which Orchestrator cannot upload yet. Push it to GitHub yourself, then add its repository from GitHub.`);
}

async function checkNotShallow(folder: string, git: GitRunner, env: NodeJS.ProcessEnv): Promise<void> {
	const shallow = (await git(["-C", folder, "rev-parse", "--is-shallow-repository"], { env, timeout: 15_000 })).stdout.trim();
	if (shallow === "true") {
		throw new PushFolderError("shallow_repository", `${path.basename(folder)} is a shallow clone, so its history cannot be uploaded. Run "git fetch --unshallow" in it, then try again.`);
	}
}

async function checkRepositoryCommit(folder: string, commit: string, git: GitRunner, env: NodeJS.ProcessEnv, maxFileBytes: number): Promise<void> {
	const name = path.basename(folder);
	const { stdout } = await git(["-C", folder, "ls-tree", "-r", "-l", "-z", commit], { env, timeout: GIT_STEP_TIMEOUT_MS });
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
		const usage = Number((await git(["-C", folder, "rev-list", "--objects", "--disk-usage", commit], { env, timeout: GIT_STEP_TIMEOUT_MS })).stdout.trim());
		if (usage > MAX_PUSH_BYTES) {
			throw new PushFolderError("folder_too_large", `${name}'s history is larger than the 2 GB Orchestrator Cloud uploads. Add its repository from GitHub instead.`);
		}
	} catch (error) {
		if (error instanceof PushFolderError) throw error;
	}
}

async function readBlobs(gitArgs: readonly string[], blobs: readonly string[], gitStream: GitStreamRunner, env: NodeJS.ProcessEnv, maxBytes = MAX_SECRET_CHECK_BYTES): Promise<Map<string, string | undefined>> {
	const contents = new Map<string, string | undefined>();
	if (blobs.length === 0) return contents;
	let pending = "";
	let current: { blob: string; remaining: number; keep: boolean; parts: string[] } | undefined;
	await gitStream([...gitArgs, "cat-file", "--batch"], { env, timeout: GIT_STEP_TIMEOUT_MS, input: blobs.map((blob) => `${blob}\n`).join(""), encoding: "latin1" }, (chunk) => {
		pending += chunk;
		for (;;) {
			if (current === undefined) {
				const end = pending.indexOf("\n");
				if (end < 0) return;
				const [name = "", type, size] = pending.slice(0, end).split(" ");
				pending = pending.slice(end + 1);
				const bytes = Number(size);
				if (type === undefined || size === undefined || !Number.isSafeInteger(bytes) || bytes < 0) {
					contents.set(name, undefined);
					continue;
				}
				current = { blob: name, remaining: bytes + 1, keep: type === "blob" && bytes <= maxBytes, parts: [] };
			}
			const taken = Math.min(current.remaining, pending.length);
			if (current.keep) current.parts.push(pending.slice(0, taken));
			pending = pending.slice(taken);
			current.remaining -= taken;
			if (current.remaining > 0) return;
			contents.set(current.blob, current.keep ? Buffer.from(current.parts.join("").slice(0, -1), "latin1").toString("utf8") : undefined);
			current = undefined;
		}
	});
	return contents;
}

function rawChange(header: string): { mode: string; blob: string; status: string } | undefined {
	const parents = /^:+/.exec(header)?.[0].length ?? 0;
	const fields = header.slice(parents).trim().split(" ");
	const [mode, blob, status] = [fields[parents], fields[2 * parents + 1], fields[2 * parents + 2]];
	return parents > 0 && mode && blob && status ? { mode, blob, status } : undefined;
}

const HISTORY_LOG_ARGS = ["log", "--format=", "--raw", "--no-abbrev", "--no-renames", "--root", "--diff-merges=separate", "--no-show-signature", "--no-notes", "--no-color", "--diff-filter=AMT", "-z"];

async function committedSecretFiles(folder: string, commit: string, gitStream: GitStreamRunner, env: NodeJS.ProcessEnv): Promise<string[]> {
	const secrets = new Set<string>();
	const toRead = new Map<string, Set<string>>();
	let change: ReturnType<typeof rawChange> | null | undefined;
	const take = (token: string) => {
		if (change === undefined) {
			const start = token.startsWith(":") ? 0 : token.lastIndexOf("\n:") + 1;
			if (start > 0 || token.startsWith(":")) change = rawChange(token.slice(start)) ?? null;
			return;
		}
		const header = change;
		change = undefined;
		const kind = secretPathKind(token);
		if (kind === "secret") secrets.add(token);
		if (kind !== "content") return;
		if (header === null) {
			secrets.add(token);
			return;
		}
		if (header.mode === "120000" || header.mode === "160000" || /^0+$/.test(header.blob)) return;
		const paths = toRead.get(header.blob) ?? new Set<string>();
		paths.add(token);
		toRead.set(header.blob, paths);
	};
	let rest = "";
	await gitStream(["-C", folder, ...HISTORY_LOG_ARGS, commit], { env, timeout: GIT_STEP_TIMEOUT_MS }, (chunk) => {
		const tokens = (rest + chunk).split("\0");
		rest = tokens.pop() ?? "";
		for (const token of tokens) if (token !== "") take(token);
	});
	if (rest !== "") take(rest);
	const unread = [...toRead].filter(([, paths]) => [...paths].some((file) => !secrets.has(file)));
	const contents = await readBlobs(["-C", folder], unread.map(([blob]) => blob), gitStream, env);
	for (const [blob, paths] of unread) {
		const content = contents.get(blob);
		for (const file of paths) if (content === undefined || contentHoldsSecret(file, content)) secrets.add(file);
	}
	return [...secrets].sort();
}

const CHECKED_COMMIT_TTL_MS = 10 * 60_000;
const MAX_CHECKED_COMMITS = 32;

const checkedCommits = new Map<string, number>();

function checkedCommitKey(folder: string, commit: string, maxFileBytes: number): string {
	return `${folder}\0${commit}\0${maxFileBytes}`;
}

function commitRecentlyChecked(key: string, now: number): boolean {
	const at = checkedCommits.get(key);
	if (at === undefined) return false;
	if (now >= at && now - at <= CHECKED_COMMIT_TTL_MS) return true;
	checkedCommits.delete(key);
	return false;
}

const runningCommitChecks = new Map<string, Promise<void>>();

function checkCommitOnce(key: string, check: () => Promise<void>): Promise<void> {
	const running = runningCommitChecks.get(key);
	if (running) return running;
	const started = check().finally(() => runningCommitChecks.delete(key));
	runningCommitChecks.set(key, started);
	return started;
}

function rememberCheckedCommit(key: string, now: number): void {
	checkedCommits.delete(key);
	checkedCommits.set(key, now);
	while (checkedCommits.size > MAX_CHECKED_COMMITS) {
		const oldest = checkedCommits.keys().next().value;
		if (oldest === undefined) break;
		checkedCommits.delete(oldest);
	}
}

const FILE_CHECK_CONCURRENCY = 32;

async function forEachLimited<T>(items: readonly T[], limit: number, work: (item: T) => Promise<void>, stop: () => boolean = () => false): Promise<void> {
	let next = 0;
	let failed = false;
	const worker = async () => {
		while (!failed && !stop() && next < items.length) {
			const item = items[next++] as T;
			try {
				await work(item);
			} catch (error) {
				failed = true;
				throw error;
			}
		}
	};
	await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
}

async function listImportFiles(folder: string, gitDir: string, git: GitRunner, env: NodeJS.ProcessEnv): Promise<string[]> {
	const { stdout } = await git(["--git-dir", gitDir, "--work-tree", folder, "ls-files", "-z", "--others", "--exclude-standard"], { env, timeout: GIT_STEP_TIMEOUT_MS });
	return stdout.split("\0").filter((file) => file !== "");
}

interface ImportScan {
	size: number;
	newestMs: number;
}

async function scanImport(folder: string, files: readonly string[], limit: number, maxFileBytes: number): Promise<ImportScan> {
	const name = path.basename(folder);
	const scan: ImportScan = { size: 0, newestMs: (await fs.stat(folder)).mtimeMs };
	await forEachLimited(files, FILE_CHECK_CONCURRENCY, async (file) => {
		const full = path.join(folder, file);
		let stat: Awaited<ReturnType<typeof fs.lstat>>;
		try {
			stat = await fs.lstat(full);
		} catch {
			return;
		}
		if (stat.size > maxFileBytes) throw largeFileError(name, file);
		scan.size += stat.size;
		scan.newestMs = Math.max(scan.newestMs, stat.mtimeMs);
		if (path.basename(file) === ".gitattributes" && usesLfs(await fs.readFile(full, "utf8").catch(() => ""))) throw lfsError(name);
	}, () => scan.size > limit);
	return scan;
}

async function stagedEntries(gitDir: string, folder: string, git: GitRunner, env: NodeJS.ProcessEnv): Promise<Array<{ mode: string; blob: string; file: string }>> {
	const { stdout } = await git(["--git-dir", gitDir, "--work-tree", folder, "ls-files", "--stage", "-z"], { env, timeout: GIT_STEP_TIMEOUT_MS });
	return stdout.split("\0").filter((entry) => entry !== "").map((entry) => {
		const tab = entry.indexOf("\t");
		const [mode = "", blob = ""] = entry.slice(0, tab).split(" ");
		return { mode, blob, file: entry.slice(tab + 1) };
	});
}

function embeddedRepositoriesError(folderName: string, repositories: readonly string[]): PushFolderError {
	const shown = repositories.slice(0, 3).join(", ") + (repositories.length > 3 ? `, and ${repositories.length - 3} more` : "");
	return new PushFolderError("folder_has_repositories", `${folderName} holds git repositories of its own (${shown}), whose files would not be uploaded. Add each of them to the workspace on its own, or add them to ${folderName}'s .gitignore.`);
}

async function requireUploadingGit(git: GitRunner, env: NodeJS.ProcessEnv): Promise<void> {
	let version: string;
	try {
		version = (await git(["--version"], { env, timeout: 15_000 })).stdout;
	} catch {
		throw new PushFolderError("git_unavailable", "git is not installed or not on PATH. Install git, then try again.");
	}
	if (!gitSupportsConfigEnv(version)) {
		throw new PushFolderError("git_unavailable", `Uploading a folder needs git 2.31 or newer; this computer has ${version.trim() || "an older git"}. Update git, then try again.`);
	}
}

type FolderUploadPlan =
	| { realFolder: string; source: "head"; commit: string; branch?: string; leftOut: string[] }
	| { realFolder: string; source: "import"; gitDir: string; kept: string[]; leftOut: string[]; newestMs: number };

async function planFolderUpload(folder: string, deps: PushFolderDeps, git: GitRunner, env: NodeJS.ProcessEnv, makeGitDir: () => Promise<string>): Promise<FolderUploadPlan> {
	const realFolder = await resolveFolder(folder, deps.homeDir ?? os.homedir(), appStateDirs(deps));
	await requireUploadingGit(git, env);
	const name = path.basename(realFolder);
	const maxFileBytes = deps.maxFileBytes ?? MAX_GITHUB_FILE_BYTES;
	const now = deps.now ?? Date.now;
	try {
		const head = await ownRepositoryHead(realFolder, git, env);
		if (head) {
			const { commit, branch } = head;
			await checkNotShallow(realFolder, git, env);
			const key = checkedCommitKey(realFolder, commit, maxFileBytes);
			if (!commitRecentlyChecked(key, now())) {
				await checkCommitOnce(key, async () => {
					await checkRepositoryCommit(realFolder, commit, git, env, maxFileBytes);
					const secrets = await committedSecretFiles(realFolder, commit, deps.gitStream ?? defaultGitStream, env);
					if (secrets.length > 0) throw secretFilesError(name, secrets);
					rememberCheckedCommit(key, now());
				});
			}
			return { realFolder, source: "head", commit, ...(branch ? { branch } : {}), leftOut: [] };
		}
		const gitDir = await makeGitDir();
		await git(["init", "--quiet", "--bare", gitDir], { env, timeout: GIT_STEP_TIMEOUT_MS });
		const listed = await listImportFiles(realFolder, gitDir, git, env);
		const decided = new Map<string, boolean>();
		await forEachLimited(listed.filter((file) => secretPathKind(file) === "content"), FILE_CHECK_CONCURRENCY, async (file) => {
			decided.set(file, await folderFileHoldsSecret(realFolder, file));
		});
		const holdsSecret = (file: string) => {
			const kind = secretPathKind(file);
			return kind === "secret" || (kind === "content" && decided.get(file) !== false);
		};
		const leftOut = listed.filter(holdsSecret);
		const kept = listed.filter((file) => !holdsSecret(file));
		const nested = kept.filter((file) => file.endsWith("/")).map((file) => file.slice(0, -1));
		if (nested.length > 0) throw embeddedRepositoriesError(name, nested);
		const limit = deps.maxImportBytes ?? MAX_FOLDER_IMPORT_BYTES;
		const { size, newestMs } = await scanImport(realFolder, kept, limit, maxFileBytes);
		if (size > limit) {
			throw new PushFolderError("folder_too_large", `${name} holds more than ${Math.round(limit / (1024 * 1024 * 1024) * 10) / 10} GB of files git does not ignore. Add the large files to its .gitignore, or make it a git repository and add it as a repository.`);
		}
		return { realFolder, source: "import", gitDir, kept, leftOut, newestMs };
	} catch (error) {
		if (error instanceof PushFolderError) throw error;
		throw new PushFolderError("import_failed", `Could not prepare ${name}: ${gitMessage(error)}`);
	}
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

async function commitImport(plan: Extract<FolderUploadPlan, { source: "import" }>, git: GitRunner, gitStream: GitStreamRunner, env: NodeJS.ProcessEnv): Promise<string> {
	const { realFolder: folder, gitDir, kept, newestMs } = plan;
	const scoped = ["--git-dir", gitDir, "--work-tree", folder];
	if (kept.length > 0) {
		const pathspecs = path.join(gitDir, "ao-import-paths");
		await fs.writeFile(pathspecs, `${kept.join("\0")}\0`);
		await git([...scoped, "add", `--pathspec-from-file=${pathspecs}`, "--pathspec-file-nul"], { env: { ...env, GIT_LITERAL_PATHSPECS: "1" }, timeout: GIT_STEP_TIMEOUT_MS });
	}
	const staged = await stagedEntries(gitDir, folder, git, env);
	const reread = staged.filter((entry) => entry.mode !== "160000" && secretPathKind(entry.file) === "content");
	const contents = await readBlobs(["--git-dir", gitDir], [...new Set(reread.map((entry) => entry.blob))], gitStream, env);
	for (const { file, blob } of reread) {
		const content = contents.get(blob);
		if (content === undefined || contentHoldsSecret(file, content)) {
			throw new PushFolderError("import_failed", `${file} in ${path.basename(folder)} changed while it was being uploaded and now may hold a credential, so nothing was uploaded. Try again.`);
		}
	}
	const embedded = staged.filter((entry) => entry.mode === "160000").map((entry) => entry.file);
	if (embedded.length > 0) throw embeddedRepositoriesError(path.basename(folder), embedded);
	const date = `@${Math.floor(newestMs / 1000)} +0000`;
	const dated = { ...env, GIT_AUTHOR_DATE: date, GIT_COMMITTER_DATE: date };
	const identity = (await hasConfiguredIdentity(folder, git, env))
		? dated
		: { ...dated, GIT_AUTHOR_NAME: FALLBACK_COMMIT_NAME, GIT_AUTHOR_EMAIL: FALLBACK_COMMIT_EMAIL, GIT_COMMITTER_NAME: FALLBACK_COMMIT_NAME, GIT_COMMITTER_EMAIL: FALLBACK_COMMIT_EMAIL };
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
		if (!controlPlane) throw new PushFolderError("invalid_request", "No Orchestrator Cloud control plane is configured.");
		const remoteUrl = resolvePushUrl(pushPath, controlPlane).toString();

		const plan = await planFolderUpload(folder, deps, git, env, async () => {
			await fs.mkdir(deps.tmpRoot(), { recursive: true });
			gitDir = path.join(deps.tmpRoot(), `import-${randomUUID()}`);
			return gitDir;
		});
		const { realFolder, source, leftOut } = plan;
		let commit: string;
		const pushArgs: string[] = [];
		if (plan.source === "head") {
			commit = plan.commit;
			pushArgs.push("-C", realFolder);
		} else {
			try {
				commit = await commitImport(plan, git, deps.gitStream ?? defaultGitStream, env);
			} catch (error) {
				if (error instanceof PushFolderError) throw error;
				throw new PushFolderError("import_failed", `Could not prepare ${path.basename(realFolder)}: ${gitMessage(error)}`);
			}
			pushArgs.push("--git-dir", plan.gitDir);
		}

		try {
			token = await deps.getToken();
		} catch {
			throw new PushFolderError("signed_out", "Your Orchestrator Cloud session could not be read. Sign in again, then try again.");
		}
		if (token === null || token === "") throw new PushFolderError("signed_out", "No Orchestrator Cloud session is available. Sign in and try again.");
		const remote = deps.remoteFor?.(remoteUrl) ?? remoteUrl;
		const pushEnv = withGitConfig(env, [
			[`http.${remoteUrl}.extraHeader`, ""],
			[`http.${remoteUrl}.extraHeader`, `Authorization: Bearer ${token}`],
			[`http.${remoteUrl}.followRedirects`, "false"],
			[`http.${remoteUrl}.sslVerify`, "true"],
			["credential.helper", ""],
			["core.askPass", ""],
		]);
		try {
			await git([...pushArgs, "push", "--quiet", "--no-verify", remote, `${commit}:refs/heads/${branch}`], { env: pushEnv, timeout: GIT_PUSH_TIMEOUT_MS });
		} catch (error) {
			throw pushRefusal(error, path.basename(realFolder), branch);
		}
		return { status: "pushed", branch, commit, source, leftOut };
	} catch (error) {
		if (error instanceof PushFolderError) return failure(error, redact(error.message, token));
		return { status: "error", code: "invalid_request", message: redact(error instanceof Error ? error.message : String(error), token) };
	} finally {
		if (gitDir) await fs.rm(gitDir, { recursive: true, force: true }).catch(() => undefined);
	}
}

function appStateDirs(deps: Pick<PushFolderDeps, "appStateDirs" | "homeDir">): string[] {
	return deps.appStateDirs?.() ?? [path.join(deps.homeDir ?? os.homedir(), ".ao")];
}

export async function previewFolderUpload(input: unknown, deps: PushFolderDeps): Promise<PushFolderPreview> {
	const git = deps.git ?? defaultGit;
	const env = quietGitEnv(deps.env?.() ?? process.env);
	let gitDir: string | undefined;
	try {
		const folder = (typeof input === "object" && input !== null ? (input as Record<string, unknown>).folder : undefined);
		if (typeof folder !== "string") throw new PushFolderError("invalid_request", "Not a folder to upload.");
		const plan = await planFolderUpload(folder, deps, git, env, async () => {
			await fs.mkdir(deps.tmpRoot(), { recursive: true });
			gitDir = path.join(deps.tmpRoot(), `preview-${randomUUID()}`);
			return gitDir;
		});
		return { status: "ok", source: plan.source, ...(plan.source === "head" && plan.branch ? { branch: plan.branch } : {}), leftOut: plan.leftOut };
	} catch (error) {
		if (error instanceof PushFolderError) return failure(error);
		return { status: "error", code: "import_failed", message: error instanceof Error ? error.message : String(error) };
	} finally {
		if (gitDir) await fs.rm(gitDir, { recursive: true, force: true }).catch(() => undefined);
	}
}

const STALE_IMPORT_MS = 2 * GIT_PUSH_TIMEOUT_MS;

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

const chosenFolders = new Set<string>();

export function rememberChosenFolder(folder: string): void {
	chosenFolders.add(path.resolve(folder));
}

export function isChosenFolder(folder: string): boolean {
	return chosenFolders.has(path.resolve(folder));
}

function fromMainFrame(event: unknown): boolean {
	const { senderFrame } = (event ?? {}) as { senderFrame?: { parent: unknown } | null };
	return !(senderFrame === null || (senderFrame && senderFrame.parent !== null));
}

export interface CloudFolderPushIpcDeps extends PushFolderDeps {
	ready?: () => Promise<void>;
	trustedSender?: (sender: unknown) => boolean;
	chosenFolder?: (folder: string) => boolean;
}

export function registerCloudFolderPushIpc(ipcMain: { handle(channel: string, handler: (event: unknown, input: unknown) => unknown): void }, deps: CloudFolderPushIpcDeps): void {
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
