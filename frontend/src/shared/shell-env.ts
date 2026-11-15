import type { TerminalShellPreference } from "./ui-locale";

export const SHELL_ENV_SENTINEL = "__AO_SHELL_ENV__";

export const FALLBACK_PATH_DIRS = [
	"/opt/homebrew/bin",
	"/opt/homebrew/sbin",
	"/usr/local/bin",
	"/usr/bin",
	"/bin",
	"/usr/sbin",
	"/sbin",
];

export function shellEnvArgs(): string[] {
	return ["-ilc", `printf '%s' '${SHELL_ENV_SENTINEL}'; env -0`];
}

export type WindowsExecutableLookup = (candidate: string) => string | null;

export type ShellEnvProbe = {
	shellPath: string;
	args: string[];
};

function windowsDirname(value: string): string {
	const normalized = value.replaceAll("\\", "/").replace(/\/+$/, "");
	const slash = normalized.lastIndexOf("/");
	return slash > 0 ? normalized.slice(0, slash) : "";
}

function windowsJoin(...parts: string[]): string {
	return parts
		.map((part) => part.replaceAll("/", "\\").replace(/^[\\/]+|[\\/]+$/g, ""))
		.filter(Boolean)
		.join("\\");
}

function windowsBasename(value: string): string {
	const normalized = value.replaceAll("\\", "/");
	return normalized.slice(normalized.lastIndexOf("/") + 1).toLowerCase();
}

function windowsProbeArgs(shellPath: string): string[] {
	switch (windowsBasename(shellPath)) {
		case "bash.exe":
		case "sh.exe":
			return ["--login", "-i", "-c", `printf '%s' '${SHELL_ENV_SENTINEL}'; env -0`];
		case "pwsh.exe":
		case "powershell.exe":
			return [
				"-NoLogo",
				"-Command",
				`Write-Output '${SHELL_ENV_SENTINEL}'; Get-ChildItem Env: | ForEach-Object { "$($_.Name)=$($_.Value)" }`,
			];
		case "cmd.exe":
			return ["/d", "/s", "/c", `echo ${SHELL_ENV_SENTINEL} & set`];
		default:
			return ["--login", "-i", "-c", `printf '%s' '${SHELL_ENV_SENTINEL}'; env -0`];
	}
}

function findGitBash(lookup: WindowsExecutableLookup): string | null {
	const gitPath = lookup("git.exe");
	const candidates = gitPath
		? [
				windowsJoin(windowsDirname(windowsDirname(gitPath)), "bin", "bash.exe"),
				windowsJoin(windowsDirname(windowsDirname(gitPath)), "usr", "bin", "bash.exe"),
			]
		: [];
	candidates.push("bash.exe");
	for (const candidate of candidates) {
		const resolved = lookup(candidate);
		if (resolved) return resolved;
	}
	return null;
}

export function resolveWindowsShellProbe(
	preference: TerminalShellPreference,
	env: Record<string, string | undefined>,
	lookup: WindowsExecutableLookup,
): ShellEnvProbe | null {
	const candidates: string[] = [];
	switch (preference.kind) {
		case "auto":
			candidates.push("pwsh.exe", "powershell.exe");
			if (env.ComSpec?.trim()) candidates.push(env.ComSpec.trim());
			candidates.push("cmd.exe");
			break;
		case "git-bash": {
			const resolved = findGitBash(lookup);
			if (resolved) candidates.push(resolved);
			break;
		}
		case "pwsh":
			candidates.push("pwsh.exe");
			break;
		case "powershell":
			candidates.push("powershell.exe");
			break;
		case "cmd":
			if (env.ComSpec?.trim()) candidates.push(env.ComSpec.trim());
			candidates.push("cmd.exe");
			break;
		case "custom":
			if (preference.path?.trim()) candidates.push(preference.path.trim());
			break;
	}
	for (const candidate of candidates) {
		const shellPath = lookup(candidate);
		if (shellPath) return { shellPath, args: windowsProbeArgs(shellPath) };
	}
	return null;
}

export function parseEnvBlock(stdout: string): Record<string, string> {
	const at = stdout.lastIndexOf(SHELL_ENV_SENTINEL);
	const block = at === -1 ? stdout : stdout.slice(at + SHELL_ENV_SENTINEL.length);
	const out: Record<string, string> = {};
	const records = block.includes("\0") ? block.split("\0") : block.split(/\r?\n/);
	for (const rec of records) {
		if (rec === "") continue;
		const eq = rec.indexOf("=");
		if (eq <= 0) continue;
		out[rec.slice(0, eq)] = rec.slice(eq + 1);
	}
	return out;
}

export function resolveShellPath(
	env: Record<string, string | undefined>,
	configuredLoginShell?: string,
): string {
	const shell = env.SHELL?.trim();
	const configured = configuredLoginShell?.trim();
	if ((!shell || shell === "/bin/sh") && configured) return configured;
	return shell && shell.length > 0 ? shell : "/bin/zsh";
}

export function withFallbackPath(currentPath: string | undefined): string {
	const result = (currentPath ?? "").split(":").filter(Boolean);
	const present = new Set(result);
	for (const dir of FALLBACK_PATH_DIRS) {
		if (!present.has(dir)) {
			present.add(dir);
			result.push(dir);
		}
	}
	return result.join(":");
}

function normalizeTerm(term: string | undefined): string {
	const trimmed = term?.trim();
	if (!trimmed || trimmed === "dumb") return "xterm-256color";
	return trimmed;
}

export function buildDaemonEnv(
	processEnv: NodeJS.ProcessEnv,
	shellEnv: Record<string, string> | null,
	overrides: Record<string, string>,
	configuredLoginShell?: string,
): NodeJS.ProcessEnv {
	const merged: NodeJS.ProcessEnv = { TERM: "xterm-256color", ...(shellEnv ?? {}), ...processEnv };
	merged.PATH = withFallbackPath(shellEnv?.PATH ?? processEnv.PATH);
	const configured = configuredLoginShell?.trim();
	const processShell = processEnv.SHELL?.trim();
	if (shellEnv?.SHELL) merged.SHELL = shellEnv.SHELL;
	else if (configured && (!processShell || processShell === "/bin/sh")) merged.SHELL = configured;
	merged.TERM = normalizeTerm(merged.TERM);
	return { ...merged, ...overrides };
}

export function devDaemonAllowedOrigins(
	configuredAllowedOrigins: string | undefined,
	rendererURL: string,
): string {
	if (configuredAllowedOrigins?.trim()) return configuredAllowedOrigins;
	return `app://renderer,${new URL(rendererURL).origin}`;
}

export type ShellRunner = (shellPath: string, args: string[]) => Promise<string | null>;

export async function resolveShellEnvWithSpec(spec: ShellEnvProbe, run: ShellRunner): Promise<Record<string, string> | null> {
	try {
		const stdout = await run(spec.shellPath, spec.args);
		if (stdout == null) return null;
		const parsed = parseEnvBlock(stdout);
		return parsed.PATH ? parsed : null;
	} catch {
		return null;
	}
}

export async function resolveShellEnv(
	env: Record<string, string | undefined>,
	run: ShellRunner,
	configuredLoginShell?: string,
): Promise<Record<string, string> | null> {
	const shellPath = resolveShellPath(env, configuredLoginShell);
	const resolved = await resolveShellEnvWithSpec({ shellPath, args: shellEnvArgs() }, run);
	return resolved ? { ...resolved, SHELL: shellPath } : null;
}
