// Developer Mode only. Links the global ao command to the CLI shipped inside the
// installed app. It never edits shell startup files and never replaces a
// command AO did not create.

import { randomUUID } from "node:crypto";

export const CLI_LINK_DIRECTORY = "/usr/local/bin";
export const CLI_LINK_NAME = "ao";
export const CLI_LINK_MARKER_FILE = "cli-link.json";

const SYSTEM_LINK_DIRECTORIES = new Set(["/usr/local/bin", "/opt/homebrew/bin", "/usr/bin"]);

export type CliLinkCode =
	| "developer-mode"
	| "unsupported"
	| "missing-app"
	| "permission"
	| "foreign"
	| "confirmation-required"
	| "version"
	| "io";

export type CliLinkCurrent =
	| { kind: "missing" }
	| { kind: "linked"; target: string; version: string }
	| { kind: "stale"; target: string | null }
	| { kind: "foreign"; target: string | null };

export type CliLinkStatus = {
	linkPath: string;
	proposedTarget: string | null;
	proposedVersion: string | null;
	current: CliLinkCurrent;
	message?: string;
	pathIssue?: string;
};

export type CliLinkFailure = {
	ok: false;
	code: CliLinkCode;
	message: string;
	linkPath?: string;
	currentTarget?: string | null;
	proposedTarget?: string | null;
};

export type CliLinkSuccess = {
	ok: true;
	changed: boolean;
	linkPath: string;
	targetPath: string;
	version: string;
};

export type CliLinkView = { ok: true; status: CliLinkStatus } | CliLinkFailure;
export type CliLinkResult = CliLinkSuccess | CliLinkFailure;

export type CliLinkIo = {
	lstat(path: string): Promise<{ symbolicLink: boolean } | null>;
	readlink(path: string): Promise<string>;
	readText(path: string): Promise<string | null>;
	writeText(path: string, text: string): Promise<void>;
	remove(path: string): Promise<void>;
	symlink(target: string, path: string): Promise<void>;
	rename(from: string, to: string): Promise<void>;
	canWrite(path: string): Promise<boolean>;
	canExecute(path: string): Promise<boolean>;
	version(executable: string): Promise<string>;
	resolve(path: string): string;
	parent(path: string): string;
	join(directory: string, name: string): string;
	realpath(path: string): Promise<string | null>;
};

export type CliLinkRequest = {
	developerMode: boolean;
	platform: NodeJS.Platform;
	isPackaged: boolean;
	appImage?: string;
	appCliPath: string;
	linkDirectory: string;
	dataDir: string;
	allowSystemLinkDir?: boolean;
	confirmReplacement?: boolean;
	expectedTarget?: string | null;
	appVersion?: string;
	shellPath?: string;
};

type Marker = { version: 1; linkPath: string; targetPath: string };

type ReadyLink = { ok: true; linkPath: string };

function failure(code: CliLinkCode, message: string, extra: Partial<CliLinkFailure> = {}): CliLinkFailure {
	return { ok: false, code, message, ...extra };
}

export function cliLinkSupport(input: {
	platform: NodeJS.Platform;
	isPackaged: boolean;
	appImage?: string;
	appCliPath?: string;
}): { ok: true } | { ok: false; message: string } {
	if (input.platform === "win32") {
		return {
			ok: false,
			message:
				"Windows is not supported. Agent Orchestrator does not add ao to PATH, and this action will not create an elevated symlink or edit shell configuration.",
		};
	}
	if (input.appImage) {
		return {
			ok: false,
			message:
				"AppImage is not supported. The bundled CLI is on a temporary mount that changes every launch, so a global link would go stale. Use a deb or rpm install, or run the AppImage directly.",
		};
	}
	if (input.platform === "darwin" && (input.appCliPath?.includes("/AppTranslocation/") || input.appCliPath?.startsWith("/Volumes/"))) {
		return { ok: false, message: "Move Agent Orchestrator to Applications before linking the global ao command." };
	}
	if (!input.isPackaged) {
		return {
			ok: false,
			message: "This checkout is not an installed app. Link the global ao command from the installed Agent Orchestrator app.",
		};
	}
	if (input.platform !== "darwin" && input.platform !== "linux") {
		return {
			ok: false,
			message: "This platform is not supported. The action only links ao on macOS and Linux.",
		};
	}
	return { ok: true };
}

