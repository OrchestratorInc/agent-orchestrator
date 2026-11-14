import semver from "semver";
import type { UpdateChannel } from "./update-settings";

const H48 = 48 * 60 * 60 * 1000;

export function evaluateEscalation(opts: {
	channel: UpdateChannel;
	stagedAt: number;
	now: number;
	important: boolean;
	runningVersion: string;
	latestStableVersion: string | undefined;
}): boolean {
	const { channel, stagedAt, now, important, runningVersion, latestStableVersion } = opts;

	if (channel === "latest") {
		return now - stagedAt >= H48;
	}

	if (important) return true;
	if (latestStableVersion !== undefined) {
		try {
			return semver.lt(runningVersion, latestStableVersion);
		} catch {
			return false;
		}
	}
	return false;
}
