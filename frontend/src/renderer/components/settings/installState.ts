import type { components } from "../../../api/schema";

export type InstallJob = components["schemas"]["InstallJob"];

const DOWNLOAD_METHOD = "download";

export type InstallView =
	| { kind: "offer" }
	| { kind: "running" }
	| { kind: "downloading" }
	| { kind: "fallback"; reason: string }
	| { kind: "manual"; command: string; reason: string }
	| { kind: "failed"; reason: string }
	| { kind: "done" };

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
			return download ? { kind: "fallback", reason: job.error ?? "" } : { kind: "failed", reason: job.error ?? "" };
		default:
			return { kind: "offer" };
	}
}

export function showsExistingJob(job: InstallJob): boolean {
	return job.status === "running" || (job.status === "failed" && job.method === DOWNLOAD_METHOD);
}
