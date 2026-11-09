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
	CLOUD_PREVIEW_FOLDER_CHANNEL,
	CLOUD_PUSH_FOLDER_CHANNEL,
	FALLBACK_COMMIT_EMAIL,
	gitSupportsConfigEnv,
	type GitRunner,
	isChosenFolder,
	isSecretLookingPath,
	isValidPushBranch,
	pathWithin,
	previewFolderUpload,
	pushFolderToRepository,
	type PushFolderDeps,
	registerCloudFolderPushIpc,
	rememberChosenFolder,
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
	it("names the files a folder upload leaves out", () => {
		for (const file of [".env", ".env.local", "config/.env.production", "certs/server.pem", "tls.key", "a/b/store.p12", "x.pfx", "id_rsa", "keys/id_ed25519",
			".npmrc", ".netrc", ".pypirc", ".git-credentials", "credentials.json", "client_secret_123.apps.json", "service-account-prod.json",
			".ssh/config", "home/.aws/credentials", ".gnupg/pubring.kbx", ".ENV", "ID_RSA"]) {
			expect(isSecretLookingPath(file), file).toBe(true);
		}
		for (const file of [".env.example", ".env.sample", "app/.env.template", "id_rsa.pub", "keys/id_ed25519.pub", "README.md", "src/env.ts", "environment.md",
			"keyboard.ts", "monkey.json", "credentials.md", "docs/ssh.md", "aws/handler.ts"]) {
			expect(isSecretLookingPath(file), file).toBe(false);
		}
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
