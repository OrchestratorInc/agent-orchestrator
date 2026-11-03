import type { components } from "../../../api/schema";

export type InstallJob = components["schemas"]["InstallJob"];

/** The daemon's method for AO's own pinned cloudflared download. */
const DOWNLOAD_METHOD = "download";

/** What the panel should show about installing the connector. */
export type InstallView =
	| { kind: "offer" }
	| { kind: "running" }
	| { kind: "downloading" }
	| { kind: "fallback"; reason: string }
	| { kind: "manual"; command: string; reason: string }
	| { kind: "failed"; reason: string }
	| { kind: "done" };

/**
 * Turns an install job into what the user should see.
 *
 * Kept separate from the component because the interesting cases are not
 * visual: Linux resolves as unsupported *with* the exact command, because AO
 * never asks for an administrator password — so "unsupported" there means
 * "here is what to run", not "this cannot work". Presenting that as a failure
 * would be wrong, and it is the case least likely to be exercised by hand.
 */
export function installView(job: InstallJob | undefined, starting: boolean): InstallView {
	if (starting) return { kind: "running" };
	if (!job) return { kind: "offer" };
	const download = job.method === DOWNLOAD_METHOD;
	switch (job.status) {
		case "running":
			return download ? { kind: "downloading" } : { kind: "running" };
		case "succeeded":
			return { kind: "done" };
		case "unsupported":
			return job.command
				? { kind: "manual", command: job.command, reason: job.error ?? "" }
				: { kind: "failed", reason: job.error ?? "" };
		case "failed":
			// The daemon's next install request runs the package-manager plan,
			// so a failed download is an offer, not a dead end.
			return download ? { kind: "fallback", reason: job.error ?? "" } : { kind: "failed", reason: job.error ?? "" };
		default:
			return { kind: "offer" };
	}
}

/**
 * Whether a job found when the panel opens belongs on screen. The daemon
 * starts its own download as soon as remote access needs the connector, so a
 * running job, or the download that failed, is shown as is. Anything else, such
 * as a plan preview or an old result, still offers the install.
 */
export function showsExistingJob(job: InstallJob): boolean {
	return job.status === "running" || (job.status === "failed" && job.method === DOWNLOAD_METHOD);
}
