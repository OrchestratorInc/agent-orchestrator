// @vitest-environment node
import { execFile } from "node:child_process";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({ ipcMain: {} }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: vi.fn(async () => "token-1") }));

import {
	clearFolderImports,
	contentHoldsSecret,
	CLOUD_PREVIEW_FOLDER_CHANNEL,
	CLOUD_PUSH_FOLDER_CHANNEL,
	FALLBACK_COMMIT_EMAIL,
	gitSupportsConfigEnv,
	type GitRunner,
	type GitStreamRunner,
	isChosenFolder,
	isValidPushBranch,
	pathWithin,
	previewFolderUpload,
	pushFolderToRepository,
	type PushFolderDeps,
	type PushFolderErrorCode,
	registerCloudFolderPushIpc,
	rememberChosenFolder,
	resolvePushUrl,
	secretPathKind,
} from "./cloud-folder-push";

const execFileAsync = promisify(execFile);
const CP = "https://cp.example.com";
const PUSH_PATH = "/api/cloud/v1/orgs/org-1/github/git/acme/notes.git";

// No user or system config leaks into the fixture repositories.
const gitEnv = { ...process.env, GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: "/dev/null", HOME: os.tmpdir() };

async function git(cwd: string, ...args: string[]): Promise<string> {
	const { stdout } = await execFileAsync("git", args, { cwd, env: gitEnv });
	return stdout.trim();
}

let root: string;
let folder: string;
let remote: string;
let tmpRoot: string;

beforeEach(async () => {
	root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), "ao-folder-push-")));
	folder = path.join(root, "notes");
	remote = path.join(root, "remote.git");
	tmpRoot = path.join(root, "state", "folder-imports");
	await fs.mkdir(folder);
	await git(root, "init", "--quiet", "--bare", remote);
});

afterEach(async () => {
	await fs.rm(root, { recursive: true, force: true });
});

