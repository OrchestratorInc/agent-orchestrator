import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import semver from "semver";

export type RelocationAction =
	| "stay"
	| "relocate"
	| "handoff";

const APPLICATIONS_DIR = "/Applications";

export interface RelocationInputs {
	inApplicationsFolder: boolean;
	installedPresent: boolean;
	installedVersion: string | null;
	runningVersion: string;
}

export function decideRelocation(inputs: RelocationInputs): RelocationAction {
	if (inputs.inApplicationsFolder) return "stay";
	if (!inputs.installedPresent) return "relocate";

	const installed = semver.valid(inputs.installedVersion ?? "");
	const running = semver.valid(inputs.runningVersion);
	if (installed === null || running === null) return "stay";

	return semver.lt(installed, running) ? "relocate" : "handoff";
}

export function installedBundlePath(runningBundlePath: string): string {
	return path.join(APPLICATIONS_DIR, path.basename(runningBundlePath));
}

export function readBundleVersion(bundlePath: string): string | null {
	try {
		const plist = readFileSync(
			path.join(bundlePath, "Contents", "Info.plist"),
			"utf8",
		);
		const match =
			/<key>CFBundleShortVersionString<\/key>\s*<string>([^<]*)<\/string>/.exec(
				plist,
			);
		return match?.[1]?.trim() || null;
	} catch {
		return null;
	}
}

export function inspectInstalledBundle(runningBundlePath: string): {
	installedPresent: boolean;
	installedVersion: string | null;
} {
	const installed = installedBundlePath(runningBundlePath);
	if (!existsSync(installed))
		return { installedPresent: false, installedVersion: null };
	return {
		installedPresent: true,
		installedVersion: readBundleVersion(installed),
	};
}
