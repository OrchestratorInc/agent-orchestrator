import { randomUUID } from "node:crypto";
import semver from "semver";
import { cliLinkSupport, type CliLinkIo } from "./cli-link";

export type CliShadowRelation = "older" | "same" | "newer" | "different";

export type CliShadowFix =
	| { kind: "replace"; replaces: "link" | "file" }
	| { kind: "manual"; command: string };

export type CliShadowStatus =
	| { kind: "match" }
	| { kind: "absent" }
	| { kind: "unknown" }
	| {
		kind: "shadowed";
		shadowPath: string;
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
	shellPath: string | undefined;
	bundledPath: string | undefined;
	isPackaged: boolean;
	appImage?: string;
	probeIdentity?: string;
};

export type CliShadowReplaceResult =
	| { ok: true }
	| { ok: false; code: "changed" | "permission" | "version" | "io"; message: string };

const CLI_NAME = "ao";

export function cliShadowEnabled(env: Record<string, string | undefined>, isPackaged: boolean): boolean {
	return isPackaged && !env.AO_DATA_DIR?.trim() && !env.AO_RUN_FILE?.trim();
}

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
		const target = await io.realpath(candidate);
		if (target === bundledReal) return { kind: "match" };
		const shadowTarget = target ?? candidate;
		const shadowIdentity = await io.identity(candidate);
		if (!shadowIdentity) return { kind: "unknown" };
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
