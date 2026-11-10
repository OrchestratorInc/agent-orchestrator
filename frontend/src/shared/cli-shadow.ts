// Answers one question for the desktop app: which ao does a shell outside AO
// run? AO pins its own ao inside the sessions and terminals it starts, but it
// cannot reach Terminal.app or an IDE terminal, and a stale ao cannot warn
// about itself. Pure and dependency-injected so it stays testable under vitest.

import { randomUUID } from "node:crypto";
import semver from "semver";
import { cliLinkSupport, type CliLinkIo } from "./cli-link";

// "different" means the versions cannot be ordered, such as a "dev" build or an
// ao that did not answer.
export type CliShadowRelation = "older" | "same" | "newer" | "different";

export type CliShadowFix =
	| { kind: "replace"; replaces: "link" | "file" }
	// AO must not replace this one. The command is for the user to run.
	| { kind: "manual"; command: string };

export type CliShadowStatus =
	| { kind: "match" }
	| { kind: "absent" }
	// The answer is not knowable: Windows, a custom daemon command, or a login
	// shell probe that failed. Never warn on a guess: a fallback PATH is not the
	// PATH the user's shell has.
	| { kind: "unknown" }
	| {
		kind: "shadowed";
		shadowPath: string;
		// What shadowPath resolves to. A replace only goes ahead while it still does.
		shadowTarget: string;
		shadowIdentity: string;
		versionChecked: boolean;
		shadowVersion: string | null;
		bundledPath: string;
		bundledVersion: string | null;
		relation: CliShadowRelation;
		fix: CliShadowFix;
	};

export type CliShadowIo = Pick<
	CliLinkIo,
	"lstat" | "canWrite" | "realpath" | "join" | "parent" | "version" | "symlink" | "rename" | "remove"
> & {
	isExecutableFile(path: string): Promise<boolean>;
	identity(path: string): Promise<string | null>;
};

export type CliShadowRequest = {
	platform: NodeJS.Platform;
	// undefined when the login-shell probe failed.
	shellPath: string | undefined;
	// undefined when the app does not run a CLI binary of its own.
	bundledPath: string | undefined;
	isPackaged: boolean;
	appImage?: string;
	// Explicit permission to run ao version, scoped to the file the user saw.
	probeIdentity?: string;
};

export type CliShadowReplaceResult =
	| { ok: true }
	| { ok: false; code: "changed" | "permission" | "version" | "io"; message: string };

const CLI_NAME = "ao";

// A dev build runs a daemon from its checkout and an isolated profile (its own
// AO_DATA_DIR or AO_RUN_FILE) is a side-by-side instance, so the ao on PATH
// differs from theirs by design. Only the user's real install can be shadowed.
export function cliShadowEnabled(env: Record<string, string | undefined>, isPackaged: boolean): boolean {
	return isPackaged && !env.AO_DATA_DIR?.trim() && !env.AO_RUN_FILE?.trim();
}

// `ao version` prints "0.14.9-nightly.1 commit abc built ...", and older or
// third-party builds may word it differently, so take the first valid token.
function parseVersion(line: string): string | null {
	for (const token of line.split(/\s+/)) {
		const version = semver.valid(token);
		if (version) return version;
	}
	return null;
}

function compareVersions(shadow: string | null, bundled: string | null): CliShadowRelation {
	const shadowVersion = shadow && parseVersion(shadow);
	const bundledVersion = bundled && parseVersion(bundled);
	if (!shadowVersion || !bundledVersion) return "different";
	const order = semver.compare(shadowVersion, bundledVersion);
	if (order === 0) return "same";
	return order < 0 ? "older" : "newer";
}