function deps(overrides: Partial<PushFolderDeps> = {}): PushFolderDeps & { calls: Array<{ args: string[]; env: NodeJS.ProcessEnv }> } {
	const calls: Array<{ args: string[]; env: NodeJS.ProcessEnv }> = [];
	const runner: GitRunner = async (args, options) => {
		calls.push({ args, env: options.env });
		// The app's own runner holds up to 64 MB of output.
		const { stdout, stderr } = await execFileAsync("git", args, { cwd: options.cwd, env: options.env, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
		return { stdout, stderr };
	};
	return {
		controlPlaneUrl: async () => CP,
		getToken: async () => "token-1",
		tmpRoot: () => tmpRoot,
		env: () => gitEnv,
		homeDir: path.join(root, "home"),
		git: runner,
		remoteFor: () => remote,
		calls,
		...overrides,
	};
}

describe("resolvePushUrl", () => {
	it("joins a repository's push path with the configured control plane", () => {
		expect(resolvePushUrl(PUSH_PATH, CP).toString()).toBe(`${CP}${PUSH_PATH}`);
		expect(resolvePushUrl(PUSH_PATH, "https://cp.example.com/base/").toString()).toBe(`https://cp.example.com/base${PUSH_PATH}`);
	});

	it("refuses anything but a repository's git endpoint", () => {
		for (const pushPath of [
			"https://evil.example.com/api/cloud/v1/orgs/o/github/git/a/b.git",
			"//evil.example.com/api/cloud/v1/orgs/o/github/git/a/b.git",
			"/api/cloud/v1/orgs/o/sessions",
			"/api/cloud/v1/orgs/o/github/git/a/../b.git",
			"/api/cloud/v1/orgs/o/github/git/a/b.git?x=1",
		]) {
			expect(() => resolvePushUrl(pushPath, CP), pushPath).toThrow();
		}
		expect(() => resolvePushUrl(PUSH_PATH, "http://cp.example.com")).toThrow();
	});

	it("needs a git that reads config from the environment", () => {
		expect(gitSupportsConfigEnv("git version 2.39.5 (Apple Git-154)")).toBe(true);
		expect(gitSupportsConfigEnv("git version 2.31.0.windows.1")).toBe(true);
		expect(gitSupportsConfigEnv("git version 2.30.2")).toBe(false);
		expect(gitSupportsConfigEnv("")).toBe(false);
	});

	it("checks branch names", () => {
		expect(isValidPushBranch("main")).toBe(true);
		expect(isValidPushBranch("release/v1")).toBe(true);
		for (const branch of ["", "-x", "a..b/.c", "a b", "x.lock", "a//b"]) expect(isValidPushBranch(branch), branch).toBe(false);
	});
});

describe("pushFolderToRepository", () => {
	it("imports a plain folder honoring its .gitignore without touching it", async () => {
		await fs.writeFile(path.join(folder, "README.md"), "# notes\n");
		await fs.mkdir(path.join(folder, "node_modules"));
		await fs.writeFile(path.join(folder, "node_modules", "big.js"), "ignored");
		await fs.writeFile(path.join(folder, ".gitignore"), "node_modules\n");
		const before = (await fs.readdir(folder)).sort();
		const d = deps();

		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, d);

		expect(result).toMatchObject({ status: "pushed", branch: "main", source: "import" });
		expect(await git(remote, "ls-tree", "-r", "--name-only", "main")).toBe(".gitignore\nREADME.md");
		// Without a configured identity the commit is AO's.
		expect(await git(remote, "log", "-1", "--format=%ae", "main")).toBe(FALLBACK_COMMIT_EMAIL);
		expect((await fs.readdir(folder)).sort()).toEqual(before);
		// The import's git directory is gone.
		expect(await fs.readdir(tmpRoot)).toEqual([]);
	});

	it("sends the token only through git's environment, scoped to the push URL", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		const d = deps();

		await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, d);

		const push = d.calls.find((call) => call.args.includes("push"));
		expect(push?.args.join(" ")).not.toContain("token-1");
		expect(push?.args.at(-1)).toMatch(/^[0-9a-f]{40}:refs\/heads\/main$/);
		const config = Object.fromEntries(Array.from({ length: Number(push?.env.GIT_CONFIG_COUNT) }, (_, i) => [push?.env[`GIT_CONFIG_KEY_${i}`], push?.env[`GIT_CONFIG_VALUE_${i}`]]));
		expect(config).toMatchObject({
			[`http.${CP}${PUSH_PATH}.extraHeader`]: "Authorization: Bearer token-1",
			// Scoped to the push URL like the header, so no other config can turn redirects back on for it.
			[`http.${CP}${PUSH_PATH}.followRedirects`]: "false",
			"credential.helper": "",
		});
		expect(config).not.toHaveProperty(["http.followRedirects"]);
		expect(push?.env.GIT_TERMINAL_PROMPT).toBe("0");
		// Only the push step ever sees the token.
		expect(d.calls.filter((call) => JSON.stringify(call.env).includes("token-1"))).toHaveLength(1);
	});

	it("pushes the checked-out commit of a folder that is its own repository", async () => {
		await git(folder, "init", "--quiet");
		await fs.writeFile(path.join(folder, "tracked.txt"), "1");
		await git(folder, "add", "tracked.txt");
		await git(folder, "-c", "user.name=Dev", "-c", "user.email=dev@example.com", "commit", "--quiet", "-m", "first");
		await fs.writeFile(path.join(folder, "uncommitted.txt"), "2");
		const head = await git(folder, "rev-parse", "HEAD");

		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "trunk" }, deps());

		expect(result).toEqual({ status: "pushed", branch: "trunk", commit: head, source: "head", leftOut: [] });
		expect(await git(remote, "rev-parse", "trunk")).toBe(head);
		expect(await git(folder, "status", "--porcelain")).toBe("?? uncommitted.txt");
	});

	it("imports a repository without commits instead of pushing nothing", async () => {
		await git(folder, "init", "--quiet");
		await fs.writeFile(path.join(folder, "draft.txt"), "draft");

		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());

		expect(result).toMatchObject({ status: "pushed", source: "import" });
		expect(await git(remote, "ls-tree", "-r", "--name-only", "main")).toBe("draft.txt");
		await expect(git(folder, "rev-parse", "--verify", "HEAD")).rejects.toThrow();
	});

	it("clears extra headers from the user's own config before adding the token's", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		const d = deps();

		await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, d);

		const push = d.calls.find((call) => call.args.includes("push"));
		const entries = Array.from({ length: Number(push?.env.GIT_CONFIG_COUNT) }, (_, i) => [push?.env[`GIT_CONFIG_KEY_${i}`], push?.env[`GIT_CONFIG_VALUE_${i}`]]);
		const headers = entries.filter(([key]) => key === `http.${CP}${PUSH_PATH}.extraHeader`).map(([, value]) => value);
		expect(headers).toEqual(["", "Authorization: Bearer token-1"]);
	});

	it("refuses to import a folder holding repositories of its own", async () => {
		const nested = path.join(folder, "lib");
		await fs.mkdir(nested);
		await git(nested, "init", "--quiet");
		await fs.writeFile(path.join(nested, "index.js"), "1");
		await git(nested, "add", "index.js");
		await git(nested, "-c", "user.name=Dev", "-c", "user.email=dev@example.com", "commit", "--quiet", "-m", "first");
		await fs.writeFile(path.join(folder, "README.md"), "# notes\n");

		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());

		expect(result).toMatchObject({ status: "error", code: "folder_has_repositories" });
		expect(result.status === "error" && result.message).toContain("(lib)");
		await expect(git(remote, "rev-parse", "--verify", "main")).rejects.toThrow();
		expect(await fs.readdir(tmpRoot)).toEqual([]);
	});

	it("refuses an import over the size limit before committing", async () => {
		await fs.writeFile(path.join(folder, "big.bin"), Buffer.alloc(2048));

		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps({ maxImportBytes: 1024 }));

		expect(result).toMatchObject({ status: "error", code: "folder_too_large" });
		await expect(git(remote, "rev-parse", "--verify", "main")).rejects.toThrow();
	});

	it("refuses a changed folder into a branch an earlier upload made, without the token", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());
		await fs.writeFile(path.join(folder, "b.txt"), "b");

		// The branch exists now; git refuses on its own ("fetch first").
		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());

		expect(result).toMatchObject({ status: "error", code: "branch_exists" });
		expect(JSON.stringify(result)).not.toContain("token-1");
	});

	it("retries an unchanged import to the commit already uploaded", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		const first = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());
		// A push whose reply was lost: the retry makes the same commit and finds it there.
		const retry = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());

		expect(first).toMatchObject({ status: "pushed", source: "import" });
		expect(retry).toMatchObject({ status: "pushed", commit: first.status === "pushed" ? first.commit : "" });
	});

	it("reports git's own failure, without the token", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		const base = deps();
		const git: GitRunner = async (args, options) => {
			if (args.includes("push")) throw Object.assign(new Error("git push failed"), { stderr: "fatal: unable to access: Could not resolve host token-1" });
			return base.git!(args, options);
		};

		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, { ...base, git });

		expect(result).toMatchObject({ status: "error", code: "push_failed" });
		expect(result.status === "error" && result.message).toMatch(/Could not upload notes/);
		expect(JSON.stringify(result)).not.toContain("token-1");
	});

	it.each([
		["fatal: unable to access 'https://cp.example.com/x.git/': The requested URL returned error: 401", "signed_out", /Sign in again/],
		// What git really prints for a 401 with prompts disabled.
		["fatal: could not read Username for 'https://cp.example.com': terminal prompts disabled", "signed_out", /Sign in again/],
		["fatal: Authentication failed for 'https://cp.example.com/x.git/'", "signed_out", /Sign in again/],
		["fatal: unable to access 'https://cp.example.com/x.git/': The requested URL returned error: 403", "push_forbidden", /did not let you upload notes/],
		[" ! [remote rejected] main -> main (AO only adds new branches here, and refs/heads/main already exists)", "branch_exists", /already has a main branch/],
		[" ! [rejected]        abc -> main (fetch first)\nerror: failed to push some refs", "branch_exists", /already has a main branch/],
		["fatal: repository 'https://cp.example.com/x.git/' not found", "push_forbidden", /did not let you upload notes/],
		["remote: error: GH001: Large files detected.\nremote: error: File big.bin is 120.00 MB; this exceeds GitHub's file size limit of 100.00 MB\n ! [remote rejected] main -> main (pre-receive hook declined)\nerror: failed to push some refs\nhint: a\nhint: b", "file_too_large", /larger than 100 MB/],
		[" ! [remote rejected] main -> main (refusing to allow a GitHub App to create or update workflow `.github/workflows/ci.yml` without `workflows` permission)", "workflows_refused", /workflows/],
	])("explains the control plane's refusal %#", async (stderr, code, message) => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		const base = deps();
		const git: GitRunner = async (args, options) => {
			if (args.includes("push")) throw Object.assign(new Error("git push failed"), { stderr });
			return base.git!(args, options);
		};

		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, { ...base, git });

		expect(result).toMatchObject({ status: "error", code });
		expect(result.status === "error" && result.message).toMatch(message);
	});

	it("refuses a push path off the control plane before reading the token", async () => {
		const getToken = vi.fn(async () => "token-1");
		const result = await pushFolderToRepository({ folder, pushPath: "https://evil.example.com/x.git", branch: "main" }, deps({ getToken }));
		expect(result).toMatchObject({ status: "error", code: "invalid_request" });
		expect(getToken).not.toHaveBeenCalled();
	});

	it("refuses the home folder, a relative path, and a missing folder", async () => {
		const home = path.join(root, "home");
		await fs.mkdir(home);
		for (const [target, code] of [[home, "folder_unavailable"], ["notes", "invalid_request"], [path.join(root, "missing"), "folder_unavailable"]] as const) {
			expect(await pushFolderToRepository({ folder: target, pushPath: PUSH_PATH, branch: "main" }, deps())).toMatchObject({ status: "error", code });
		}
	});

	it("reports a signed-out app", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps({ getToken: async () => null }));
		expect(result).toMatchObject({ status: "error", code: "signed_out" });
		expect(await fs.readdir(tmpRoot)).toEqual([]);
	});

	it("reports a session that cannot be read or refreshed as signed out", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		const d = deps({ getToken: async () => { throw new Error("refresh failed: invalid_grant"); } });
		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, d);
		expect(result).toMatchObject({ status: "error", code: "signed_out", message: expect.stringMatching(/Sign in again/) });
		expect(d.calls.some((call) => call.args.includes("push"))).toBe(false);
		expect(await fs.readdir(tmpRoot)).toEqual([]);
	});

	it("clears import directories left by a crash, not one another app is using", async () => {
		await fs.mkdir(path.join(tmpRoot, "import-old"), { recursive: true });
		await fs.mkdir(path.join(tmpRoot, "import-new"), { recursive: true });
		const hourAgo = new Date(Date.now() - 2 * 60 * 60_000);
		await fs.utimes(path.join(tmpRoot, "import-old"), hourAgo, hourAgo);

		await clearFolderImports(tmpRoot);

		expect(await fs.readdir(tmpRoot)).toEqual(["import-new"]);
		await expect(clearFolderImports(path.join(root, "missing"))).resolves.toBeUndefined();
	});

	it("refuses a shallow clone", async () => {
		const source = path.join(root, "source");
		await fs.mkdir(source);
		await git(source, "init", "--quiet", "--initial-branch=main");
		for (const n of [1, 2]) {
			await fs.writeFile(path.join(source, "a.txt"), String(n));
			await git(source, "add", ".");
			await git(source, "-c", "user.name=T", "-c", "user.email=t@e", "commit", "--quiet", "-m", `c${n}`);
		}
		await fs.rm(folder, { recursive: true });
		await git(root, "clone", "--quiet", "--depth", "1", `file://${source}`, folder);

		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());

		expect(result).toMatchObject({ status: "error", code: "shallow_repository" });
	});

	it("refuses a repository folder whose commit holds submodules or Git LFS", async () => {
		await git(folder, "init", "--quiet", "--initial-branch=main");
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		await git(folder, "add", ".");
		await git(folder, "update-index", "--add", "--cacheinfo", `160000,${"1".repeat(40)},vendor/lib`);
		await git(folder, "-c", "user.name=T", "-c", "user.email=t@e", "commit", "--quiet", "-m", "first");
		expect(await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps())).toMatchObject({ status: "error", code: "folder_has_repositories" });

		await git(folder, "rm", "--quiet", "--cached", "vendor/lib");
		await fs.writeFile(path.join(folder, ".gitattributes"), "*.psd filter=lfs diff=lfs merge=lfs -text\n");
		await git(folder, "add", ".gitattributes");
		await git(folder, "-c", "user.name=T", "-c", "user.email=t@e", "commit", "--quiet", "-m", "lfs");
		expect(await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps())).toMatchObject({ status: "error", code: "lfs_unsupported" });
		await expect(git(remote, "rev-parse", "--verify", "main")).rejects.toThrow();
	});

	it("refuses an import using Git LFS before adding anything", async () => {
		await fs.mkdir(path.join(folder, "art"));
		await fs.writeFile(path.join(folder, "art", ".gitattributes"), "*.psd filter=lfs -text\n");
		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());
		expect(result).toMatchObject({ status: "error", code: "lfs_unsupported" });
	});

	it("refuses a file over GitHub's limit, in an import and in a repository's commit", async () => {
		await fs.writeFile(path.join(folder, "big.bin"), Buffer.alloc(2048));
		expect(await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps({ maxFileBytes: 1024 }))).toMatchObject({ status: "error", code: "file_too_large" });

		await git(folder, "init", "--quiet", "--initial-branch=main");
		await git(folder, "add", ".");
		await git(folder, "-c", "user.name=T", "-c", "user.email=t@e", "commit", "--quiet", "-m", "big");
		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps({ maxFileBytes: 1024 }));
		expect(result).toMatchObject({ status: "error", code: "file_too_large" });
		expect(result.status === "error" && result.message).toMatch(/big\.bin/);
	});

	it("keeps the token off unverified TLS and out of traces", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		const options = deps({ env: () => ({ ...gitEnv, GIT_SSL_NO_VERIFY: "1", GIT_TRACE: "1", GIT_TRACE_CURL: "/tmp/x", GIT_CURL_VERBOSE: "1" }) });

		expect(await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, options)).toMatchObject({ status: "pushed" });

		const push = options.calls.find((call) => call.args.includes("push"))!;
		for (const key of ["GIT_SSL_NO_VERIFY", "GIT_TRACE", "GIT_TRACE_CURL", "GIT_CURL_VERBOSE"]) expect(push.env[key]).toBeUndefined();
		const config = Object.entries(push.env).filter(([key]) => key.startsWith("GIT_CONFIG_KEY_")).map(([key, value]) => `${value}=${push.env[key.replace("KEY", "VALUE")]}`);
		expect(config).toContain(`http.${CP}${PUSH_PATH}.sslVerify=true`);
	});
});

