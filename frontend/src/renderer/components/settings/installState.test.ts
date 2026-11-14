import { describe, expect, it } from "vitest";
import { installView, showsExistingJob, type InstallJob } from "./installState";

const job = (over: Partial<InstallJob>): InstallJob => ({ target: "cloudflared", status: "idle", ...over }) as InstallJob;

describe("what to show while installing the connector", () => {
	it("offers the install before anything has run", () => {
		expect(installView(undefined, false)).toEqual({ kind: "offer" });
	});

	it("shows progress from the moment the request is sent", () => {
		expect(installView(undefined, true)).toEqual({ kind: "running" });
	});

	it("shows progress while the command runs", () => {
		expect(installView(job({ status: "running" }), false).kind).toBe("running");
	});

	it("reports success", () => {
		expect(installView(job({ status: "succeeded" }), false)).toEqual({ kind: "done" });
	});

	it("hands over the command when AO cannot run it itself", () => {
		const got = installView(
			job({ status: "unsupported", command: "sudo apt-get install -y cloudflared", error: "needs root" }),
			false,
		);
		expect(got).toEqual({
			kind: "manual",
			command: "sudo apt-get install -y cloudflared",
			reason: "needs root",
		});
	});

	it("reports a dead end when there is no command to offer", () => {
		expect(installView(job({ status: "unsupported", error: "no package manager found" }), false)).toEqual({
			kind: "failed",
			reason: "no package manager found",
		});
	});

	it("reports a failure with its reason", () => {
		expect(installView(job({ status: "failed", error: "brew exited 1" }), false)).toEqual({
			kind: "failed",
			reason: "brew exited 1",
		});
	});

	it("labels AO's own download as a download", () => {
		expect(installView(job({ status: "running", method: "download" }), false)).toEqual({ kind: "downloading" });
	});

	it("offers the package manager after the download failed", () => {
		expect(installView(job({ status: "failed", method: "download", error: "checksum mismatch" }), false)).toEqual({
			kind: "fallback",
			reason: "checksum mismatch",
		});
	});

	it("adopts only a running job or a failed download when the panel opens", () => {
		expect(showsExistingJob(job({ status: "running", method: "download" }))).toBe(true);
		expect(showsExistingJob(job({ status: "failed", method: "download" }))).toBe(true);
		expect(showsExistingJob(job({ status: "idle" }))).toBe(false);
		expect(showsExistingJob(job({ status: "succeeded", method: "download" }))).toBe(false);
		expect(showsExistingJob(job({ status: "failed", method: "homebrew" }))).toBe(false);
	});
});
