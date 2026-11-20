import { chmod, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
	buildClaudeAuthorizeUrl,
	extractClaudeOAuthToken,
	findCodexAuthFile,
	firstExecutable,
	readClaudeOAuthTokenFromDir,
	resolveProviderBinary,
} from "./provider-auth-flow";

// firstExecutable is the core of the GUI-launched-app PATH fix: it must locate an
// agent CLI in an install directory that a Finder-launched app's minimal inherited
// PATH would never include, and must ignore a same-named non-executable file.
describe("firstExecutable", () => {
	const dirs: string[] = [];

	afterEach(async () => {
		await Promise.all(dirs.map((dir) => rm(dir, { recursive: true, force: true })));
		dirs.length = 0;
	});

	async function tempDir(): Promise<string> {
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-binresolve-"));
		dirs.push(dir);
		return dir;
	}

	it("finds an executable binary in a searched directory", async () => {
		const dir = await tempDir();
		const bin = path.join(dir, "claude");
		await writeFile(bin, "#!/bin/sh\n");
		await chmod(bin, 0o755);
		expect(await firstExecutable("claude", [dir])).toBe(bin);
	});

	it("returns null when no directory contains the binary", async () => {
		const dir = await tempDir();
		expect(await firstExecutable("claude", [dir, "/nonexistent-ao-dir"])).toBeNull();
	});

	it("skips a same-named file that is not executable", async () => {
		const dir = await tempDir();
		const notExec = path.join(dir, "claude");
		await writeFile(notExec, "not a program");
		await chmod(notExec, 0o644);
		expect(await firstExecutable("claude", [dir])).toBeNull();
	});

	it("returns the first match and tolerates empty/duplicate dirs", async () => {
		const first = await tempDir();
		const second = await tempDir();
		for (const dir of [first, second]) {
			const bin = path.join(dir, "codex");
			await writeFile(bin, "#!/bin/sh\n");
			await chmod(bin, 0o755);
		}
		expect(await firstExecutable("codex", ["", first, first, second])).toBe(path.join(first, "codex"));
	});
});

// The cloud browser login must find the agent CLI wherever the daemon finds
// it: AO's managed launcher first, then the login-shell PATH the desktop
// recovered at startup, which a Finder/Dock launch's own PATH lacks.
describe.skipIf(process.platform === "win32")("resolveProviderBinary", () => {
	const dirs: string[] = [];

	afterEach(async () => {
		await Promise.all(dirs.map((dir) => rm(dir, { recursive: true, force: true })));
		dirs.length = 0;
	});

	async function tempDir(): Promise<string> {
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-provider-bin-"));
		dirs.push(dir);
		return dir;
	}

	async function executable(dir: string, name: string): Promise<string> {
		await mkdir(dir, { recursive: true });
		const bin = path.join(dir, name);
		await writeFile(bin, "#!/bin/sh\n");
		await chmod(bin, 0o755);
		return bin;
	}

	it("finds a CLI on the recovered shell PATH", async () => {
		const shellBin = await tempDir();
		const bin = await executable(shellBin, "ao-test-agent");
		const resolved = await resolveProviderBinary("ao-test-agent", { PATH: shellBin });
		expect(resolved?.path).toBe(bin);
		expect(resolved?.pathEnv.split(path.delimiter)).toContain(shellBin);
	});

	it("prefers AO's managed launcher over a PATH copy, as the daemon does", async () => {
		const dataDir = await tempDir();
		const shellBin = await tempDir();
		await executable(shellBin, "ao-test-agent");
		const managed = await executable(path.join(dataDir, "harnesses", "bin"), "ao-test-agent");
		const resolved = await resolveProviderBinary("ao-test-agent", { PATH: shellBin, AO_DATA_DIR: dataDir });
		expect(resolved?.path).toBe(managed);
	});
});

