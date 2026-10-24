import path from "node:path";

function shellQuote(value: string): string {
	return `'${value.replaceAll("'", `'\\''`)}'`;
}

function appleScriptString(value: string): string {
	return `"${value.replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`;
}

function windowsCommandArg(value: string): string {
	return `"${value.replaceAll('"', '""')}"`;
}

export const LINUX_TERMINAL_LAUNCHERS = [
	{ command: "x-terminal-emulator", argsBeforeCommand: ["-e"] },
	{ command: "gnome-terminal", argsBeforeCommand: ["--"] },
	{ command: "konsole", argsBeforeCommand: ["-e"] },
	{ command: "xfce4-terminal", argsBeforeCommand: ["--execute"] },
	{ command: "kitty", argsBeforeCommand: [] },
	{ command: "alacritty", argsBeforeCommand: ["-e"] },
] as const;

export function terminalCommand(
	argv: readonly string[], platform: NodeJS.Platform, env: NodeJS.ProcessEnv,
	isExecutable: (candidate: string) => boolean,
): { command: string; args: string[] } | undefined {
	if (platform === "darwin") {
		const script = `tell application "Terminal"\nactivate\ndo script ${appleScriptString(`exec ${argv.map(shellQuote).join(" ")}`)}\nend tell`;
		return { command: "/usr/bin/osascript", args: ["-e", script] };
	}
	if (platform === "win32") {
		// The launcher runs with null stdio, so a direct cmd.exe would get no
		// console. start opens a new console window with real input and output.
		const shell = env.ComSpec || env.COMSPEC || "cmd.exe";
		const inner = `${windowsCommandArg(shell)} /d /s /v:off /k "${argv.map(windowsCommandArg).join(" ")}"`;
		return { command: shell, args: ["/d", "/s", "/v:off", "/c", `"start "" ${inner}"`] };
	}
	for (const launcher of LINUX_TERMINAL_LAUNCHERS) {
		for (const dir of [...(env.PATH || "").split(path.posix.delimiter).filter(Boolean), "/usr/local/bin", "/usr/bin"]) {
			const command = path.posix.join(dir, launcher.command);
			if (isExecutable(command)) return { command, args: [...launcher.argsBeforeCommand, ...argv] };
		}
	}
	return undefined;
}