function guard(io: CliLinkIo, request: CliLinkRequest): CliLinkFailure | ReadyLink {
	if (!request.developerMode) {
		return failure("developer-mode", "Turn on Developer mode to link the global ao command.");
	}
	const resolvedDir = io.resolve(request.linkDirectory);
	if (!request.allowSystemLinkDir && SYSTEM_LINK_DIRECTORIES.has(resolvedDir)) {
		throw new Error("refusing to inspect or change a real global CLI directory");
	}
	const support = cliLinkSupport(request);
	if (!support.ok) return failure("unsupported", support.message, { linkPath: io.join(resolvedDir, CLI_LINK_NAME) });
	return { ok: true, linkPath: io.join(resolvedDir, CLI_LINK_NAME) };
}

async function readMarker(io: CliLinkIo, dataDir: string): Promise<Marker | null> {
	const text = await io.readText(io.join(dataDir, CLI_LINK_MARKER_FILE));
	if (!text) return null;
	try {
		const parsed = JSON.parse(text) as Partial<Marker>;
		if (parsed.version !== 1 || typeof parsed.linkPath !== "string" || typeof parsed.targetPath !== "string") return null;
		return { version: 1, linkPath: parsed.linkPath, targetPath: parsed.targetPath };
	} catch {
		return null;
	}
}

export async function selectCliLinkDirectory(
	io: CliLinkIo,
	dataDir: string,
	shellPath: string | undefined,
	candidates: string[],
): Promise<string> {
	const allowed = new Set(candidates.map((directory) => io.resolve(directory)));
	const marker = await readMarker(io, dataDir);
	if (marker && allowed.has(io.parent(marker.linkPath)) && io.join(io.parent(marker.linkPath), CLI_LINK_NAME) === marker.linkPath) {
		const stat = await io.lstat(marker.linkPath);
		if (stat?.symbolicLink) {
			const target = await io.readlink(marker.linkPath).catch(() => null);
			if (target && (target.startsWith("/") ? target : io.resolve(io.join(io.parent(marker.linkPath), target))) === io.resolve(marker.targetPath)) {
				return io.parent(marker.linkPath);
			}
		}
	}
	for (const entry of shellPath?.split(":") ?? []) {
		if (!entry || !entry.startsWith("/")) continue;
		const directory = io.resolve(entry);
		if (!allowed.has(directory)) continue;
		if (await io.canWrite(directory)) return directory;
	}
	return io.resolve(candidates[0] ?? CLI_LINK_DIRECTORY);
}

async function freshShellPathIssue(io: CliLinkIo, shellPath: string | undefined, linkPath: string): Promise<string | undefined> {
	if (!shellPath) return undefined;
	const directories = shellPath.split(":").filter((directory) => directory && directory.startsWith("/"));
	const linkDirectory = io.parent(linkPath);
	const position = directories.findIndex((directory) => io.resolve(directory) === linkDirectory);
	if (position < 0) return `${linkDirectory} is not on the login shell PATH. Add it to PATH before linking.`;
	for (const directory of directories.slice(0, position)) {
		const candidate = io.join(directory, CLI_LINK_NAME);
		if (await io.canExecute(candidate)) return `A fresh shell finds ${candidate} before ${linkPath}. Choose an earlier writable PATH directory or remove that command before linking.`;
	}
	return undefined;
}

async function classify(io: CliLinkIo, linkPath: string, appCliPath: string, marker: Marker | null): Promise<CliLinkCurrent & { managed: boolean }> {
	const stat = await io.lstat(linkPath);
	if (!stat) return { kind: "missing", managed: false };
	if (!stat.symbolicLink) return { kind: "foreign", target: null, managed: false };
	let raw: string;
	try {
		raw = await io.readlink(linkPath);
	} catch {
		return { kind: "foreign", target: null, managed: false };
	}
	const absolute = raw.startsWith("/") ? raw : io.resolve(io.join(io.parent(linkPath), raw));
	const markerOwns = marker?.linkPath === linkPath && io.resolve(marker.targetPath) === absolute;
	const appReal = await io.realpath(appCliPath);
	const linkReal = await io.realpath(linkPath);
	const sameAsApp = Boolean(appReal && linkReal && appReal === linkReal) || absolute === io.resolve(appCliPath);
	if (!markerOwns) return { kind: "foreign", target: absolute, managed: false };
	if (!linkReal || !sameAsApp) return { kind: "stale", target: absolute, managed: true };
	return { kind: "linked", target: absolute, version: "", managed: true };
}