// The claude token is extracted by shape, not from a fixed file, so it survives
// claude moving its credential store between versions.
describe("extractClaudeOAuthToken", () => {
	it("pulls an sk-ant-oat token out of setup-token stdout", () => {
		const stdout = "Authenticated!\nYour token:\nsk-ant-oat01-AbC_dEf-123456789 \nDone.\n";
		expect(extractClaudeOAuthToken(stdout)).toBe("sk-ant-oat01-AbC_dEf-123456789");
	});

	it("returns null when no token is present", () => {
		expect(extractClaudeOAuthToken("Opening browser to sign in...\nno token here")).toBeNull();
	});
});

// AO runs Claude's own PKCE loopback flow (instead of scraping `claude
// setup-token`, whose Ink TUI prints nothing on a non-TTY pipe). The authorize URL
// must carry the exact parameters Claude Code's OAuth client expects, or the
// browser round-trip / token exchange fails.
describe("buildClaudeAuthorizeUrl", () => {
	it("emits the loopback PKCE authorize URL Claude Code's client expects", () => {
		const url = new URL(buildClaudeAuthorizeUrl("http://localhost:54321/callback", "CHALLENGE_abc", "STATE_xyz"));
		expect(url.origin + url.pathname).toBe("https://claude.com/cai/oauth/authorize");
		expect(url.searchParams.get("client_id")).toBe("9d1c250a-e61b-44d9-88ed-5944d1962f5e");
		expect(url.searchParams.get("response_type")).toBe("code");
		expect(url.searchParams.get("redirect_uri")).toBe("http://localhost:54321/callback");
		expect(url.searchParams.get("scope")).toBe("user:inference");
		expect(url.searchParams.get("code_challenge")).toBe("CHALLENGE_abc");
		expect(url.searchParams.get("code_challenge_method")).toBe("S256");
		expect(url.searchParams.get("state")).toBe("STATE_xyz");
		expect(url.searchParams.get("code")).toBe("true");
	});
});

describe("readClaudeOAuthTokenFromDir", () => {
	const dirs: string[] = [];
	afterEach(async () => {
		await Promise.all(dirs.map((dir) => rm(dir, { recursive: true, force: true })));
		dirs.length = 0;
	});
	async function tempDir(): Promise<string> {
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-claudetok-"));
		dirs.push(dir);
		return dir;
	}

	it("finds a token written into a file in the isolated config dir", async () => {
		const dir = await tempDir();
		await writeFile(path.join(dir, ".credentials.json"), '{"token":"sk-ant-oat01-file_TOKEN_abc123"}');
		expect(await readClaudeOAuthTokenFromDir(dir)).toBe("sk-ant-oat01-file_TOKEN_abc123");
	});

	it("returns null when no file holds a token", async () => {
		const dir = await tempDir();
		await writeFile(path.join(dir, ".claude.json"), '{"machineID":"x","userID":"y"}');
		expect(await readClaudeOAuthTokenFromDir(dir)).toBeNull();
	});
});

describe("findCodexAuthFile", () => {
	const dirs: string[] = [];
	afterEach(async () => {
		await Promise.all(dirs.map((dir) => rm(dir, { recursive: true, force: true })));
		dirs.length = 0;
	});
	async function tempDir(): Promise<string> {
		const dir = await mkdtemp(path.join(os.tmpdir(), "ao-codexauth-"));
		dirs.push(dir);
		return dir;
	}

	it("finds auth.json directly under CODEX_HOME", async () => {
		const home = await tempDir();
		const auth = path.join(home, "auth.json");
		await writeFile(auth, "{}");
		expect(await findCodexAuthFile(home)).toBe(auth);
	});

	it("finds a nested auth.json one level down", async () => {
		const home = await tempDir();
		await mkdir(path.join(home, "store"), { recursive: true });
		const auth = path.join(home, "store", "auth.json");
		await writeFile(auth, "{}");
		expect(await findCodexAuthFile(home)).toBe(auth);
	});

	it("returns null when no auth.json exists", async () => {
		const home = await tempDir();
		expect(await findCodexAuthFile(home)).toBeNull();
	});
});