function shellQuote(value: string): string {
	return /^[\w@%+=:,./-]+$/.test(value) ? value : `'${value.replaceAll("'", `'\\''`)}'`;
}

// Package-manager bin directories are usually writable, so a writable path says
// nothing about who owns the file: only where it really lives does.
function packageManagerRemoval(target: string): string | null {
	const formula = /\/Cellar\/([^/]+)\//.exec(target)?.[1];
	if (formula) return `brew uninstall ${shellQuote(formula)}`;
	const cask = /\/Caskroom\/([^/]+)\//.exec(target)?.[1];
	if (cask) return `brew uninstall --cask ${shellQuote(cask)}`;
	return null;
}

async function chooseFix(io: CliShadowIo, request: CliShadowRequest, bundledPath: string, shadowPath: string, shadowTarget: string): Promise<CliShadowFix> {
	const removal = packageManagerRemoval(shadowTarget);
	if (removal) return { kind: "manual", command: removal };
	if (!(await io.canWrite(io.parent(shadowPath)))) return { kind: "manual", command: `sudo rm ${shellQuote(shadowPath)}` };
	if (shadowTarget.includes("/node_modules/")) return { kind: "manual", command: `rm ${shellQuote(shadowPath)}` };
	// A link into a translocated app, an AppImage mount, or a checkout goes stale.
	const support = cliLinkSupport({ platform: request.platform, isPackaged: request.isPackaged, appImage: request.appImage, appCliPath: bundledPath });
	if (!support.ok) return { kind: "manual", command: `rm ${shellQuote(shadowPath)}` };
	const stat = await io.lstat(shadowPath);
	return { kind: "replace", replaces: stat?.symbolicLink ? "link" : "file" };
}

export async function inspectCliShadow(io: CliShadowIo, request: CliShadowRequest): Promise<CliShadowStatus> {
	const { platform, shellPath, bundledPath } = request;
	if (platform === "win32" || !shellPath || !bundledPath) return { kind: "unknown" };
	const bundledReal = await io.realpath(bundledPath);
	if (!bundledReal) return { kind: "unknown" };
	const directories = shellPath.split(":").filter((directory) => directory.startsWith("/"));
	for (const directory of directories) {
		const candidate = io.join(directory, CLI_NAME);
		if (!(await io.isExecutableFile(candidate))) continue;
		// Resolving both sides keeps a symlink to the app's binary, such as the
		// Developer Mode CLI link, from reading as a shadow.
		const target = await io.realpath(candidate);
		if (target === bundledReal) return { kind: "match" };
		const shadowTarget = target ?? candidate;
		const shadowIdentity = await io.identity(candidate);
		if (!shadowIdentity) return { kind: "unknown" };
		// Only an explicit check of this exact file may execute the shadow CLI.
		const [shadowLine, bundledLine, fix] = await Promise.all([
			(request.probeIdentity === shadowIdentity) ? io.version(candidate).catch(() => null) : Promise.resolve(null),
			io.version(bundledPath).catch(() => null),
			chooseFix(io, request, bundledPath, candidate, shadowTarget),
		]);
		return {
			kind: "shadowed",
			shadowPath: candidate,
			shadowTarget,
			shadowIdentity,
			versionChecked: request.probeIdentity === shadowIdentity,
			shadowVersion: shadowLine && (parseVersion(shadowLine) ?? shadowLine),
			bundledPath,
			bundledVersion: bundledLine && (parseVersion(bundledLine) ?? bundledLine),
			relation: compareVersions(shadowLine, bundledLine),
			fix,
		};
	}
	return { kind: "absent" };
}

// Swaps the other ao for a symlink to the app's. The caller names what the user
// confirmed, and this re-checks it, so the renderer cannot aim the swap at any
// other path and a file that changed behind the dialog is left alone. Renaming
// over the path replaces a symlink itself, never the file it points to. Unlike
// the Developer Mode link this writes no ownership marker: the replaced path
// may not be one that flow manages.
export async function replaceCliShadow(
	io: CliShadowIo,
	request: CliShadowRequest,
	expected: { path: string; target: string; identity: string },
): Promise<CliShadowReplaceResult> {
	const changed = (): CliShadowReplaceResult => ({ ok: false, code: "changed", message: `${expected.path} changed. Review it before replacing.` });
	const status = await inspectCliShadow(io, request);
	if (status.kind !== "shadowed" || status.fix.kind !== "replace" || status.shadowPath !== expected.path || status.shadowTarget !== expected.target || status.shadowIdentity !== expected.identity) return changed();
	const stagedPath = `${expected.path}.${randomUUID()}.tmp`;
	let staged = false;
	let verifying = false;
	try {
		await io.symlink(status.bundledPath, stagedPath);
		staged = true;
		verifying = true;
		await io.version(stagedPath);
		verifying = false;
		if ((await io.realpath(expected.path)) !== expected.target || (await io.identity(expected.path)) !== expected.identity) return changed();
		await io.rename(stagedPath, expected.path);
		staged = false;
		return { ok: true };
	} catch (error) {
		const code = error instanceof Error && "code" in error ? (error as NodeJS.ErrnoException).code : undefined;
		const message = error instanceof Error ? error.message : `Could not replace ${expected.path}.`;
		return { ok: false, code: code === "EACCES" || code === "EPERM" ? "permission" : verifying ? "version" : "io", message };
	} finally {
		if (staged) await io.remove(stagedPath).catch(() => undefined);
	}
}