export async function inspectCliLink(io: CliLinkIo, request: CliLinkRequest): Promise<CliLinkView> {
	const gated = guard(io, request);
	if ("code" in gated) return gated;
	const marker = await readMarker(io, request.dataDir);
	const current = await classify(io, gated.linkPath, request.appCliPath, marker);
	let proposedTarget: string | null = null;
	let proposedVersion: string | null = null;
	let message: string | undefined;
	const pathIssue = await freshShellPathIssue(io, request.shellPath, gated.linkPath);
	if (await io.canExecute(request.appCliPath)) {
		proposedTarget = io.resolve(request.appCliPath);
		try {
			const binaryVersion = await io.version(proposedTarget);
			proposedVersion = request.appVersion ?? binaryVersion;
		} catch (error) {
			message = error instanceof Error ? error.message : "The app CLI did not report a version.";
		}
	} else {
		message = `The installed app CLI is missing or not executable (${request.appCliPath}).`;
	}
	if (current.kind === "linked") {
		try {
			current.version = request.appVersion ?? await io.version(gated.linkPath);
		} catch (error) {
			message = error instanceof Error ? error.message : "The linked command did not report a version.";
		}
	}
	if (current.kind === "foreign") {
		message = `The command at ${gated.linkPath} is not an Agent Orchestrator link. It was left unchanged.`;
	}
	if (current.kind === "missing" && !(await io.canWrite(io.parent(gated.linkPath)))) {
		message = await io.lstat(io.parent(gated.linkPath))
			? `Cannot write ${io.parent(gated.linkPath)}. This action will not change ownership or ask for a password.`
			: `The link directory ${io.parent(gated.linkPath)} does not exist.`;
	}
	if (pathIssue) message = [message, pathIssue].filter(Boolean).join(" ");
	return {
		ok: true,
		status: {
			linkPath: gated.linkPath,
			proposedTarget,
			proposedVersion,
			current,
			message,
			pathIssue,
		},
	};
}

async function writeMarker(io: CliLinkIo, request: CliLinkRequest, linkPath: string, targetPath: string): Promise<void> {
	const marker: Marker = { version: 1, linkPath, targetPath };
	await io.writeText(io.join(request.dataDir, CLI_LINK_MARKER_FILE), `${JSON.stringify(marker)}\n`);
}