async function commitAll(dir: string, message = "commit"): Promise<void> {
	await git(dir, "add", "-A");
	await git(dir, "-c", "user.name=T", "-c", "user.email=t@e", "commit", "--quiet", "-m", message);
}

type StreamOptions = { cwd?: string; env: NodeJS.ProcessEnv; input?: string; encoding?: "utf8" | "latin1" };

/** git's whole output, as a stream runner would hand it on: fed its input, in its encoding. */
async function streamOutput(args: string[], options: StreamOptions): Promise<string> {
	const run = execFileAsync("git", args, { cwd: options.cwd, env: options.env, encoding: "buffer", maxBuffer: 64 * 1024 * 1024 });
	// git can exit before it reads stdin it never needed (`git log`); the write
	// then fails with EPIPE, which the runner ignores (cloud-folder-push.ts).
	run.child.stdin?.on("error", () => undefined);
	run.child.stdin?.end(options.input ?? "");
	return (await run).stdout.toString(options.encoding ?? "utf8");
}

describe("previewFolderUpload", () => {
	// Each folder an upload refuses, set up in the test's folder; setup returns
	// the dependencies both the preview and the upload run with.
	const refused: Array<[string, () => Promise<Partial<PushFolderDeps>>, PushFolderErrorCode]> = [
		["a shallow clone", async () => {
			const source = path.join(root, "source");
			await fs.mkdir(source);
			await git(source, "init", "--quiet", "--initial-branch=main");
			for (const n of [1, 2]) {
				await fs.writeFile(path.join(source, "a.txt"), String(n));
				await commitAll(source, `c${n}`);
			}
			await fs.rm(folder, { recursive: true });
			await git(root, "clone", "--quiet", "--depth", "1", `file://${source}`, folder);
			return {};
		}, "shallow_repository"],
		["a repository whose commit holds a submodule", async () => {
			await git(folder, "init", "--quiet", "--initial-branch=main");
			await fs.writeFile(path.join(folder, "a.txt"), "a");
			await git(folder, "add", ".");
			await git(folder, "update-index", "--add", "--cacheinfo", `160000,${"1".repeat(40)},vendor/lib`);
			await git(folder, "-c", "user.name=T", "-c", "user.email=t@e", "commit", "--quiet", "-m", "first");
			return {};
		}, "folder_has_repositories"],
		["a repository using Git LFS", async () => {
			await git(folder, "init", "--quiet", "--initial-branch=main");
			await fs.writeFile(path.join(folder, ".gitattributes"), "*.psd filter=lfs diff=lfs merge=lfs -text\n");
			await commitAll(folder);
			return {};
		}, "lfs_unsupported"],
		["a repository whose commit holds a file over GitHub's limit", async () => {
			await git(folder, "init", "--quiet", "--initial-branch=main");
			await fs.writeFile(path.join(folder, "big.bin"), Buffer.alloc(2048));
			await commitAll(folder);
			return { maxFileBytes: 1024 };
		}, "file_too_large"],
		["a folder using Git LFS", async () => {
			await fs.mkdir(path.join(folder, "art"));
			await fs.writeFile(path.join(folder, "art", ".gitattributes"), "*.psd filter=lfs -text\n");
			return {};
		}, "lfs_unsupported"],
		["a folder holding a file over GitHub's limit", async () => {
			await fs.writeFile(path.join(folder, "big.bin"), Buffer.alloc(2048));
			return { maxFileBytes: 1024 };
		}, "file_too_large"],
		["a folder holding a repository of its own", async () => {
			const nested = path.join(folder, "lib");
			await fs.mkdir(nested);
			await git(nested, "init", "--quiet");
			await fs.writeFile(path.join(nested, "index.js"), "1");
			await commitAll(nested);
			await fs.writeFile(path.join(folder, "README.md"), "# notes\n");
			return {};
		}, "folder_has_repositories"],
		["a folder holding an empty repository of its own", async () => {
			await fs.mkdir(path.join(folder, "scratch"));
			await git(path.join(folder, "scratch"), "init", "--quiet");
			await fs.writeFile(path.join(folder, "README.md"), "# notes\n");
			return {};
		}, "folder_has_repositories"],
		["a folder over the import limit", async () => {
			await fs.writeFile(path.join(folder, "big.bin"), Buffer.alloc(2048));
			return { maxImportBytes: 1024 };
		}, "folder_too_large"],
		["any folder, with a git too old to upload it", async () => {
			await fs.writeFile(path.join(folder, "a.txt"), "a");
			const oldGit: GitRunner = async (args, options) => {
				if (args[0] === "--version") return { stdout: "git version 2.30.2\n", stderr: "" };
				const { stdout, stderr } = await execFileAsync("git", args, { cwd: options.cwd, env: options.env, encoding: "utf8" });
				return { stdout, stderr };
			};
			return { git: oldGit };
		}, "git_unavailable"],
	];

	it.each(refused)("refuses %s, as the upload does, before any repository is made", async (_name, setup, code) => {
		const overrides = await setup();

		const preview = await previewFolderUpload({ folder }, deps(overrides));
		const upload = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps(overrides));

		expect(preview).toMatchObject({ status: "error", code });
		expect(upload).toMatchObject({ status: "error", code });
		expect(await git(remote, "branch", "--list")).toBe("");
		expect(await fs.readdir(tmpRoot).catch(() => [])).toEqual([]);
	});

	it("passes a folder and a repository the upload takes, naming the branch a repository uploads", async () => {
		await fs.writeFile(path.join(folder, "README.md"), "# notes\n");
		expect(await previewFolderUpload({ folder }, deps())).toEqual({ status: "ok", source: "import", leftOut: [] });

		await git(folder, "init", "--quiet", "--initial-branch=draft");
		await commitAll(folder);
		expect(await previewFolderUpload({ folder }, deps())).toEqual({ status: "ok", source: "head", branch: "draft", leftOut: [] });
		await git(folder, "checkout", "--quiet", "--detach");
		expect(await previewFolderUpload({ folder }, deps())).toEqual({ status: "ok", source: "head", leftOut: [] });
	});

	it("imports a repository without commits and a folder inside another repository", async () => {
		await git(folder, "init", "--quiet");
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		expect(await previewFolderUpload({ folder }, deps())).toMatchObject({ status: "ok", source: "import" });

		await commitAll(folder);
		const inner = path.join(folder, "inner");
		await fs.mkdir(inner);
		await fs.writeFile(path.join(inner, "b.txt"), "b");
		expect(await previewFolderUpload({ folder: inner }, deps())).toMatchObject({ status: "ok", source: "import" });
	});

	it("refuses a repository git cannot read instead of importing its files", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		// A .git git cannot use, as with "detected dubious ownership".
		await fs.writeFile(path.join(folder, ".git"), `gitdir: ${path.join(root, "missing.git")}\n`);
		const preview = await previewFolderUpload({ folder }, deps());
		const upload = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());
		for (const result of [preview, upload]) {
			expect(result).toMatchObject({ status: "error", code: "import_failed", message: expect.stringMatching(/could not read notes as a repository/) });
		}
		expect(await git(remote, "branch", "--list")).toBe("");

		// git's own refusal reaches the person.
		const dubious: GitRunner = async (args, options) => {
			if (args.includes("--show-toplevel")) throw Object.assign(new Error("exit 128"), { code: 128, stderr: "fatal: detected dubious ownership in repository at '/x'\n" });
			return deps().git!(args, options);
		};
		await fs.rm(path.join(folder, ".git"));
		await git(folder, "init", "--quiet");
		expect(await previewFolderUpload({ folder }, deps({ git: dubious }))).toMatchObject({ status: "error", code: "import_failed", message: expect.stringContaining("dubious ownership") });
	});
});

