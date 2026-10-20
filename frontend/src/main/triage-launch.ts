import { accessSync, constants } from "node:fs";
import { spawn } from "node:child_process";
import os from "node:os";
import path from "node:path";
import { app } from "electron";
import { resolveDaemonLaunch } from "../shared/daemon-launch";
import { launchCommand } from "./launch-command";
import { terminalCommand } from "./terminal-command";

export type TriageResult = { ok: true } | { ok: false; error: string; command?: string };
export type TriagePrepareResult = { ok: true; prompt: string; dir: string } | { ok: false; error: string; command?: string };

export async function prepareTriage(stateEnv: NodeJS.ProcessEnv): Promise<TriagePrepareResult> {
	try {
		const launch = resolveDaemonLaunch({ ...stateEnv, AO_DAEMON_COMMAND: undefined }, app.isPackaged,
			process.resourcesPath, app.getAppPath(), os.homedir(), process.platform);
		if (!launch) throw new Error("The bundled binary is unavailable.");
		return await new Promise<TriagePrepareResult>((resolve) => {
			const child = spawn(launch.command, ["triage", "--no-launch", "--json"], {
				env: stateEnv, stdio: ["ignore", "pipe", "ignore"], windowsHide: true,
			});
			let stdout = "";
			let bytes = 0;
			let settled = false;
			const finish = (result: TriagePrepareResult) => {
				if (settled) return;
				settled = true;
				clearTimeout(timeout);
				resolve(result);
			};
			const stop = (error: string) => {
				finish({ ok: false, error });
				child.kill("SIGKILL");
			};
			const timeout = setTimeout(() => stop("report.timedOut"), 20_000);
			child.stdout.setEncoding("utf8");
			child.stdout.on("data", (chunk: string) => {
				if (settled) return;
				bytes += Buffer.byteLength(chunk);
				if (bytes > 1024 * 1024) stop("report.tooLarge");
				else stdout += chunk;
			});
			child.on("error", () => finish({ ok: false, error: "report.prepareFailed" }));
			child.on("close", (code) => {
				if (settled) return;
				try {
					if (code !== 0) throw new Error();
					const result = JSON.parse(stdout);
					if (!result || !["dir", "prompt", "context", "report", "playbook"].every((key) => typeof result[key] === "string")) throw new Error();
					finish({ ok: true, prompt: result.prompt, dir: result.dir });
				} catch { finish({ ok: false, error: "report.prepareFailed" }); }
			});
		});
	} catch { return { ok: false, error: "report.prepareFailed" }; }
}

export async function launchTriageInTerminal(
	stateEnv: NodeJS.ProcessEnv,
): Promise<TriageResult> {
	let manualCommand = "";
	try {
		// A configured daemon command may be a shell expression or a stale PATH ao.
		const launch = resolveDaemonLaunch({ ...stateEnv, AO_DAEMON_COMMAND: undefined }, app.isPackaged,
			process.resourcesPath, app.getAppPath(), os.homedir(), process.platform);
		if (!launch) throw new Error("The bundled binary is unavailable.");
		const args = ["triage"];
		// Terminal command text is visible and may enter shell history. Only state
		// paths belong here; the terminal's shell supplies credentials and PATH.
		const env: Record<string, string> = {};
		if (stateEnv.AO_DATA_DIR && stateEnv.AO_DATA_DIR !== path.join(os.homedir(), ".ao", "data")) env.AO_DATA_DIR = stateEnv.AO_DATA_DIR;
		if (stateEnv.AO_RUN_FILE && stateEnv.AO_RUN_FILE !== path.join(os.homedir(), ".ao", "running.json")) env.AO_RUN_FILE = stateEnv.AO_RUN_FILE;
		const overrides = Object.entries(env);
		let argv: string[];
		if (process.platform === "win32") {
			// Encode paths before passing through cmd.exe, which expands %variables%
			// even inside quotes. PowerShell receives only a base64 argument.
			const quote = (value: string) => `'${value.replaceAll("'", "''")}'`;
			manualCommand = [...overrides.map(([key, value]) => `$env:${key}=${quote(value)}`), `& ${[launch.command, ...args].map(quote).join(" ")}`].join("; ");
			argv = [path.win32.join(stateEnv.SystemRoot || "C:\\Windows", "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-EncodedCommand", Buffer.from(manualCommand, "utf16le").toString("base64")];
		} else {
			argv = [...(overrides.length ? ["/usr/bin/env", ...overrides.map(([key, value]) => `${key}=${value}`)] : []), launch.command, ...args];
			const quote = (value: string) => `'${value.replaceAll("'", `'\\''`)}'`;
			manualCommand = [...overrides.map(([key, value]) => `${key}=${quote(value)}`), ...[launch.command, ...args].map(quote)].join(" ");
		}
		const terminal = terminalCommand(argv, process.platform, stateEnv, (candidate) => {
			try { accessSync(candidate, constants.X_OK); return true; } catch { return false; }
		});
		if (!terminal) throw new Error("No terminal was found.");
		await launchCommand(terminal.command, terminal.args, os.homedir(), { env: stateEnv });
		return { ok: true };
	} catch {
		return { ok: false, error: "report.terminalFailed", ...(manualCommand ? { command: manualCommand } : {}) };
	}
}
