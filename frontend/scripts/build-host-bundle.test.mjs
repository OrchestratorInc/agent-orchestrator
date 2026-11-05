import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, readlinkSync, rmSync, statSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test, vi } from "vitest";
import { buildHostBundle } from "./build-host-bundle.mjs";
import { bashEnv, bashPath, bashSearchPath, gitPosixBin } from "./test-fixtures/git-posix-tools.mjs";

vi.mock("node:child_process", async (importOriginal) => {
	const actual = await importOriginal();
	/** Windows 使用真实 GNU tar 保留 MSYS POSIX 链接，其他命令直接执行。 */
	const execFileSync = (command, args, options) => {
		if (process.platform === "win32" && command === "tar") {
			return actual.execFileSync(join(gitPosixBin, "tar.exe"), args.map(bashPath), {
				...options,
				env: bashEnv({ ...options?.env, PATH: bashSearchPath, MSYS: "winsymlinks:sys" }),
			});
		}
		return actual.execFileSync(command, args, options);
	};
	return { ...actual, default: { ...actual, execFileSync }, execFileSync };
});

test("headless archive contains the daemon, Claude ACP runtime, and tmux in the expected layout", () => {
	const root = mkdtempSync(join(tmpdir(), "ao-host-bundle-test-"));
	try {
		const daemon = join(root, "daemon", "ao");
		const node = join(root, "resources", "acp-runtime", "node", "bin", "node");
		const adapter = join(root, "resources", "acp-runtime", "node_modules", "@agentclientprotocol", "claude-agent-acp", "dist", "index.js");
		const tmux = join(root, "tmux", "bin", "tmux");
		for (const file of [daemon, node, adapter, tmux]) {
			mkdirSync(dirname(file), { recursive: true });
			writeFileSync(file, file);
		}
		for (const file of [daemon, node, tmux]) chmodSync(file, 0o444);
		const adapterLink = join(root, "resources", "acp-runtime", "node_modules", ".bin", "claude-agent-acp");
		mkdirSync(dirname(adapterLink), { recursive: true });
		const adapterTarget = "../@agentclientprotocol/claude-agent-acp/dist/index.js";
		if (process.platform === "win32") {
			// 用 MSYS 自身创建 POSIX 链接；Windows 原生 lstat 将它视为普通文件。
			execFileSync(join(gitPosixBin, "ln.exe"), ["-s", adapterTarget, bashPath(adapterLink)], { env: bashEnv({ MSYS: "winsymlinks:sys" }) });
		} else symlinkSync(adapterTarget, adapterLink);
		const archive = buildHostBundle({ source: root, platform: "linux", arch: "x64" });
		assert.equal(archive, join(root, "dist-host", "ao-host-linux-x64.tar.gz"));
		const extracted = join(root, "extracted");
		mkdirSync(extracted);
		execFileSync("tar", ["-xzf", archive, "-C", extracted]);
		for (const [original, bundled] of [
			[daemon, "resources/daemon/ao"],
			[node, "resources/acp-runtime/node/bin/node"],
			[adapter, "resources/acp-runtime/node_modules/@agentclientprotocol/claude-agent-acp/dist/index.js"],
			[tmux, "resources/tmux/bin/tmux"],
		]) {
			assert.equal(readFileSync(join(extracted, bundled), "utf8"), original);
		}
		if (process.platform !== "win32") {
			for (const file of ["daemon/ao", "acp-runtime/node/bin/node", "tmux/bin/tmux"]) {
				assert.equal(statSync(join(extracted, "resources", file)).mode & 0o777, 0o755);
			}
		}
		const bundledLink = join(extracted, "resources", "acp-runtime", "node_modules", ".bin", "claude-agent-acp");
		const bundledTarget = process.platform === "win32"
			? execFileSync(join(gitPosixBin, "readlink.exe"), [bashPath(bundledLink)], { encoding: "utf8", env: bashEnv() }).trim()
			: readlinkSync(bundledLink);
		assert.equal(bundledTarget, adapterTarget);
		rmSync(adapter);
		assert.throws(() => buildHostBundle({ source: root, platform: "linux", arch: "x64" }), /Headless bundle requires/);
	} finally {
		rmSync(root, { recursive: true, force: true });
	}
});