describe("checks the upload repeats after its preview", () => {
	/** A history walk that counts how often it runs. */
	function countingStream(): GitStreamRunner & { walks: number } {
		const stream = Object.assign(async (args: string[], options: StreamOptions, onChunk: (chunk: string) => void) => {
			if (args.includes("log")) stream.walks += 1;
			onChunk(await streamOutput(args, options));
		}, { walks: 0 });
		return stream;
	}

	beforeEach(async () => {
		await git(folder, "init", "--quiet", "-b", "main");
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		await commitAll(folder);
	});

	it("skips a commit's history checks on the upload right after its preview", async () => {
		const gitStream = countingStream();
		expect(await previewFolderUpload({ folder }, deps({ gitStream }))).toMatchObject({ status: "ok", source: "head" });
		expect(gitStream.walks).toBe(1);

		const upload = deps({ gitStream });
		expect(await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, upload)).toMatchObject({ status: "pushed", source: "head" });

		expect(gitStream.walks).toBe(1);
		expect(upload.calls.some((call) => call.args.includes("ls-tree"))).toBe(false);
		// Whether the repository is shallow is not the commit's to say: asked every time.
		expect(upload.calls.some((call) => call.args.includes("--is-shallow-repository"))).toBe(true);
	});

	it("checks a commit made since the preview in full, refusing a secret it brought", async () => {
		const gitStream = countingStream();
		expect(await previewFolderUpload({ folder }, deps({ gitStream }))).toMatchObject({ status: "ok" });
		await fs.writeFile(path.join(folder, ".env"), `${FAKE}=${FAKE}\n`);
		await commitAll(folder, "env");

		const upload = deps({ gitStream });
		expect(await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, upload)).toMatchObject({ status: "error", code: "secret_files", files: [".env"] });
		expect(gitStream.walks).toBe(2);
		expect(await git(remote, "branch", "--list")).toBe("");
	});

	it("checks again once the preview's checks are old, or with another file limit", async () => {
		const gitStream = countingStream();
		const start = Date.now();
		expect(await previewFolderUpload({ folder }, deps({ gitStream, now: () => start }))).toMatchObject({ status: "ok" });
		expect(await previewFolderUpload({ folder }, deps({ gitStream, now: () => start + 60_000 }))).toMatchObject({ status: "ok" });
		expect(gitStream.walks).toBe(1);
		expect(await previewFolderUpload({ folder }, deps({ gitStream, now: () => start + 11 * 60_000 }))).toMatchObject({ status: "ok" });
		expect(gitStream.walks).toBe(2);
		expect(await previewFolderUpload({ folder }, deps({ gitStream, now: () => start + 11 * 60_000, maxFileBytes: 0 }))).toMatchObject({ status: "error", code: "file_too_large" });
	});

	it("never remembers a commit that was refused", async () => {
		await fs.writeFile(path.join(folder, ".env"), `${FAKE}=${FAKE}\n`);
		await commitAll(folder, "env");
		const gitStream = countingStream();
		for (let i = 0; i < 2; i++) expect(await previewFolderUpload({ folder }, deps({ gitStream }))).toMatchObject({ status: "error", code: "secret_files" });
		expect(gitStream.walks).toBe(2);
	});
});

describe("import scan", () => {
	it("looks at a bounded number of files at once", async () => {
		await Promise.all(Array.from({ length: 120 }, (_, i) => fs.writeFile(path.join(folder, `f${i}.txt`), String(i))));
		const lstat = fs.lstat.bind(fs);
		let inFlight = 0;
		let most = 0;
		const spy = vi.spyOn(fs, "lstat").mockImplementation((async (file: Parameters<typeof fs.lstat>[0]) => {
			inFlight += 1;
			most = Math.max(most, inFlight);
			try {
				await new Promise((resolve) => setTimeout(resolve, 2));
				return await lstat(file);
			} finally {
				inFlight -= 1;
			}
		}) as typeof fs.lstat);
		try {
			expect(await previewFolderUpload({ folder }, deps())).toMatchObject({ status: "ok", source: "import" });
		} finally {
			spy.mockRestore();
		}
		expect(most).toBeGreaterThan(1);
		expect(most).toBeLessThanOrEqual(32);
	});
});

