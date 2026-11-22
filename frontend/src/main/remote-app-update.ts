import type { UpdateInstallResult, UpdateStatus } from "./update-settings";

export const REMOTE_APP_UPDATE_ACTION = "__app-update";

export interface RemoteAppUpdateDeps {
	packaged: boolean;
	hasUnsavedDrafts: () => boolean;
	installBlocker: () => string | undefined;
	check: () => Promise<void>;
	download: () => Promise<void>;
	status: () => UpdateStatus;
	install: (version: string) => Promise<UpdateInstallResult>;
}

function refuseUnsafe(deps: RemoteAppUpdateDeps): void {
	if (!deps.packaged) throw new Error("Updates are only available in the installed app.");
	if (deps.hasUnsavedDrafts()) throw new Error("Orchestrator on that computer has a draft that isn't saved yet. Update it there.");
	const blocker = deps.installBlocker();
	if (blocker) throw new Error(blocker);
}

function stagedVersion(status: UpdateStatus): string | undefined {
	return status.staged?.version ?? (status.state === "downloaded" ? status.version : undefined);
}

function failure(status: UpdateStatus, fallback: string): Error | undefined {
	if (status.state === "error" || status.state === "unsupported") return new Error(status.message || fallback);
	return undefined;
}

export async function runRemoteAppUpdate(deps: RemoteAppUpdateDeps, args: Record<string, unknown>): Promise<{ version: string }> {
	refuseUnsafe(deps);
	if (args.step === "install") {
		if (typeof args.version !== "string" || !args.version) throw new Error("No update version to install.");
		const result = await deps.install(args.version);
		if (result) throw new Error(`Orchestrator ${result.version} is ready to install instead. Try again.`);
		return { version: args.version };
	}
	await deps.check();
	let status = deps.status();
	const checkFailure = failure(status, "Couldn't check for updates.");
	if (checkFailure) throw checkFailure;
	if (status.state === "available" && status.version !== stagedVersion(status)) {
		await deps.download();
		status = deps.status();
		const downloadFailure = failure(status, "Couldn't download the update.");
		if (downloadFailure) throw downloadFailure;
	}
	const version = stagedVersion(status);
	if (!version) throw new Error("No newer version is available for that computer.");
	if (version === args.notApplied) throw new Error(`The update to ${version} didn't apply on that computer. Update Orchestrator there.`);
	refuseUnsafe(deps);
	return { version };
}
