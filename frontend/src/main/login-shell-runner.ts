import { spawn, type ChildProcess, type SpawnOptions } from "node:child_process";
import type { ShellRunner } from "../shared/shell-env";

type SpawnProcess = (command: string, args: readonly string[], options: SpawnOptions) => ChildProcess;
type KillProcess = (pid: number, signal: NodeJS.Signals) => void;

export type LoginShellRunnerOptions = {
	spawnProcess?: SpawnProcess;
	killProcess?: KillProcess;
	platform?: NodeJS.Platform;
};

// Run the user's login shell to dump its env. stdin is ignored so an rc that
// reads input hits EOF instead of hanging; stderr is ignored to drop banner
// noise. Never rejects: resolves null on spawn error, non-zero exit, or timeout
// (SIGKILLed), so the caller degrades to the static PATH floor.
//
// On POSIX the probe runs detached, which puts it in its own session with no
// controlling terminal. Otherwise an interactive shell (`-i`) started from a
// terminal claims the terminal's foreground process group with tcsetpgrp(), and
// a timeout SIGKILL leaves it un-restored: the app is then in a background group
// and the next TTY read stops it with SIGTTIN (exit 149). Timeouts kill the
// whole process group so rc-spawned helpers do not outlive the probe.
export function createLoginShellRunner(timeoutMs: number, options: LoginShellRunnerOptions = {}): ShellRunner {
	const spawnProcess = options.spawnProcess ?? spawn;
	const killProcess = options.killProcess ?? ((pid, signal) => process.kill(pid, signal));
	const ownProcessGroup = (options.platform ?? process.platform) !== "win32";
	return (shellPath, args) =>
		new Promise((resolve) => {
			let settled = false;
			const finish = (value: string | null) => {
				if (settled) return;
				settled = true;
				resolve(value);
			};
			let child: ChildProcess;
			try {
				child = spawnProcess(shellPath, args, {
					stdio: ["ignore", "pipe", "ignore"],
					windowsHide: true,
					...(ownProcessGroup ? { detached: true } : {}),
				});
			} catch {
				finish(null);
				return;
			}
			const timer = setTimeout(() => {
				let groupKilled = false;
				if (ownProcessGroup && child.pid) {
					try {
						killProcess(-child.pid, "SIGKILL");
						groupKilled = true;
					} catch {
						// The group may already be gone; fall back to the child itself.
					}
				}
				if (!groupKilled) child.kill("SIGKILL");
				finish(null);
			}, timeoutMs);
			let stdout = "";
			// stdout may be typed Readable | null under this stdio config; guard it.
			child.stdout?.on("data", (chunk: Buffer) => {
				stdout += chunk.toString("utf8");
			});
			child.once("error", () => {
				clearTimeout(timer);
				finish(null);
			});
			child.once("exit", (code) => {
				clearTimeout(timer);
				finish(code === 0 ? stdout : null);
			});
		});
}