describe("registerCloudFolderPushIpc", () => {
	it("answers only the app window's main frame", async () => {
		const handlers = new Map<string, (event: unknown, input: unknown) => unknown>();
		registerCloudFolderPushIpc({ handle: (channel, handler) => handlers.set(channel, handler) }, deps());
		const handler = handlers.get(CLOUD_PUSH_FOLDER_CHANNEL);
		await fs.writeFile(path.join(folder, "a.txt"), "a");

		expect(await handler?.({ senderFrame: { parent: {} } }, { folder, pushPath: PUSH_PATH, branch: "main" })).toMatchObject({ status: "error", code: "invalid_request" });
		expect(await handler?.({ senderFrame: { parent: null } }, { folder, pushPath: PUSH_PATH, branch: "main" })).toMatchObject({ status: "pushed" });
	});

	it("answers only the app's shell, for a folder picked in its folder dialog", async () => {
		const handlers = new Map<string, (event: unknown, input: unknown) => unknown>();
		const shell = { id: 1 };
		registerCloudFolderPushIpc({ handle: (channel, handler) => handlers.set(channel, handler) }, {
			...deps(),
			trustedSender: (sender) => sender === shell,
			chosenFolder: isChosenFolder,
		});
		const handler = handlers.get(CLOUD_PUSH_FOLDER_CHANNEL)!;
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		const input = { folder, pushPath: PUSH_PATH, branch: "main" };

		expect(await handler({ sender: { id: 2 }, senderFrame: { parent: null } }, input)).toMatchObject({ status: "error", code: "invalid_request" });
		expect(await handler({ sender: shell, senderFrame: { parent: null } }, input)).toMatchObject({ status: "error", message: expect.stringMatching(/folder dialog/) });
		rememberChosenFolder(folder);
		expect(await handler({ sender: shell, senderFrame: { parent: null } }, input)).toMatchObject({ status: "pushed" });
	});
});

// Every pushed blob's content, to check what reached the remote.
async function pushedContents(ref: string): Promise<Map<string, { mode: string; content: string }>> {
	const out = new Map<string, { mode: string; content: string }>();
	for (const line of (await git(remote, "ls-tree", "-r", ref)).split("\n").filter(Boolean)) {
		const [meta, file] = line.split("\t");
		const [mode, , object] = meta.split(" ");
		out.set(file, { mode, content: await git(remote, "cat-file", "blob", object) });
	}
	return out;
}

const FAKE = "FAKE_TEST_VALUE";

