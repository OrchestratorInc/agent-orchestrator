import { accessSync, constants } from "node:fs";
import { spawn } from "node:child_process";
import os from "node:os";
import path from "node:path";
import { app } from "electron";
import { resolveDaemonLaunch } from "../shared/daemon-launch";
import { launchCommand } from "./launch-command";
import { terminalCommand } from "./terminal-command";

export type TriageInput = { agent?: "claude" | "codex" };
export type TriageResult = { ok: true } | { ok: false; error: string };
export type TriagePrepareResult = { ok: true; prompt: string; dir: string } | { ok: false; error: string };

export async function prepareTriage(stateEnv: NodeJS.ProcessEnv): Promise<TriagePrepareResult> {
	try {
		const launch = resolveDaemonLaunch({ ...process.env, AO_DAEMON_COMMAND: undefined }, app.isPackaged,
			process.resourcesPath, app.getAppPath(), os.homedir(), process.platform);
		if (!launch) throw new Error("The bundled binary is unavailable.");
		return await new Promise<TriagePrepareResult>((resolve) => {
			const child = spawn(launch.command, ["triage", "--no-launch", "--json"], {
				env: { ...process.env, ...stateEnv }, stdio: ["ignore", "pipe", "ignore"], windowsHide: true,
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
			const timeout = setTimeout(() => stop("Preparing the report timed out. Try again."), 20_000);
			child.stdout.setEncoding("utf8");
			child.stdout.on("data", (chunk: string) => {
				if (settled) return;
				bytes += Buffer.byteLength(chunk);
				if (bytes > 1024 * 1024) stop("The report response was too large.");
				else stdout += chunk;
			});
			child.on("error", () => finish({ ok: false, error: "Could not prepare the report. Try again." }));
			child.on("close", (code) => {
				if (settled) return;
				try {
					if (code !== 0) throw new Error();
					const result = JSON.parse(stdout);
					if (!result || !["dir", "prompt", "context", "report", "playbook"].every((key) => typeof result[key] === "string")) throw new Error();
					finish({ ok: true, prompt: result.prompt, dir: result.dir });
				} catch { finish({ ok: false, error: "Could not prepare the report. Try again." }); }
			});
		});
	} catch { return { ok: false, error: "Could not prepare the report. Try again." }; }
}

export async function launchTriageInTerminal(
	input: TriageInput = {},
	stateEnv: NodeJS.ProcessEnv = {},
): Promise<TriageResult> {
	if (!input || typeof input !== "object" || (input.agent !== undefined && input.agent !== "claude" && input.agent !== "codex")) {
		return { ok: false, error: "Choose Claude Code, Codex, or Auto." };
	}
	let manualCommand = "";
	try {
		// A configured daemon command may be a shell expression or a stale PATH ao.
		const launch = resolveDaemonLaunch({ ...process.env, AO_DAEMON_COMMAND: undefined }, app.isPackaged,
			process.resourcesPath, app.getAppPath(), os.homedir(), process.platform);
		if (!launch) throw new Error("The bundled binary is unavailable.");
		const args = ["triage", ...(input.agent ? ["--agent", input.agent] : [])];
		const env = {
			AO_DATA_DIR: stateEnv.AO_DATA_DIR || path.join(os.homedir(), ".ao"),
			AO_RUN_FILE: stateEnv.AO_RUN_FILE || path.join(os.homedir(), ".ao", "running.json"),
		};
		let argv: string[];
		if (process.platform === "win32") {
			// Encode paths before passing through cmd.exe, which expands %variables%
			// even inside quotes. PowerShell receives only a base64 argument.
			const quote = (value: string) => `'${value.replaceAll("'", "''")}'`;
			const script = `${Object.entries(env).map(([key, value]) => `$env:${key}=${quote(value)}`).join("; ")}; & ${[launch.command, ...args].map(quote).join(" ")}`;
			manualCommand = script;
			argv = [path.win32.join(process.env.SystemRoot || "C:\\Windows", "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-EncodedCommand", Buffer.from(script, "utf16le").toString("base64")];
		} else {
			argv = ["/usr/bin/env", ...Object.entries(env).map(([key, value]) => `${key}=${value}`), launch.command, ...args];
			const quote = (value: string) => `'${value.replaceAll("'", `'\\''`)}'`;
			manualCommand = `${Object.entries(env).map(([key, value]) => `${key}=${quote(value)}`).join(" ")} ${[launch.command, ...args].map(quote).join(" ")}`;
		}
		const terminal = terminalCommand(argv, process.platform, process.env, (candidate) => {
			try { accessSync(candidate, constants.X_OK); return true; } catch { return false; }
		});
		if (!terminal) throw new Error("No terminal was found.");
		await launchCommand(terminal.command, terminal.args, os.homedir());
		return { ok: true };
	} catch (error) {
		const message = error instanceof Error ? error.message : "Could not open a terminal. Try again.";
		return { ok: false, error: message + (manualCommand ? `\nRun this command in a terminal:\n${manualCommand}` : "") };
	}
}