export async function linkCli(io: CliLinkIo, request: CliLinkRequest): Promise<CliLinkResult> {
	const view = await inspectCliLink(io, request);
	if (!view.ok) return view;
	const { status } = view;
	if (status.current.kind === "foreign") {
		return failure("foreign", `The command at ${status.linkPath} is not an Agent Orchestrator link. It was left unchanged.`, {
			linkPath: status.linkPath,
			currentTarget: status.current.target,
			proposedTarget: status.proposedTarget,
		});
	}
	if (!status.proposedTarget) {
		return failure("missing-app", `The installed app CLI is missing or not executable (${request.appCliPath}).`, {
			linkPath: status.linkPath,
		});
	}
	if (!status.proposedVersion) {
		return failure("version", "The installed app CLI did not report a version.", {
			linkPath: status.linkPath,
			proposedTarget: status.proposedTarget,
		});
	}
	if (status.pathIssue) {
		return failure("unsupported", status.pathIssue, { linkPath: status.linkPath, proposedTarget: status.proposedTarget });
	}
	if (status.current.kind === "linked") {
		return {
			ok: true,
			changed: false,
			linkPath: status.linkPath,
			targetPath: status.proposedTarget,
			version: status.current.version || status.proposedVersion,
		};
	}
	if (status.current.kind === "stale" && !request.confirmReplacement) {
		return failure(
			"confirmation-required",
			`Replace ${status.current.target ?? status.linkPath} with ${status.proposedTarget}?`,
			{ linkPath: status.linkPath, currentTarget: status.current.target, proposedTarget: status.proposedTarget },
		);
	}
	if (status.current.kind === "stale" && request.expectedTarget !== status.current.target) {
		return failure("confirmation-required", "The current ao link changed. Review its target before relinking.", {
			linkPath: status.linkPath, currentTarget: status.current.target, proposedTarget: status.proposedTarget,
		});
	}
	if (!(await io.canWrite(io.parent(status.linkPath)))) {
		const exists = await io.lstat(io.parent(status.linkPath));
		return failure(exists ? "permission" : "io", exists
			? `Cannot write ${io.parent(status.linkPath)}. This action will not change ownership or ask for a password.`
			: `The link directory ${io.parent(status.linkPath)} does not exist.`, {
			linkPath: status.linkPath,
			proposedTarget: status.proposedTarget,
		});
	}
	const markerPath = io.join(request.dataDir, CLI_LINK_MARKER_FILE);
	const previousMarker = await io.readText(markerPath);
	const stagedPath = `${status.linkPath}.${randomUUID()}.tmp`;
	let staged = false;
	let markerChanged = false;
	let verifying = false;
	try {
		await io.symlink(status.proposedTarget, stagedPath);
		staged = true;
		verifying = true;
		await io.version(stagedPath);
		verifying = false;
		const latest = await classify(io, status.linkPath, request.appCliPath, await readMarker(io, request.dataDir));
		if (latest.kind !== status.current.kind || (latest.kind === "stale" && status.current.kind === "stale" && latest.target !== status.current.target)) {
			return failure("confirmation-required", "The current ao link changed. Review its target before linking.", { linkPath: status.linkPath });
		}
		await writeMarker(io, request, status.linkPath, status.proposedTarget);
		markerChanged = true;
		if (status.current.kind === "missing") {
			await io.symlink(status.proposedTarget, status.linkPath);
		} else {
			await io.rename(stagedPath, status.linkPath);
			staged = false;
		}
	} catch (error) {
		if (markerChanged) {
			if (previousMarker === null) await io.remove(markerPath);
			else await io.writeText(markerPath, previousMarker);
		}
		const code = error instanceof Error && "code" in error && (error as NodeJS.ErrnoException).code;
		return failure(code === "EACCES" || code === "EPERM" ? "permission" : verifying ? "version" : "io", error instanceof Error ? error.message : "Could not create the ao link.", {
			linkPath: status.linkPath, proposedTarget: status.proposedTarget,
		});
	} finally {
		if (staged) await io.remove(stagedPath).catch(() => undefined);
	}
	return { ok: true, changed: true, linkPath: status.linkPath, targetPath: status.proposedTarget, version: status.proposedVersion };
}

export async function unlinkCli(io: CliLinkIo, request: CliLinkRequest): Promise<CliLinkResult> {
	const view = await inspectCliLink(io, request);
	if (!view.ok) return view;
	const { status } = view;
	if (status.current.kind === "foreign") {
		return failure("foreign", status.message ?? "The existing command is not an Agent Orchestrator link.", {
			linkPath: status.linkPath, currentTarget: status.current.target,
		});
	}
	if (!request.confirmReplacement || request.expectedTarget !== (status.current.kind === "missing" ? null : status.current.target)) {
		return failure("confirmation-required", `Remove the Agent Orchestrator link at ${status.linkPath}?`, {
			linkPath: status.linkPath,
			currentTarget: status.current.kind === "missing" ? null : status.current.target,
		});
	}
	if (status.current.kind === "missing") {
		return failure("io", `No ao command exists at ${status.linkPath}.`, { linkPath: status.linkPath });
	}
	const latest = await classify(io, status.linkPath, request.appCliPath, await readMarker(io, request.dataDir));
	if (latest.kind === "foreign" || latest.kind === "missing" || latest.target !== status.current.target) {
		return failure("foreign", `The command at ${status.linkPath} is not an Agent Orchestrator link. It was left unchanged.`, {
			linkPath: status.linkPath,
		});
	}
	try {
		const markerPath = io.join(request.dataDir, CLI_LINK_MARKER_FILE);
		const markerText = await io.readText(markerPath);
		await io.remove(markerPath);
		try {
			await io.remove(status.linkPath);
		} catch (error) {
			if (markerText !== null) await io.writeText(markerPath, markerText);
			throw error;
		}
	} catch (error) {
		const code = error instanceof Error && "code" in error && (error as NodeJS.ErrnoException).code;
		if (code === "EACCES" || code === "EPERM") {
			return failure("permission", `Cannot remove ${status.linkPath}. This action will not change ownership or ask for a password.`, {
				linkPath: status.linkPath,
			});
		}
		return failure("io", error instanceof Error ? error.message : "Could not remove the link.", { linkPath: status.linkPath });
	}
	return {
		ok: true,
		changed: true,
		linkPath: status.linkPath,
		targetPath: status.proposedTarget ?? "",
		version: status.proposedVersion ?? "",
	};
}