describe("secret-looking files", () => {
	it("names the files a folder upload leaves out, always or by what they hold", () => {
		for (const file of [".env", ".env.local", "config/.env.production", "a/b/store.p12", "x.pfx", "id_rsa", "keys/id_ed25519",
			".netrc", ".pypirc", ".git-credentials", "credentials.json", "client_secret_123.apps.json", "service-account-prod.json",
			".ssh/config", "home/.aws/credentials", ".gnupg/pubring.kbx", ".ENV", "ID_RSA", ".ssh/", "test/.ssh/known.pem"]) {
			expect(secretPathKind(file), file).toBe("secret");
		}
		for (const file of [".npmrc", "packages/a/.npmrc", "certs/server.pem", "tls.key", "TLS.KEY", "test/fixtures/ca.pem"]) {
			expect(secretPathKind(file), file).toBe("content");
		}
		for (const file of [".env.example", ".env.sample", "app/.env.template", "id_rsa.pub", "keys/id_ed25519.pub", "README.md", "src/env.ts", "environment.md",
			"keyboard.ts", "monkey.json", "credentials.md", "docs/ssh.md", "aws/handler.ts", "lib/", "certs.pem/"]) {
			expect(secretPathKind(file), file).toBe("none");
		}
	});

	it("tells an .npmrc holding a credential from one that only configures npm", () => {
		for (const content of [
			`//registry.npmjs.org/:_authToken=${FAKE}\n`,
			`registry=https://npm.example.com\n//npm.example.com/:_auth = ${FAKE}\n`,
			`_password="${FAKE}"\n`,
			`; a comment\n_authToken=${FAKE}\n`,
		]) expect(contentHoldsSecret(".npmrc", content), content).toBe(true);
		for (const content of [
			"engine-strict=true\nsave-exact=true\n",
			"@acme:registry=https://npm.pkg.github.com\n",
			"//registry.npmjs.org/:_authToken=${NPM_TOKEN}\n",
			"//npm.pkg.github.com/:_authToken=\"${GITHUB_TOKEN}\"\n",
			`# _authToken=${FAKE}\n; //registry.npmjs.org/:_authToken=${FAKE}\n`,
			"_authToken=\n",
		]) expect(contentHoldsSecret("config/.npmrc", content), content).toBe(false);
	});

	it("tells a private key from public certificates in .pem and .key files", () => {
		const cert = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n";
		expect(contentHoldsSecret("certs/ca.pem", cert)).toBe(false);
		expect(contentHoldsSecret("certs/ca.pem", `${cert}-----BEGIN PUBLIC KEY-----\nMIIB\n-----END PUBLIC KEY-----\n`)).toBe(false);
		for (const header of ["PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY", "OPENSSH PRIVATE KEY", "ENCRYPTED PRIVATE KEY", "PGP PRIVATE KEY BLOCK"]) {
			expect(contentHoldsSecret("test/fixtures/server.key", `-----BEGIN ${header}-----\n${FAKE}\n-----END ${header}-----\n`), header).toBe(true);
		}
		// A certificate bundled with its key, a binary key, and a key without its PEM header.
		expect(contentHoldsSecret("server.pem", `${cert}-----BEGIN PRIVATE KEY-----\n${FAKE}\n-----END PRIVATE KEY-----\n`)).toBe(true);
		expect(contentHoldsSecret("server.key", "\u0030\u0082\u0004\u00a4")).toBe(true);
		expect(contentHoldsSecret("server.key", FAKE)).toBe(true);
	});

	it("uploads an .npmrc and certificates without credentials, and leaves out the ones holding them", async () => {
		await fs.mkdir(path.join(folder, "certs"));
		await fs.mkdir(path.join(folder, "app"));
		await fs.writeFile(path.join(folder, ".npmrc"), "//registry.npmjs.org/:_authToken=${NPM_TOKEN}\nengine-strict=true\n");
		await fs.writeFile(path.join(folder, "app", ".npmrc"), `//registry.npmjs.org/:_authToken=${FAKE}\n`);
		await fs.writeFile(path.join(folder, "certs", "ca.pem"), "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n");
		await fs.writeFile(path.join(folder, "certs", "server.key"), `-----BEGIN PRIVATE KEY-----\n${FAKE}\n-----END PRIVATE KEY-----\n`);
		expect(await previewFolderUpload({ folder }, deps())).toEqual({ status: "ok", source: "import", leftOut: ["app/.npmrc", "certs/server.key"] });

		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());

		expect(result).toMatchObject({ status: "pushed", source: "import", leftOut: ["app/.npmrc", "certs/server.key"] });
		const pushed = await pushedContents("main");
		expect([...pushed.keys()].sort()).toEqual([".npmrc", "certs/ca.pem"]);
		for (const { content } of pushed.values()) expect(content).not.toContain(FAKE);
	});

	it("reads a kept .npmrc again as added, refusing it if a credential arrived since it was checked", async () => {
		await fs.writeFile(path.join(folder, ".npmrc"), "engine-strict=true\n");
		const base = deps();
		const racing: GitRunner = async (args, options) => {
			if (args.includes("add")) await fs.writeFile(path.join(folder, ".npmrc"), `//registry.npmjs.org/:_authToken=${FAKE}\n`);
			return base.git!(args, options);
		};

		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, { ...base, git: racing });

		expect(result).toMatchObject({ status: "error", code: "import_failed", message: expect.stringContaining(".npmrc") });
		expect(await git(remote, "branch", "--list")).toBe("");
	});

	it("uploads a repository whose history holds only an .npmrc and certificates without credentials", async () => {
		await git(folder, "init", "--quiet", "-b", "main");
		await fs.writeFile(path.join(folder, ".npmrc"), "//registry.npmjs.org/:_authToken=${NPM_TOKEN}\n");
		await fs.mkdir(path.join(folder, "testdata"));
		await fs.writeFile(path.join(folder, "testdata", "ca.pem"), "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n");
		await commitAll(folder);
		expect(await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps())).toMatchObject({ status: "pushed", source: "head" });
	});

	it("refuses a repository whose .npmrc held a credential in any version, or that holds a private key", async () => {
		await git(folder, "init", "--quiet", "-b", "main");
		await fs.writeFile(path.join(folder, ".npmrc"), `//registry.npmjs.org/:_authToken=${FAKE}\n`);
		await commitAll(folder, "with token");
		await fs.writeFile(path.join(folder, ".npmrc"), "//registry.npmjs.org/:_authToken=${NPM_TOKEN}\n");
		await commitAll(folder, "env reference");
		expect(await previewFolderUpload({ folder }, deps())).toMatchObject({ status: "error", code: "secret_files", files: [".npmrc"] });

		const other = path.join(root, "other");
		await fs.mkdir(path.join(other, "test", "fixtures"), { recursive: true });
		await git(other, "init", "--quiet", "-b", "main");
		await fs.writeFile(path.join(other, "test", "fixtures", "server.key"), `-----BEGIN RSA PRIVATE KEY-----\n${FAKE}\n-----END RSA PRIVATE KEY-----\n`);
		await commitAll(other);
		expect(await previewFolderUpload({ folder: other }, deps())).toMatchObject({ status: "error", code: "secret_files", files: ["test/fixtures/server.key"] });
	});

	it("finds a secret file only a merge brought in", async () => {
		await git(folder, "init", "--quiet", "-b", "main");
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		await commitAll(folder, "a");
		await git(folder, "checkout", "--quiet", "-b", "side");
		await fs.writeFile(path.join(folder, "b.txt"), "b");
		await commitAll(folder, "b");
		await git(folder, "checkout", "--quiet", "main");
		await fs.writeFile(path.join(folder, "c.txt"), "c");
		await commitAll(folder, "c");
		await git(folder, "-c", "user.name=T", "-c", "user.email=t@e", "merge", "--quiet", "--no-ff", "--no-commit", "side");
		// Added in the merge commit itself, on neither side.
		await fs.writeFile(path.join(folder, ".env"), `${FAKE}=${FAKE}\n`);
		await git(folder, "add", ".env");
		await git(folder, "-c", "user.name=T", "-c", "user.email=t@e", "commit", "--quiet", "-m", "merge");

		const d = deps();
		expect(await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, d)).toMatchObject({ status: "error", code: "secret_files", files: [".env"] });
		// The history streams through its own runner, never buffered whole.
		expect(d.calls.some((call) => call.args.includes("log"))).toBe(false);
		expect(await git(remote, "branch", "--list")).toBe("");
	});

	it("reads the history however git splits its output", async () => {
		await git(folder, "init", "--quiet", "-b", "main");
		await fs.mkdir(path.join(folder, "config"));
		await fs.writeFile(path.join(folder, "README.md"), "# notes\n");
		await fs.writeFile(path.join(folder, "config", ".env.production"), `${FAKE}=${FAKE}\n`);
		await commitAll(folder);
		await git(folder, "rm", "--quiet", "config/.env.production");
		await commitAll(folder, "drop");
		// Three characters at a time, so names and changes are cut anywhere.
		const gitStream: GitStreamRunner = async (args, options, onChunk) => {
			const stdout = await streamOutput(args, options);
			for (let i = 0; i < stdout.length; i += 3) onChunk(stdout.slice(i, i + 3));
		};
		expect(await previewFolderUpload({ folder }, deps({ gitStream }))).toMatchObject({ status: "error", code: "secret_files", files: ["config/.env.production"] });
		// A history git fails to read is a failure, not a pass.
		const failing: GitStreamRunner = async () => { throw Object.assign(new Error("exit 128"), { stderr: "fatal: bad object HEAD" }); };
		expect(await previewFolderUpload({ folder }, deps({ gitStream: failing }))).toMatchObject({ status: "error", code: "import_failed", message: expect.stringContaining("bad object") });
	});

	it("leaves secret-looking files out of an import, nested ones too, and reports them", async () => {
		await fs.mkdir(path.join(folder, "config"), { recursive: true });
		await fs.mkdir(path.join(folder, ".ssh"));
		await fs.mkdir(path.join(folder, "keys"));
		await fs.writeFile(path.join(folder, "README.md"), "# notes\n");
		await fs.writeFile(path.join(folder, ".env"), `${FAKE}=${FAKE}\n`);
		await fs.writeFile(path.join(folder, ".env.example"), "TOKEN=\n");
		await fs.writeFile(path.join(folder, "config", ".env.production"), `${FAKE}=${FAKE}\n`);
		await fs.writeFile(path.join(folder, ".ssh", "config"), `Host x\n  IdentityFile ${FAKE}\n`);
		await fs.writeFile(path.join(folder, "keys", "id_rsa"), FAKE);
		await fs.writeFile(path.join(folder, "keys", "id_rsa.pub"), "ssh-rsa AAAA public");
		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());
		expect(result).toMatchObject({ status: "pushed", source: "import" });
		const leftOut = result.status === "pushed" ? result.leftOut : [];
		expect([...leftOut].sort()).toEqual([".env", ".ssh/config", "config/.env.production", "keys/id_rsa"]);
		const pushed = await pushedContents("main");
		expect([...pushed.keys()].sort()).toEqual([".env.example", "README.md", "keys/id_rsa.pub"]);
		for (const { content } of pushed.values()) expect(content).not.toContain(FAKE);
		// The folder itself is untouched.
		expect(await fs.readFile(path.join(folder, ".env"), "utf8")).toContain(FAKE);
	});

	it("uploads a link into ~/.ssh as a link, never the key it points at", async () => {
		const home = path.join(root, "home");
		await fs.mkdir(path.join(home, ".ssh"), { recursive: true });
		await fs.writeFile(path.join(home, ".ssh", "deploy_key"), FAKE);
		await fs.writeFile(path.join(folder, "README.md"), "# notes\n");
		await fs.symlink(path.join(home, ".ssh", "deploy_key"), path.join(folder, "key-link"));
		await fs.symlink(path.join(home, ".ssh"), path.join(folder, "ssh-dir"));
		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps({ homeDir: home }));
		expect(result).toMatchObject({ status: "pushed", source: "import" });
		const pushed = await pushedContents("main");
		// Git stores a symlink as a link (mode 120000) to its target path.
		expect(pushed.get("key-link")).toEqual({ mode: "120000", content: path.join(home, ".ssh", "deploy_key") });
		expect(pushed.get("ssh-dir")?.mode).toBe("120000");
		for (const { content } of pushed.values()) expect(content).not.toContain(FAKE);
	});

	it("refuses a repository folder whose history holds a secret-looking file, even one deleted since", async () => {
		await git(folder, "init", "--quiet", "-b", "main");
		await fs.writeFile(path.join(folder, "README.md"), "# notes\n");
		await fs.writeFile(path.join(folder, ".env"), `${FAKE}=${FAKE}\n`);
		await git(folder, "add", "-A");
		await git(folder, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "--quiet", "-m", "with env");
		await git(folder, "rm", "--quiet", ".env");
		await git(folder, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "--quiet", "-m", "drop env");
		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());
		expect(result).toMatchObject({ status: "error", code: "secret_files", message: expect.stringContaining(".env"), files: [".env"] });
		expect(await git(remote, "branch", "--list")).toBe("");
		expect(await previewFolderUpload({ folder }, deps())).toMatchObject({ status: "error", code: "secret_files", files: [".env"] });
	});

	it("previews what an upload leaves out without uploading anything", async () => {
		await fs.writeFile(path.join(folder, "README.md"), "# notes\n");
		await fs.writeFile(path.join(folder, ".env"), `${FAKE}=${FAKE}\n`);
		expect(await previewFolderUpload({ folder }, deps())).toEqual({ status: "ok", source: "import", leftOut: [".env"] });
		expect(await git(remote, "branch", "--list")).toBe("");
		expect(await fs.readdir(tmpRoot)).toEqual([]);
	});
});

/** A PEM block; its label is passed in, so no key header is written in this file. */
function pem(label: string, body: string): string {
	return `-----BEGIN ${label}-----\n${body}\n-----END ${label}-----\n`;
}

/** A line of base64, as a key body is written (64 characters), made here rather than written out. */
const BASE64_LINE = Buffer.from("Man is distinguished, not only by his reason, but by this").toString("base64").slice(0, 64);

