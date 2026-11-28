// @vitest-environment node
import { EventEmitter } from "node:events";
import type { ChildProcess, SpawnOptions } from "node:child_process";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createLoginShellRunner } from "./login-shell-runner";

function childProcessDouble(pid = 4242) {
	const child = new EventEmitter() as ChildProcess;
	const stdout = new EventEmitter();
	Object.assign(child, { pid, stdout, kill: vi.fn(() => true) });
	return { child, stdout };
}

describe("createLoginShellRunner", () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it("spawns the POSIX probe detached so an interactive shell cannot take the terminal's foreground group", () => {
		const { child } = childProcessDouble();
		const spawnProcess = vi.fn((_command: string, _args: readonly string[], _options: SpawnOptions) => child);
		void createLoginShellRunner(3_000, { spawnProcess, platform: "linux" })("/usr/bin/bash", ["-ilc", "env"]);

		expect(spawnProcess).toHaveBeenCalledWith("/usr/bin/bash", ["-ilc", "env"], {
			stdio: ["ignore", "pipe", "ignore"],
			windowsHide: true,
			detached: true,
		});
	});

	it("does not detach the probe on Windows", () => {
		const { child } = childProcessDouble();
		const spawnProcess = vi.fn((_command: string, _args: readonly string[], _options: SpawnOptions) => child);
		void createLoginShellRunner(3_000, { spawnProcess, platform: "win32" })("C:\\bash.exe", ["-c", "env"]);

		expect(spawnProcess.mock.calls[0]?.[2]).toEqual({ stdio: ["ignore", "pipe", "ignore"], windowsHide: true });
	});

	it("resolves stdout when the shell exits cleanly", async () => {
		const { child, stdout } = childProcessDouble();
		const result = createLoginShellRunner(3_000, { spawnProcess: () => child, platform: "linux" })("/bin/zsh", []);
		stdout.emit("data", Buffer.from("__AO_SHELL_ENV__PATH=/usr/bin\0"));
		child.emit("exit", 0, null);

		await expect(result).resolves.toBe("__AO_SHELL_ENV__PATH=/usr/bin\0");
	});

	it("resolves null on a non-zero exit", async () => {
		const { child } = childProcessDouble();
		const result = createLoginShellRunner(3_000, { spawnProcess: () => child, platform: "linux" })("/bin/zsh", []);
		child.emit("exit", 1, null);

		await expect(result).resolves.toBeNull();
	});

	it("kills the whole probe process group on timeout", async () => {
		const { child } = childProcessDouble(4242);
		const killProcess = vi.fn();
		const result = createLoginShellRunner(3_000, { spawnProcess: () => child, killProcess, platform: "linux" })(
			"/usr/bin/bash",
			[],
		);
		vi.advanceTimersByTime(3_000);

		await expect(result).resolves.toBeNull();
		expect(killProcess).toHaveBeenCalledWith(-4242, "SIGKILL");
		expect(child.kill).not.toHaveBeenCalled();
	});

	it("falls back to killing the child when the group kill fails", async () => {
		const { child } = childProcessDouble();
		const killProcess = vi.fn(() => {
			throw new Error("ESRCH");
		});
		const result = createLoginShellRunner(3_000, { spawnProcess: () => child, killProcess, platform: "darwin" })(
			"/bin/zsh",
			[],
		);
		vi.advanceTimersByTime(3_000);

		await expect(result).resolves.toBeNull();
		expect(child.kill).toHaveBeenCalledWith("SIGKILL");
	});

	it("kills only the child on Windows timeouts", async () => {
		const { child } = childProcessDouble();
		const killProcess = vi.fn();
		const result = createLoginShellRunner(3_000, { spawnProcess: () => child, killProcess, platform: "win32" })(
			"C:\\bash.exe",
			[],
		);
		vi.advanceTimersByTime(3_000);

		await expect(result).resolves.toBeNull();
		expect(killProcess).not.toHaveBeenCalled();
		expect(child.kill).toHaveBeenCalledWith("SIGKILL");
	});

	it("resolves null when spawn throws or the child errors", async () => {
		const throwing = createLoginShellRunner(3_000, {
			spawnProcess: () => {
				throw new Error("EACCES");
			},
			platform: "linux",
		})("/bin/zsh", []);
		await expect(throwing).resolves.toBeNull();

		const { child } = childProcessDouble();
		const erroring = createLoginShellRunner(3_000, { spawnProcess: () => child, platform: "linux" })("/bin/zsh", []);
		child.emit("error", new Error("ENOENT"));
		await expect(erroring).resolves.toBeNull();
	});
});
