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
	CLOUD_PUSH_FOLDER_CHANNEL,
	FALLBACK_COMMIT_EMAIL,
	gitSupportsConfigEnv,
	type GitRunner,
	isValidPushBranch,
	pushFolderToRepository,
	type PushFolderDeps,
	registerCloudFolderPushIpc,
	resolvePushUrl,
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
		const { stdout, stderr } = await execFileAsync("git", args, { cwd: options.cwd, env: options.env, encoding: "utf8" });
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
			"http.followRedirects": "false",
			"credential.helper": "",
		});
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

		expect(result).toEqual({ status: "pushed", branch: "trunk", commit: head, source: "head" });
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

	it("reports git's refusal, without the token", async () => {
		await fs.writeFile(path.join(folder, "a.txt"), "a");
		await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());
		await fs.writeFile(path.join(folder, "b.txt"), "b");

		// The branch exists now; a second import would have to update it.
		const result = await pushFolderToRepository({ folder, pushPath: PUSH_PATH, branch: "main" }, deps());

		expect(result).toMatchObject({ status: "error", code: "push_failed" });
		expect(result.status === "error" && result.message).toMatch(/Could not upload notes/);
		expect(JSON.stringify(result)).not.toContain("token-1");
	});

	it.each([
		["fatal: unable to access 'https://cp.example.com/x.git/': The requested URL returned error: 401", "signed_out", /Sign in again/],
		["fatal: unable to access 'https://cp.example.com/x.git/': The requested URL returned error: 403", "push_forbidden", /did not let you upload notes/],
		[" ! [remote rejected] main -> main (AO only adds new branches here, and refs/heads/main already exists)", "branch_exists", /already has a main branch/],
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

	it("clears leftover import directories", async () => {
		await fs.mkdir(path.join(tmpRoot, "import-x"), { recursive: true });
		await clearFolderImports(tmpRoot);
		await expect(fs.stat(tmpRoot)).rejects.toThrow();
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
});