describe("history checks the person's git settings cannot change", () => {
	let settings: string;
	/** Dependencies whose git reads the person's own (global) settings. */
	async function configured(text: string) {
		await fs.writeFile(settings, text);
		return deps({ env: () => ({ ...gitEnv, GIT_CONFIG_GLOBAL: settings }) });
	}

	beforeEach(async () => {
		settings = path.join(root, "gitconfig");
		await git(folder, "init", "--quiet", "-b", "main");
	});

	it("sees a secret its first commit added with log.showRoot off, changed since or not", async () => {
		await fs.writeFile(path.join(folder, ".env"), `${FAKE}=1\n`);
		await fs.writeFile(path.join(folder, ".netrc"), `machine x password ${FAKE}\n`);
		await commitAll(folder, "first");
		await fs.writeFile(path.join(folder, ".env"), `${FAKE}=2\n`);
		await commitAll(folder, "second");
		const d = await configured("[log]\n\tshowRoot = false\n");
		expect(await previewFolderUpload({ folder }, d)).toMatchObject({ status: "error", code: "secret_files", files: [".env", ".netrc"] });
		expect(await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, d)).toMatchObject({ status: "error", code: "secret_files" });
		expect(await git(remote, "branch", "--list")).toBe("");
	});

	it.each(["combined", "dense-combined", "first-parent"])("sees a secret only a merge added with log.diffMerges = %s", async (format) => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		await commitAll(folder, "a");
		await git(folder, "checkout", "--quiet", "-b", "side");
		await fs.writeFile(path.join(folder, "b.txt"), "b");
		await commitAll(folder, "b");
		await git(folder, "checkout", "--quiet", "main");
		await fs.writeFile(path.join(folder, "c.txt"), "c");
		await commitAll(folder, "c");
		await git(folder, "-c", "user.name=T", "-c", "user.email=t@e", "merge", "--quiet", "--no-ff", "--no-commit", "side");
		await fs.writeFile(path.join(folder, ".env"), `${FAKE}=${FAKE}\n`);
		await fs.writeFile(path.join(folder, ".npmrc"), `//registry.npmjs.org/:_authToken=${FAKE}\n`);
		await git(folder, "add", ".env", ".npmrc");
		await git(folder, "-c", "user.name=T", "-c", "user.email=t@e", "commit", "--quiet", "-m", "merge");
		const d = await configured(`[log]\n\tdiffMerges = ${format}\n`);
		expect(await previewFolderUpload({ folder }, d)).toMatchObject({ status: "error", code: "secret_files", files: [".env", ".npmrc"] });
	});

	it("sees the secret a signed commit added with log.showSignature on", async (context) => {
		const key = path.join(root, "signing-key");
		try {
			await execFileAsync("ssh-keygen", ["-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", key]);
			await fs.writeFile(path.join(folder, "a.txt"), "a");
			await commitAll(folder, "a");
			await fs.writeFile(path.join(folder, ".env"), `${FAKE}=${FAKE}\n`);
			await git(folder, "add", ".env");
			await git(folder, "-c", "user.name=T", "-c", "user.email=t@e", "-c", "gpg.format=ssh", "-c", `user.signingkey=${key}`, "commit", "--quiet", "-S", "-m", "signed");
		} catch {
			// No ssh-keygen, or a git without SSH signing (before 2.34).
			context.skip();
		}
		expect(await git(folder, "cat-file", "commit", "HEAD")).toContain("gpgsig");
		const d = await configured(`[log]\n\tshowSignature = true\n[gpg]\n\tformat = ssh\n`);
		expect(await previewFolderUpload({ folder }, d)).toMatchObject({ status: "error", code: "secret_files", files: [".env"] });
	});

	it("reads a change past anything git prints before it", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		await commitAll(folder, "a");
		await fs.writeFile(path.join(folder, ".env"), `${FAKE}=${FAKE}\n`);
		await commitAll(folder, "env");
		// What a signature check, or another setting, puts ahead of a commit's changes.
		const chatty: GitStreamRunner = async (args, options, onChunk) => {
			const stdout = await streamOutput(args, options);
			onChunk(args.includes("log") ? `Good "git" signature for t@e with ED25519 key SHA256:${FAKE}\n${stdout}` : stdout);
		};
		expect(await previewFolderUpload({ folder }, deps({ gitStream: chatty }))).toMatchObject({ status: "error", code: "secret_files", files: [".env"] });
	});

	it("reads a merge's change in git's combined format", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		await commitAll(folder, "a");
		const zero = "0".repeat(40);
		const blob = "1".repeat(40);
		const combined: GitStreamRunner = async (args, options, onChunk) => {
			if (args.includes("log")) onChunk(`::000000 000000 100644 ${zero} ${zero} ${blob} AA\0.env\0`);
			else onChunk(await streamOutput(args, options));
		};
		expect(await previewFolderUpload({ folder }, deps({ gitStream: combined }))).toMatchObject({ status: "error", code: "secret_files", files: [".env"] });
	});
});

describe("credentials read by content", () => {
	it("finds an .npmrc credential however its key or encoding is written", () => {
		const utf16 = (text: string) => Buffer.concat([Buffer.from([0xff, 0xfe]), Buffer.from(text, "utf16le")]).toString("utf8");
		for (const content of [
			utf16(`//registry.npmjs.org/:_authToken=${FAKE}\r\n`),
			`//registry.npmjs.org/:_authToken=${FAKE}\0\n`,
			`https://registry.npmjs.org/:_authToken=${FAKE}\n`,
			`@acme:_authToken=${FAKE}\n`,
			`"//registry.npmjs.org/:_authToken"=${FAKE}\n`,
			`'_password' = '${FAKE}'\n`,
			`key="${pem("PRIVATE KEY", FAKE).replace(/\n/g, "\\n")}"\n`,
		]) expect(contentHoldsSecret(".npmrc", content), content).toBe(true);
		for (const content of [
			"always-auth=true\n@acme:registry=https://npm.pkg.github.com\n",
			"\"//registry.npmjs.org/:_authToken\"=\"${NPM_TOKEN}\"\n",
			"\uFEFF//registry.npmjs.org/:_authToken=${NPM_TOKEN}\r\n",
		]) expect(contentHoldsSecret(".npmrc", content), content).toBe(false);
	});

	it("finds a secret beside public certificates, and keeps a certificate chain", () => {
		const cert = pem("CERTIFICATE", `${BASE64_LINE}\n${BASE64_LINE}\nMIIB`);
		// A key without its header, a setting, and text that is not plain.
		expect(contentHoldsSecret("server.pem", `${cert}${BASE64_LINE}\n${BASE64_LINE}\n`)).toBe(true);
		expect(contentHoldsSecret("bundle.pem", `${cert}api_key=${FAKE}\n`)).toBe(true);
		expect(contentHoldsSecret("bundle.pem", `${cert}export API_TOKEN="${FAKE}"\n`)).toBe(true);
		expect(contentHoldsSecret("server.key", `${cert}\0`)).toBe(true);
		// An unfinished public block keeps its body.
		expect(contentHoldsSecret("server.pem", `${cert}-----BEGIN CERTIFICATE-----\n${BASE64_LINE}\n`)).toBe(true);
		// A chain as openssl and curl write them: subjects, issuers, attributes and comments.
		const chain = [
			"##\n## Bundle of CA Root Certificates\n## Source: https://hg.mozilla.org/releases/mozilla-release/raw-file/default/security/nss/lib/ckfw/builtins/certdata.txt\n##\n",
			"Example Root CA\n===============\n",
			"Bag Attributes\n    friendlyName: example.com\n    localKeyID: 01 00 00 00\n",
			// A bundle's hashes and serial numbers: hex in one case, or decimal.
			`# SHA256 Fingerprint: ${"e5".repeat(32)}\n# Serial: ${"6870496496".repeat(5)}\n## SHA256: ${"E5".repeat(32)}\n`,
			"subject=/C=US/O=Example Inc/CN=example.com\nissuer=/C=US/O=Example CA/CN=Example Intermediate\n",
			cert,
			"subject=C = US, O = Example CA, CN = Example Intermediate\nissuer=C = US, O = Example CA, CN = Example Root\n",
			cert,
			pem("PUBLIC KEY", BASE64_LINE),
		].join("");
		expect(contentHoldsSecret("certs/fullchain.pem", chain)).toBe(false);
		expect(contentHoldsSecret("certs/ca.key", chain.replace(/\n/g, "\r\n"))).toBe(false);
	});

	it("leaves out an imported UTF-16 .npmrc holding a token", async () => {
		await fs.writeFile(path.join(folder, "README.md"), "# notes\n");
		await fs.writeFile(path.join(folder, ".npmrc"), Buffer.concat([Buffer.from([0xff, 0xfe]), Buffer.from(`//registry.npmjs.org/:_authToken=${FAKE}\r\n`, "utf16le")]));
		expect(await previewFolderUpload({ folder }, deps())).toEqual({ status: "ok", source: "import", leftOut: [".npmrc"] });
	});
});

