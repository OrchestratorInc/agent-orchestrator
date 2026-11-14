import { accessSync, constants, statSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { app } from "electron";
import { resolveDaemonLaunch } from "../shared/daemon-launch";
import { launchCommand } from "./launch-command";
import { terminalCommand } from "./terminal-command";

export type TriageErrorKey = "report.terminalFailed" | "report.desktopRequired";
export type TriageResult = { ok: true } | { ok: false; error: TriageErrorKey; command?: string };

function resolveTriageBinary(stateEnv: NodeJS.ProcessEnv): string {
	const configured = stateEnv.AO_DAEMON_COMMAND?.trim();
	if (configured && path.isAbsolute(configured)) {
		try {
			if (statSync(configured).isFile()) {
				accessSync(configured, constants.X_OK);
				return configured;
			}
		} catch { /* Fall back to the bundled or dev binary. */ }
	}
	// Ignore shell expressions and PATH commands, which may resolve to a stale ao.
	const launch = resolveDaemonLaunch({ ...stateEnv, AO_DAEMON_COMMAND: undefined }, app.isPackaged,
		process.resourcesPath, app.getAppPath(), os.homedir(), process.platform);
	if (!launch) throw new Error("The bundled binary is unavailable.");
	return launch.command;
}

// Linux terminals close when their command exits, which would take the issue
// URL or failure ao triage prints last with them. Windows has -NoExit and macOS
// Terminal keeps the window.
const KEEP_WINDOW_OPEN = `"$@"; status=$?; printf '\\nPress Enter to close this window.'; read -r reply; exit "$status"`;

export async function launchTriageInTerminal(
	stateEnv: NodeJS.ProcessEnv,
): Promise<TriageResult> {
	let manualCommand = "";
	try {
		const command = resolveTriageBinary(stateEnv);
		const args = ["triage"];
		// Terminal command text is visible and may enter shell history. Only state
		// paths belong here; the terminal's shell supplies credentials and PATH.
		// Pass them even at their defaults so the shell's own exports cannot
		// point triage at another instance.
		const env: Record<string, string> = {};
		if (stateEnv.AO_DATA_DIR) env.AO_DATA_DIR = stateEnv.AO_DATA_DIR;
		if (stateEnv.AO_RUN_FILE) env.AO_RUN_FILE = stateEnv.AO_RUN_FILE;
		const overrides = Object.entries(env);
		let argv: string[];
		if (process.platform === "win32") {
			// Encode paths before passing through cmd.exe, which expands %variables%
			// even inside quotes. PowerShell receives only a base64 argument.
			const quote = (value: string) => `'${value.replaceAll("'", "''")}'`;
			manualCommand = [...overrides.map(([key, value]) => `$env:${key}=${quote(value)}`), `& ${[command, ...args].map(quote).join(" ")}`].join("; ");
			argv = [path.win32.join(stateEnv.SystemRoot || "C:\\Windows", "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoExit", "-NoProfile", "-EncodedCommand", Buffer.from(manualCommand, "utf16le").toString("base64")];
		} else {
			argv = [...(overrides.length ? ["/usr/bin/env", ...overrides.map(([key, value]) => `${key}=${value}`)] : []), command, ...args];
			const quote = (value: string) => `'${value.replaceAll("'", `'\\''`)}'`;
			manualCommand = [...overrides.map(([key, value]) => `${key}=${quote(value)}`), ...[command, ...args].map(quote)].join(" ");
			if (process.platform !== "darwin") argv = ["/bin/sh", "-c", KEEP_WINDOW_OPEN, "sh", ...argv];
		}
		const terminal = terminalCommand(argv, process.platform, stateEnv, (candidate) => {
			try { accessSync(candidate, constants.X_OK); return true; } catch { return false; }
		});
		if (!terminal) throw new Error("No terminal was found.");
		await launchCommand(terminal.command, terminal.args, os.homedir(), { env: stateEnv });
		return { ok: true };
	} catch {
		return { ok: false, error: "report.terminalFailed" as const, ...(manualCommand ? { command: manualCommand } : {}) };
	}
}