describe("committed files read by content", () => {
	beforeEach(async () => {
		await git(folder, "init", "--quiet", "-b", "main");
	});

	it("reads a file stored under two kinds of name by each kind's rules", async () => {
		const both = `${pem("CERTIFICATE", "MIIB")}//registry.npmjs.org/:_authToken=${FAKE}\n`;
		await fs.writeFile(path.join(folder, ".npmrc"), both);
		await commitAll(folder, "npmrc");
		await fs.writeFile(path.join(folder, "ca.pem"), both);
		await commitAll(folder, "certificate");
		// As a certificate it holds nothing; as an .npmrc it holds a token.
		expect(contentHoldsSecret("ca.pem", both)).toBe(false);
		expect(await previewFolderUpload({ folder }, deps())).toMatchObject({ status: "error", code: "secret_files", files: [".npmrc"] });
	});

	it("reads every committed version through one git process, never one too large to check", async () => {
		await fs.mkdir(path.join(folder, "certs"));
		await fs.writeFile(path.join(folder, ".npmrc"), "//registry.npmjs.org/:_authToken=${NPM_TOKEN}\n");
		for (let i = 0; i < 4; i++) await fs.writeFile(path.join(folder, "certs", `ca${i}.pem`), pem("CERTIFICATE", `MIIB${i}`));
		await commitAll(folder, "first");
		await fs.writeFile(path.join(folder, "certs", "ca0.pem"), pem("CERTIFICATE", "MIIC"));
		await commitAll(folder, "second");
		const runs: string[][] = [];
		const recording: GitStreamRunner = async (args, options, onChunk) => {
			runs.push(args);
			onChunk(await streamOutput(args, options));
		};
		const d = deps({ gitStream: recording });
		expect(await previewFolderUpload({ folder }, d)).toMatchObject({ status: "ok", source: "head" });
		expect(runs.filter((args) => args.includes("cat-file"))).toEqual([["-C", folder, "cat-file", "--batch"]]);
		expect(d.calls.some((call) => call.args.includes("cat-file"))).toBe(false);

		// A version too large to check is taken as holding a secret, and skipped unread.
		await fs.writeFile(path.join(folder, "certs", "big.pem"), `${pem("CERTIFICATE", "MIIB")}${"#".repeat(1024 * 1024 + 1)}\n`);
		await commitAll(folder, "big");
		expect(await previewFolderUpload({ folder }, deps())).toMatchObject({ status: "error", code: "secret_files", files: ["certs/big.pem"] });
	});

	it("walks a commit's history once for requests that overlap", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		await commitAll(folder);
		let walks = 0;
		const slow: GitStreamRunner = async (args, options, onChunk) => {
			if (args.includes("log")) {
				walks += 1;
				await new Promise((resolve) => setTimeout(resolve, 200));
			}
			onChunk(await streamOutput(args, options));
		};
		const results = await Promise.all([previewFolderUpload({ folder }, deps({ gitStream: slow })), previewFolderUpload({ folder }, deps({ gitStream: slow }))]);
		for (const result of results) expect(result).toMatchObject({ status: "ok", source: "head" });
		expect(walks).toBe(1);
	});
});

describe("an import's files read again once added", () => {
	function racing(file: string, content: string | Buffer): GitRunner {
		const base = deps();
		return async (args, options) => {
			if (args.includes("add")) await fs.writeFile(path.join(folder, file), content);
			return base.git!(args, options);
		};
	}

	it("reads the file added, not another whose name it starts like", async () => {
		const cert = pem("CERTIFICATE", "MIIB");
		await fs.writeFile(path.join(folder, "a.pem"), cert);
		await fs.writeFile(path.join(folder, "0:a.pem"), cert);
		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps({ git: racing("0:a.pem", pem("PRIVATE KEY", FAKE)) }));
		expect(result).toMatchObject({ status: "error", code: "import_failed", message: expect.stringContaining("0:a.pem") });
		expect(await git(remote, "branch", "--list")).toBe("");
	});

	it("refuses a kept file that grew past what is checked", async () => {
		await fs.writeFile(path.join(folder, ".npmrc"), "engine-strict=true\n");
		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps({ git: racing(".npmrc", `engine-strict=true\n${"#".repeat(2 * 1024 * 1024)}\n`) }));
		expect(result).toMatchObject({ status: "error", code: "import_failed", message: expect.stringContaining(".npmrc") });
		expect(await git(remote, "branch", "--list")).toBe("");
	});
});

describe("Orchestrator's own state", () => {
	it("refuses a folder inside the app's data directory, or one holding it", async () => {
		const dataDir = path.join(root, "custom-data");
		await fs.mkdir(path.join(dataDir, "projects"), { recursive: true });
		await fs.writeFile(path.join(dataDir, "projects", "a.txt"), "a");
		const withState = deps({ appStateDirs: () => [dataDir] });
		for (const target of [dataDir, path.join(dataDir, "projects"), root]) {
			expect(await pushFolderToRepository({ folder: target, pushPath: PUSH_PATH, branch: "main" }, withState), target).toMatchObject({ status: "error", code: "folder_unavailable" });
		}
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		expect(await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, withState)).toMatchObject({ status: "pushed" });
	});

	it("compares case only where the file system ignores it", () => {
		expect(pathWithin("/data/ao/x", "/data/ao", false)).toBe(true);
		expect(pathWithin("/data/AO/x", "/data/ao", false)).toBe(false);
		expect(pathWithin("/data/AO/x", "/data/ao", true)).toBe(true);
		expect(pathWithin("/data/ao-other", "/data/ao", false)).toBe(false);
		expect(pathWithin("/data", "/data/ao", false)).toBe(false);
	});
});

describe("registerCloudFolderPushIpc preview", () => {
	it("answers only the app's shell, for a folder picked in its folder dialog", async () => {
		const handlers = new Map<string, (event: unknown, input: unknown) => unknown>();
		const shell = { id: 1 };
		registerCloudFolderPushIpc({ handle: (channel, handler) => handlers.set(channel, handler) }, {
			...deps(),
			trustedSender: (sender) => sender === shell,
			chosenFolder: (candidate) => candidate === folder,
		});
		const handler = handlers.get(CLOUD_PREVIEW_FOLDER_CHANNEL)!;
		await fs.writeFile(path.join(folder, ".env"), `${FAKE}=${FAKE}\n`);
		expect(await handler({ sender: { id: 2 }, senderFrame: { parent: null } }, { folder })).toMatchObject({ status: "error", code: "invalid_request" });
		expect(await handler({ sender: shell, senderFrame: { parent: {} } }, { folder })).toMatchObject({ status: "error", code: "invalid_request" });
		expect(await handler({ sender: shell, senderFrame: { parent: null } }, { folder: root })).toMatchObject({ status: "error", message: expect.stringMatching(/folder dialog/) });
		expect(await handler({ sender: shell, senderFrame: { parent: null } }, { folder })).toEqual({ status: "ok", source: "import", leftOut: [".env"] });
	});
});
